/*
Copyright 2022 Koor Technologies, Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package collector

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ceph/go-ceph/rbd"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/multierr"
)

// QoS limits are stored as Ceph per image config overrides, so these are live
// enforcement values rather than annotations. Note that Ceph reads a configured
// 0 as "unlimited", which is why an absent key and a 0 are kept distinct: an
// absent key produces no series at all.
const (
	metaKeyQoSReadIOPS  = "conf_rbd_qos_read_iops_limit"
	metaKeyQoSWriteIOPS = "conf_rbd_qos_write_iops_limit"
)

// ownershipLabels pairs an RBD image metadata key with the Prometheus label it
// is exposed as, and its order defines the label order of the owner metric.
//
// This is the single place a key/label pair is declared; exposing an additional
// key the provisioning pipeline starts writing means adding one line here.
var ownershipLabels = []struct {
	key   string
	label string
}{
	{"e2e.resource_type", "resource_type"},
	{"e2e.vm_id", "vm_id"},
	{"e2e.project", "project"},
	{"e2e.customer_id", "customer_id"},
	{"e2e.customer_email", "customer_email"},
	{"e2e.billed_customer_crn", "billed_customer_crn"},
}

// rbdImageLabels identify an image and are shared by every per image metric.
var rbdImageLabels = []string{"pool", "namespace", "image"}

var (
	rbdImageProvisionedBytesDesc = prometheus.NewDesc(
		prometheus.BuildFQName(MetricsNamespace, "rbd_image", "provisioned_bytes"),
		"Provisioned (virtual) size of the RBD image in bytes",
		rbdImageLabels, nil)

	rbdImageCreateTimestampDesc = prometheus.NewDesc(
		prometheus.BuildFQName(MetricsNamespace, "rbd_image", "create_timestamp_seconds"),
		"Unix timestamp of when the RBD image was created",
		rbdImageLabels, nil)

	rbdImageQoSReadIOPSDesc = prometheus.NewDesc(
		prometheus.BuildFQName(MetricsNamespace, "rbd_image", "qos_read_iops_limit"),
		"Configured read IOPS limit per RBD image",
		rbdImageLabels, nil)

	rbdImageQoSWriteIOPSDesc = prometheus.NewDesc(
		prometheus.BuildFQName(MetricsNamespace, "rbd_image", "qos_write_iops_limit"),
		"Configured write IOPS limit per RBD image",
		rbdImageLabels, nil)

	rbdImageOwnerDesc = prometheus.NewDesc(
		prometheus.BuildFQName(MetricsNamespace, "rbd_image", "owner"),
		"Ownership mapping for the RBD image; value is always 1",
		ownerLabelNames(), nil)
)

// ownerLabelNames returns the owner metric's label names, derived from
// ownershipLabels so the two can never drift apart.
func ownerLabelNames() []string {
	names := append([]string{}, rbdImageLabels...)
	for _, o := range ownershipLabels {
		names = append(names, o.label)
	}

	return names
}

type RBDImages struct{}

func init() {
	Factories["rbd_images"] = NewRBDImages
}

func NewRBDImages() (Collector, error) {
	return &RBDImages{}, nil
}

// Update collects the cheap per image metrics. Everything here is answered from
// the image header and its metadata, so this collector is safe to refresh far
// more frequently than rbd_image_usage.
func (c *RBDImages) Update(ctx context.Context, client *Client, ch chan<- prometheus.Metric) error {
	return eachRBDImage(ctx, client, func(pool, namespace, name string, image *rbd.Image) error {
		var errs error

		if size, err := image.GetSize(); err != nil {
			errs = multierr.Append(errs, fmt.Errorf("failed to get size of image %s/%s. %w", pool, name, err))
		} else {
			ch <- prometheus.MustNewConstMetric(rbdImageProvisionedBytesDesc,
				prometheus.GaugeValue, float64(size), pool, namespace, name)
		}

		if created, err := image.GetCreateTimestamp(); err != nil {
			errs = multierr.Append(errs, fmt.Errorf("failed to get create timestamp of image %s/%s. %w", pool, name, err))
		} else {
			ch <- prometheus.MustNewConstMetric(rbdImageCreateTimestampDesc,
				prometheus.GaugeValue, float64(created.Sec), pool, namespace, name)
		}

		meta, err := image.ListMetadata()
		if err != nil {
			return multierr.Append(errs, fmt.Errorf("failed to list metadata of image %s/%s. %w", pool, name, err))
		}

		emitRBDImageOwner(ch, pool, namespace, name, meta)

		return multierr.Append(errs, emitRBDImageQoS(ch, pool, namespace, name, meta))
	})
}

// emitRBDImageOwner publishes the ownership mapping when the image carries at
// least one ownership key.
//
// Partially tagged images are still reported, with the missing labels left
// empty, so that they stay visible instead of silently dropping out of tenant
// queries. An image with no ownership keys at all produces no series, which
// makes untagged images countable by comparing against provisioned_bytes.
func emitRBDImageOwner(ch chan<- prometheus.Metric, pool, namespace, name string, meta map[string]string) {
	values := []string{pool, namespace, name}

	tagged := false
	for _, o := range ownershipLabels {
		value, ok := meta[o.key]
		if ok {
			tagged = true
		}
		values = append(values, value)
	}

	if !tagged {
		return
	}

	ch <- prometheus.MustNewConstMetric(rbdImageOwnerDesc, prometheus.GaugeValue, 1, values...)
}

// emitRBDImageQoS publishes a QoS limit only for images that actually carry the
// override, so that "no limit configured" stays distinguishable from a limit
// that is explicitly set to 0.
func emitRBDImageQoS(ch chan<- prometheus.Metric, pool, namespace, name string, meta map[string]string) error {
	limits := []struct {
		key  string
		desc *prometheus.Desc
	}{
		{metaKeyQoSReadIOPS, rbdImageQoSReadIOPSDesc},
		{metaKeyQoSWriteIOPS, rbdImageQoSWriteIOPSDesc},
	}

	var errs error
	for _, limit := range limits {
		raw, ok := meta[limit.key]
		if !ok {
			continue
		}

		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			errs = multierr.Append(errs, fmt.Errorf("failed to parse %s=%q of image %s/%s. %w", limit.key, raw, pool, name, err))
			continue
		}

		ch <- prometheus.MustNewConstMetric(limit.desc, prometheus.GaugeValue, value, pool, namespace, name)
	}

	return errs
}
