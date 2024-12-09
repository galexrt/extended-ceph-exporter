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
	"slices"

	"github.com/ceph/go-ceph/rados"
	"github.com/ceph/go-ceph/rbd"
	"github.com/galexrt/extended-ceph-exporter/pkg/config"
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

	if len(client.Config.RBD.Pools) > 0 {
		// Remove any pools not in our list
		pools = slices.DeleteFunc(pools, func(pool string) bool {
			return !slices.ContainsFunc(client.Config.RBD.Pools, func(rp *config.RBDPool) bool {
				return rp.Name == pool
			})
		})
	}

	var errs error
	// List pools and iterate over each
	for _, pool := range pools {
		ioctx, err := client.Rados.OpenIOContext(pool)
		if err != nil {
			errs = multierr.Append(errs, fmt.Errorf("failed to open rados IO context for %s pool. %w", pool, err))
			continue
		}

		namespaces := []string{
			rados.AllNamespaces,
		}

		if idx := slices.IndexFunc(client.Config.RBD.Pools, func(rp *config.RBDPool) bool {
			return rp.Name == pool
		}); idx > -1 {
			if len(client.Config.RBD.Pools[idx].Namespaces) > 0 {
				namespaces = client.Config.RBD.Pools[idx].Namespaces
			}
		}

		for _, namespace := range namespaces {
			ioctx.SetNamespace(namespace)

			images, err := rbd.GetImageNames(ioctx)
			if err != nil {
				errs = multierr.Append(errs, fmt.Errorf("failed to get image names from %s pool (namespace: %s). %w", pool, namespace, err))
				continue
			}

			for _, image := range images {
				info := rbd.GetImage(ioctx, image)

				id, err := info.GetId()
				if err != nil {
					errs = multierr.Append(errs, fmt.Errorf("failed to get image id for %s/%s (namespace: %s). %w", pool, image, namespace, err))
					continue
				}

				labels := map[string]string{
					"pool": pool,
					"id":   id,
					"name": image,
				}
				if namespace != rados.AllNamespaces {
					labels["namespace"] = namespace
				}

				size, err := info.GetSize()
				if err != nil {
					errs = multierr.Append(errs, fmt.Errorf("failed to get image size for %s/%s (namespace: %s). %w", pool, image, namespace, err))
					continue
				}

				c.current = prometheus.NewDesc(
					prometheus.BuildFQName(MetricsNamespace, "rbd", "volume_size"),
					"RBD Volume provisioned size",
					nil, labels)
				ch <- prometheus.MustNewConstMetric(
					c.current, prometheus.GaugeValue, float64(size))
			}
		}
	}

	return errs
}
