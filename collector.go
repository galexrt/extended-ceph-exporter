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
	"fmt"
	"sync"
	"time"

	"github.com/galexrt/extended-ceph-exporter/collector"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

var (
	scrapeDurationDesc = prometheus.NewDesc(
		prometheus.BuildFQName(collector.MetricsNamespace, "scrape", "collector_duration_seconds"),
		"Duration of a collector scrape.",
		[]string{"collector", "realm"},
		nil,
	)
	scrapeSuccessDesc = prometheus.NewDesc(
		prometheus.BuildFQName(collector.MetricsNamespace, "scrape", "collector_success"),
		"Whether a collector succeeded.",
		[]string{"collector", "realm"},
		nil,
	)
)

// ExtendedCephMetricsCollector contains the collectors to be used
type ExtendedCephMetricsCollector struct {
	ctx             context.Context
	ctxTimeout      time.Duration
	logger          *zap.Logger
	lastCollectTime time.Time
	clients         map[string]*collector.Client
	collectors      map[string]collector.Collector

	// Cache related
	cachingEnabled bool
	cacheDuration  time.Duration
	cache          []prometheus.Metric
	cacheMutex     sync.Mutex
}

func NewExtendedCephMetricsCollector(ctx context.Context, logger *zap.Logger, clients map[string]*collector.Client, collectors map[string]collector.Collector, ctxTimeout time.Duration, cachingEnabled bool, cacheDuration time.Duration) *ExtendedCephMetricsCollector {
	return &ExtendedCephMetricsCollector{
		ctx:             ctx,
		ctxTimeout:      ctxTimeout,
		logger:          logger,
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
			n.logger.Debug(fmt.Sprintf("Using cache. Now: %s, Expiry: %s, LastCollect: %s", time.Now().String(), expiry.String(), n.lastCollectTime.String()))
			for _, cachedMetric := range n.cache {
				n.logger.Debug(fmt.Sprintf("Pushing cached metric %s to outgoingCh", cachedMetric.Desc().String()))
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
				n.logger.Debug(fmt.Sprintf("Appending metric %s to cache", metric.Desc().String()))
				n.cache = append(n.cache, metric)
			}
		}
		n.logger.Debug("Finished pushing metrics from metricsCh to outgoingCh")
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
					n.logger.Error(fmt.Sprintf("%s collector failed after %fs", collName, duration.Seconds()), zap.Error(err))
					success = 0
				} else {
					n.logger.Debug(fmt.Sprintf("%s collector succeeded after %fs.", collName, duration.Seconds()))
					success = 1
				}
				metricsCh <- prometheus.MustNewConstMetric(scrapeDurationDesc, prometheus.GaugeValue, duration.Seconds(), collName, clientName)
				metricsCh <- prometheus.MustNewConstMetric(scrapeSuccessDesc, prometheus.GaugeValue, success, collName, clientName)
			}(collName, coll, clientName, client)
		}
	}

	n.logger.Debug("Waiting for collectors")
	wgCollection.Wait()
	n.logger.Debug("Finished waiting for collectors")

	n.lastCollectTime = time.Now()
	n.logger.Debug(fmt.Sprintf("Updated lastCollectTime to %s", n.lastCollectTime.String()))

	close(metricsCh)

	n.logger.Debug("Waiting for outgoing Adapter")
	wgOutgoing.Wait()
	n.logger.Debug("Finished waiting for outgoing Adapter")
}
