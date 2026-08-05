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

	"github.com/ceph/go-ceph/rados"
	rgwadmin "github.com/ceph/go-ceph/rgw/admin"
	"github.com/galexrt/extended-ceph-exporter/pkg/config"
	"github.com/prometheus/client_golang/prometheus"
)

// MetricsNamespace is the prefix shared by every metric this exporter emits.
// It is "custom" rather than "ceph" to keep the metric names compatible with
// the in-house RBD exporter this replaces (e.g. custom_rbd_image_owner).
const MetricsNamespace = "custom"

type Client struct {
	Name string

	Config *config.Config

	RGWAdminAPI *rgwadmin.API
	Rados       *rados.Conn
}

type Collector interface {
	Update(context.Context, *Client, chan<- prometheus.Metric) error
}

// errNoRGWAPI reports that an RGW collector ran against a client without an RGW
// API connection, which is the case for the synthetic client used when no realm
// is configured. Returning an error keeps it a visible collector failure rather
// than a nil dereference.
func errNoRGWAPI(client *Client) error {
	return fmt.Errorf("client %q has no RGW API connection, configure a realm in realms.yaml or disable the RGW collectors", client.Name)
}

type NewCollectorFunc func() (Collector, error)

var Factories map[string]NewCollectorFunc = map[string]NewCollectorFunc{}
