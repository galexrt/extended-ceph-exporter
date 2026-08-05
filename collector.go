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

// minRefreshInterval keeps a misconfigured interval from turning a refresher
// into a hot loop against the cluster.
const minRefreshInterval = time.Second

var (
	scrapeDurationDesc = prometheus.NewDesc(
		prometheus.BuildFQName(collector.MetricsNamespace, "scrape", "collector_duration_seconds"),
		"Duration of a collector scrape.",
		[]string{"collector", "client"},
		nil,
	)
	scrapeSuccessDesc = prometheus.NewDesc(
		prometheus.BuildFQName(collector.MetricsNamespace, "scrape", "collector_success"),
		"Whether a collector succeeded.",
		[]string{"collector", "client"},
		nil,
	)
	lastRefreshDesc = prometheus.NewDesc(
		prometheus.BuildFQName(collector.MetricsNamespace, "rbd", "last_refresh_timestamp_seconds"),
		"Unix timestamp of the last completed collection cycle, per collector.",
		[]string{"collector"},
		nil,
	)
)

// ExtendedCephMetricsCollector contains the collectors to be used
//
// Every enabled collector is refreshed by its own background goroutine on its
// own interval, and scrapes only replay what those refreshers stored. A cheap
// collector can therefore be kept fresh without an expensive one delaying, or
// timing out, a Prometheus scrape.
type ExtendedCephMetricsCollector struct {
	ctx        context.Context
	ctxTimeout time.Duration
	logger     *zap.Logger
	clients    map[string]*collector.Client
	collectors map[string]collector.Collector

	// Refresh related
	defaultInterval time.Duration
	intervals       map[string]time.Duration

	// cache and lastRefresh are both keyed by collector name.
	cacheMutex  sync.Mutex
	cache       map[string][]prometheus.Metric
	lastRefresh map[string]time.Time
}

func NewExtendedCephMetricsCollector(ctx context.Context, logger *zap.Logger, clients map[string]*collector.Client, collectors map[string]collector.Collector, ctxTimeout time.Duration, defaultInterval time.Duration, intervals map[string]time.Duration) *ExtendedCephMetricsCollector {
	return &ExtendedCephMetricsCollector{
		ctx:             ctx,
		ctxTimeout:      ctxTimeout,
		logger:          logger,
		clients:         clients,
		collectors:      collectors,
		defaultInterval: defaultInterval,
		intervals:       intervals,
		cache:           make(map[string][]prometheus.Metric, len(collectors)),
		lastRefresh:     make(map[string]time.Time, len(collectors)),
	}
}

// Describe implements the prometheus.Collector interface.
func (n *ExtendedCephMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- scrapeDurationDesc
	ch <- scrapeSuccessDesc
	ch <- lastRefreshDesc
}

// Collect implements the prometheus.Collector interface.
//
// It never collects anything itself; it only replays what the background
// refreshers produced, which is what keeps a scrape fast regardless of how
// expensive the underlying collection is.
func (n *ExtendedCephMetricsCollector) Collect(outgoingCh chan<- prometheus.Metric) {
	for _, metric := range n.snapshot() {
		outgoingCh <- metric
	}
}

// snapshot copies the cached metrics of every collector and adds a refresh
// timestamp for each.
//
// The copy is taken under the lock so that sending on the outgoing channel,
// which is paced by whoever is scraping, cannot stall the refreshers.
func (n *ExtendedCephMetricsCollector) snapshot() []prometheus.Metric {
	n.cacheMutex.Lock()
	defer n.cacheMutex.Unlock()

	metrics := make([]prometheus.Metric, 0, len(n.collectors))
	for name := range n.collectors {
		metrics = append(metrics, n.cache[name]...)

		// Always emitted, so a collector that has never completed a cycle shows
		// up as 0 and stays alertable rather than being absent entirely.
		var timestamp float64
		if refreshed, ok := n.lastRefresh[name]; ok {
			timestamp = float64(refreshed.Unix())
		}

		metrics = append(metrics, prometheus.MustNewConstMetric(lastRefreshDesc, prometheus.GaugeValue, timestamp, name))
	}

	return metrics
}

// StartRefreshers launches one background refresher per enabled collector.
func (n *ExtendedCephMetricsCollector) StartRefreshers(ctx context.Context) {
	for name := range n.collectors {
		interval := n.intervalFor(name)
		n.logger.Info("starting collector refresher", zap.String("collector", name), zap.Duration("interval", interval))

		go n.refreshLoop(ctx, name, interval)
	}
}

// intervalFor resolves a collector's refresh interval, preferring its explicit
// override over the shared default.
func (n *ExtendedCephMetricsCollector) intervalFor(name string) time.Duration {
	interval := n.defaultInterval
	if override, ok := n.intervals[name]; ok && override > 0 {
		interval = override
	}

	if interval < minRefreshInterval {
		interval = minRefreshInterval
	}

	return interval
}

// refreshLoop primes the cache once and then refreshes at a fixed rate, so that
// the cycle to cycle period does not drift by however long a collection took.
func (n *ExtendedCephMetricsCollector) refreshLoop(ctx context.Context, name string, interval time.Duration) {
	n.refresh(name, interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.refresh(name, interval)
		}
	}
}

// refresh runs one collection cycle for a collector, but stops waiting for it
// once bound has elapsed.
//
// A librados call can block indefinitely and Go cannot interrupt a blocked cgo
// call, so an abandoned cycle keeps running and leaks its goroutine. Abandoning
// it regardless is what keeps the loop alive and the refresh timestamp moving,
// which is what makes a wedged collector visible instead of letting the exporter
// serve stale metrics forever without ever reporting an error.
func (n *ExtendedCephMetricsCollector) refresh(name string, bound time.Duration) {
	begin := time.Now()
	done := make(chan struct{})

	go func() {
		defer close(done)

		metrics := n.runCollector(name)

		n.cacheMutex.Lock()
		defer n.cacheMutex.Unlock()
		n.cache[name] = metrics
		n.lastRefresh[name] = time.Now()
	}()

	select {
	case <-done:
		n.logger.Debug("refresh cycle complete", zap.String("collector", name), zap.Float64("took", time.Since(begin).Seconds()))
	case <-time.After(bound):
		n.logger.Error("refresh cycle exceeded its interval and was abandoned, it may be blocked in librados and its goroutine will leak until the exporter is restarted",
			zap.String("collector", name), zap.Duration("interval", bound))
	}
}

// runCollector runs a single collector against every client and returns the
// metrics it produced.
func (n *ExtendedCephMetricsCollector) runCollector(name string) []prometheus.Metric {
	coll := n.collectors[name]
	metricsCh := make(chan prometheus.Metric)

	collected := []prometheus.Metric{}

	// Wait to ensure metricsCh is fully drained before the collected metrics
	// are handed back
	drained := make(chan struct{})
	go func() {
		defer close(drained)

		for metric := range metricsCh {
			collected = append(collected, metric)
		}
	}()

	wgCollection := sync.WaitGroup{}

	for clientName, client := range n.clients {
		wgCollection.Add(1)
		go func(clientName string, client *collector.Client) {
			defer wgCollection.Done()

			begin := time.Now()
			ctx, cancel := context.WithTimeout(n.ctx, n.ctxTimeout)
			defer cancel()

			err := coll.Update(ctx, client, metricsCh)
			duration := time.Since(begin)
			var success float64

			if err != nil {
				n.logger.Error(fmt.Sprintf("%s collector failed after %fs", name, duration.Seconds()), zap.Error(err))
				success = 0
			} else {
				n.logger.Debug(fmt.Sprintf("%s collector succeeded after %fs.", name, duration.Seconds()))
				success = 1
			}

			metricsCh <- prometheus.MustNewConstMetric(scrapeDurationDesc, prometheus.GaugeValue, duration.Seconds(), name, clientName)
			metricsCh <- prometheus.MustNewConstMetric(scrapeSuccessDesc, prometheus.GaugeValue, success, name, clientName)
		}(clientName, client)
	}

	wgCollection.Wait()
	close(metricsCh)
	<-drained

	return collected
}
