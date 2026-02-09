package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

type RGWLifecycle struct {
	// Placeholder for future metrics
}

func init() {
	Factories["rgw_lifecycle"] = NewRGWLifecycle
}

func NewRGWLifecycle() (Collector, error) {
	return &RGWLifecycle{}, nil
}

func (c *RGWLifecycle) Update(ctx context.Context, client *Client, ch chan<- prometheus.Metric) error {
	//state, err := client.RGWAdminAPI.
	//if err != nil {
	//	return err
	//}

	// TODO

	return nil
}
