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

package main

import (
	"context"
	"sync"
	"time"

	"github.com/galexrt/extended-ceph-exporter/collector"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

var (
	scrapeDurationDesc = prometheus.NewDesc(
		prometheus.BuildFQName(collector.Namespace, "scrape", "collector_duration_seconds"),
		"Duration of a collector scrape.",
		[]string{"collector", "realm"},
		nil,
	)
	scrapeSuccessDesc = prometheus.NewDesc(
		prometheus.BuildFQName(collector.Namespace, "scrape", "collector_success"),
		"Whether a collector succeeded.",
		[]string{"collector", "realm"},
		nil,
	)
)

// ExtendedCephMetricsCollector contains the collectors to be used
type ExtendedCephMetricsCollector struct {
	ctx             context.Context
	ctxTimeout      time.Duration
	log             *logrus.Logger
	lastCollectTime time.Time
	clients         map[string]*collector.Client
	collectors      map[string]collector.Collector

	// Cache related
	cachingEnabled bool
	cacheDuration  time.Duration
	cache          []prometheus.Metric
	cacheMutex     sync.Mutex
}

func NewExtendedCephMetricsCollector(ctx context.Context, log *logrus.Logger, clients map[string]*collector.Client, collectors map[string]collector.Collector, ctxTimeout time.Duration, cachingEnabled bool, cacheDuration time.Duration) *ExtendedCephMetricsCollector {
	return &ExtendedCephMetricsCollector{
		ctx:             ctx,
		ctxTimeout:      ctxTimeout,
		log:             log,
		lastCollectTime: time.Unix(0, 0),
		clients:         clients,
		collectors:      collectors,
		cache:           make([]prometheus.Metric, 0),
		cachingEnabled:  cachingEnabled,
		cacheDuration:   cacheDuration,
	}
}

// Describe implements the prometheus.Collector interface.
func (n *ExtendedCephMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- scrapeDurationDesc
	ch <- scrapeSuccessDesc
}

// Collect implements the prometheus.Collector interface.
func (n *ExtendedCephMetricsCollector) Collect(outgoingCh chan<- prometheus.Metric) {
	if n.cachingEnabled {
		n.cacheMutex.Lock()
		defer n.cacheMutex.Unlock()

		expiry := n.lastCollectTime.Add(n.cacheDuration)
		if time.Now().Before(expiry) {
			n.log.Debugf("Using cache. Now: %s, Expiry: %s, LastCollect: %s", time.Now().String(), expiry.String(), n.lastCollectTime.String())
			for _, cachedMetric := range n.cache {
				n.log.Debugf("Pushing cached metric %s to outgoingCh", cachedMetric.Desc().String())
				outgoingCh <- cachedMetric
			}
			return
		}
		// Clear cache, but keep slice
		n.cache = n.cache[:0]
	}

	metricsCh := make(chan prometheus.Metric)

	// Wait to ensure outgoingCh is not closed before the goroutine is finished
	wgOutgoing := sync.WaitGroup{}
	wgOutgoing.Add(1)
	go func() {
		defer wgOutgoing.Done()

		for metric := range metricsCh {
			outgoingCh <- metric
			if n.cachingEnabled {
				n.log.Debugf("Appending metric %s to cache", metric.Desc().String())
				n.cache = append(n.cache, metric)
			}
		}
		n.log.Debug("Finished pushing metrics from metricsCh to outgoingCh")
	}()

	wgCollection := sync.WaitGroup{}

	for collName, coll := range n.collectors {
		for clientName, client := range n.clients {
			wgCollection.Add(1)
			go func(collName string, coll collector.Collector, clientName string, client *collector.Client) {
				defer wgCollection.Done()

				begin := time.Now()
				ctx, cancel := context.WithTimeout(n.ctx, n.ctxTimeout)
				defer cancel()

				err := coll.Update(ctx, client, metricsCh)
				duration := time.Since(begin)
				var success float64

				if err != nil {
					n.log.Errorf("%s collector failed after %fs: %s", collName, duration.Seconds(), err)
					success = 0
				} else {
					n.log.Debugf("%s collector succeeded after %fs.", collName, duration.Seconds())
					success = 1
				}
				metricsCh <- prometheus.MustNewConstMetric(scrapeDurationDesc, prometheus.GaugeValue, duration.Seconds(), collName, clientName)
				metricsCh <- prometheus.MustNewConstMetric(scrapeSuccessDesc, prometheus.GaugeValue, success, collName, clientName)
			}(collName, coll, clientName, client)
		}
	}

	n.log.Debug("Waiting for collectors")
	wgCollection.Wait()
	n.log.Debug("Finished waiting for collectors")

	n.lastCollectTime = time.Now()
	n.log.Debugf("Updated lastCollectTime to %s", n.lastCollectTime.String())

	close(metricsCh)

	n.log.Debug("Waiting for outgoing Adapter")
	wgOutgoing.Wait()
	n.log.Debug("Finished waiting for outgoing Adapter")
}
