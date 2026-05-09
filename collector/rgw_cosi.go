package collector

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	cosiapi "sigs.k8s.io/container-object-storage-interface/client/apis/objectstorage/v1alpha2"
)

type RGWCOSI struct {
	current *prometheus.Desc
}

func init() {
	Factories["rgw_cosi"] = NewRGWCOSI
}

func NewRGWCOSI() (Collector, error) {
	return &RGWCOSI{}, nil
}

func (c *RGWCOSI) Update(ctx context.Context, client *Client, ch chan<- prometheus.Metric) error {
	_ = &cosiapi.Bucket{}
	// TODO

	return nil
}
