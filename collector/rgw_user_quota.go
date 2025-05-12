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

	"github.com/ceph/go-ceph/rgw/admin"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/multierr"
)

type RGWUserQuota struct {
	current *prometheus.Desc
}

func init() {
	Factories["rgw_user_quota"] = NewRGWUserQuota
}

func NewRGWUserQuota() (Collector, error) {
	return &RGWUserQuota{}, nil
}

func (c *RGWUserQuota) Update(ctx context.Context, client *Client, ch chan<- prometheus.Metric) error {
	// Get the "admin" user
	users, err := client.RGWAdminAPI.GetUsers(ctx)
	if err != nil {
		return err
	}

	var errs error
	// Iterate over users to get quota
	for _, user := range *users {
		userQuota, err := client.RGWAdminAPI.GetUserQuota(ctx, admin.QuotaSpec{
			UID: user,
		})
		if err != nil {
			errs = multierr.Append(errs, fmt.Errorf("failed to get user %q quota. %w", user, err))
			continue
		}

		// If quote nil/disabled, skip user
		if userQuota.Enabled == nil || !*userQuota.Enabled {
			continue
		}

		labels := map[string]string{
			"uid":   user,
			"realm": client.Name,
		}

		c.current = prometheus.NewDesc(
			prometheus.BuildFQName(MetricsNamespace, "rgw", "user_quota_max_size"),
			"RGW User Quota max size",
			nil, labels)
		ch <- prometheus.MustNewConstMetric(
			c.current, prometheus.GaugeValue, float64(*userQuota.MaxSize))

		c.current = prometheus.NewDesc(
			prometheus.BuildFQName(MetricsNamespace, "rgw", "user_quota_max_size_kb"),
			"RGW User Quota max size KiB",
			nil, labels)
		ch <- prometheus.MustNewConstMetric(
			c.current, prometheus.GaugeValue, float64(*userQuota.MaxSizeKb))

		c.current = prometheus.NewDesc(
			prometheus.BuildFQName(MetricsNamespace, "rgw", "user_quota_max_objects"),
			"RGW User Quota max objects",
			nil, labels)
		ch <- prometheus.MustNewConstMetric(
			c.current, prometheus.GaugeValue, float64(*userQuota.MaxObjects))
	}

	return errs
}
