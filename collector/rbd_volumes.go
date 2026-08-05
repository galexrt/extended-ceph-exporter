/*
Copyright 2024 Alexander Trost All rights reserved.

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

	"github.com/ceph/go-ceph/rbd"
	"github.com/prometheus/client_golang/prometheus"
)

var rbdVolumeSizeDesc = prometheus.NewDesc(
	prometheus.BuildFQName(MetricsNamespace, "rbd", "volume_size"),
	"RBD Volume provisioned size",
	[]string{"pool", "namespace", "id", "name"}, nil)

type RBDVolumes struct{}

func init() {
	Factories["rbd_volumes"] = NewRBDVolumes
}

func NewRBDVolumes() (Collector, error) {
	return &RBDVolumes{}, nil
}

// Update collects the provisioned size of every RBD volume.
//
// rbd_images reports the same size as custom_rbd_image_provisioned_bytes, with
// tenant ownership alongside it, so this collector only exists to keep the
// upstream metric available and is disabled by default.
func (c *RBDVolumes) Update(ctx context.Context, client *Client, ch chan<- prometheus.Metric) error {
	return eachRBDImage(ctx, client, func(pool, namespace, name string, image *rbd.Image) error {
		id, err := image.GetId()
		if err != nil {
			return fmt.Errorf("failed to get image id for %s/%s (namespace: %q). %w", pool, name, namespace, err)
		}

		size, err := image.GetSize()
		if err != nil {
			return fmt.Errorf("failed to get image size for %s/%s (namespace: %q). %w", pool, name, namespace, err)
		}

		ch <- prometheus.MustNewConstMetric(rbdVolumeSizeDesc,
			prometheus.GaugeValue, float64(size), pool, namespace, id, name)

		return nil
	})
}
