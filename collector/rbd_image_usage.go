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

	"github.com/ceph/go-ceph/rbd"
	"github.com/prometheus/client_golang/prometheus"
)

var rbdImageUsedBytesDesc = prometheus.NewDesc(
	prometheus.BuildFQName(MetricsNamespace, "rbd_image", "used_bytes"),
	"Actually-allocated bytes of the RBD image (thin-provisioning aware)",
	rbdImageLabels, nil)

type RBDImageUsage struct{}

func init() {
	Factories["rbd_image_usage"] = NewRBDImageUsage
}

func NewRBDImageUsage() (Collector, error) {
	return &RBDImageUsage{}, nil
}

// Update collects how much space each image actually occupies.
//
// This is the expensive collector. Unless the fast-diff image feature is
// enabled, librbd has to visit every backing object to answer, which is the
// same work `rbd du` performs. Give it a longer refresh interval than the other
// RBD collectors.
func (c *RBDImageUsage) Update(ctx context.Context, client *Client, ch chan<- prometheus.Metric) error {
	return eachRBDImage(ctx, client, func(pool, namespace, name string, image *rbd.Image) error {
		used, err := rbdImageUsedBytes(image)
		if err != nil {
			return fmt.Errorf("failed to determine disk usage of image %s/%s (namespace: %q). %w", pool, name, namespace, err)
		}

		ch <- prometheus.MustNewConstMetric(rbdImageUsedBytesDesc,
			prometheus.GaugeValue, float64(used), pool, namespace, name)

		return nil
	})
}

// rbdImageUsedBytes sums the allocated extents of an image, which is what
// `rbd du` reports as its usage.
//
// The parent is excluded so that a clone is only charged for the data it owns,
// and whole object mode lets librbd answer per backing object instead of
// scanning byte ranges.
func rbdImageUsedBytes(image *rbd.Image) (uint64, error) {
	size, err := image.GetSize()
	if err != nil {
		return 0, err
	}

	var used uint64
	err = image.DiffIterate(rbd.DiffIterateConfig{
		SnapName:      rbd.NoSnapshot,
		Offset:        0,
		Length:        size,
		IncludeParent: rbd.ExcludeParent,
		WholeObject:   rbd.EnableWholeObject,
		Callback: func(_, length uint64, exists int, _ interface{}) int {
			// exists is 0 for regions librbd knows to be zeroes.
			if exists != 0 {
				used += length
			}

			return 0
		},
	})
	if err != nil {
		return 0, err
	}

	return used, nil
}
