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

	"github.com/ceph/go-ceph/rbd"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/multierr"
)

type RBDVolumes struct {
	current *prometheus.Desc
}

func init() {
	Factories["rbd_volumes"] = NewRBDVolumes
}

func NewRBDVolumes() (Collector, error) {
	return &RBDVolumes{}, nil
}

func (c *RBDVolumes) Update(ctx context.Context, client *Client, ch chan<- prometheus.Metric) error {
	pools, err := client.Rados.ListPools()
	if err != nil {
		return err
	}

	var errs error
	// List pools and iterate over each
	// TODO add flag to provide list of pools to check
	for _, pool := range pools {
		ioctx, err := client.Rados.OpenIOContext(pool)
		if err != nil {
			return err
		}

		images, err := rbd.GetImageNames(ioctx)
		if err != nil {
			return err
		}

		for _, image := range images {
			info := rbd.GetImage(ioctx, image)

			id, err := info.GetId()
			if err != nil {
				errs = multierr.Append(errs, err)
				continue
			}

			labels := map[string]string{
				"pool": pool,
				"id":   id,
				"name": image,
			}

			size, err := info.GetSize()
			if err != nil {
				errs = multierr.Append(errs, err)
				continue
			}

			c.current = prometheus.NewDesc(
				prometheus.BuildFQName(Namespace, "rbd", "volume_size"),
				"RBD Volume provisioned size",
				nil, labels)
			ch <- prometheus.MustNewConstMetric(
				c.current, prometheus.GaugeValue, float64(size))
		}
	}

	return errs
}
