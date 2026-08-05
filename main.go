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
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ceph/go-ceph/rados"
	"github.com/ceph/go-ceph/rgw/admin"
	"github.com/galexrt/extended-ceph-exporter/collector"
	"github.com/galexrt/extended-ceph-exporter/pkg/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	flag "github.com/spf13/pflag"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// defaultClientName names the client used when no RGW realm is configured. RBD
// collectors are cluster scoped and need no realm, so an RBD only deployment
// still gets exactly one client to collect against.
const defaultClientName = "default"

// radosOpTimeoutOptions are the librados options bounding how long a single
// operation may wait for the cluster.
var radosOpTimeoutOptions = []string{"rados_osd_op_timeout", "rados_mon_op_timeout"}

var (
	flags                    = flag.NewFlagSet("exporter", flag.ExitOnError)
	defaultEnabledCollectors = []string{"rbd_images", "rbd_image_usage"}
)

type CmdLineOpts struct {
	Version bool

	ConfigFile string
	RealmsFile string

	CollectorsEnabled []string

	ListenAddress    string
	CollectorTimeout time.Duration
	RefreshInterval  time.Duration
	RefreshIntervals map[string]string
}

var opts CmdLineOpts

func init() {
	flags.BoolVar(&opts.Version, "version", false, "Show version info and exit")

	flags.StringVar(&opts.ConfigFile, "config", "", "Config file path (default name `config.yaml` , current and `/config` directory).")
	flags.StringVar(&opts.RealmsFile, "realms-config", "", "Config file path (default name `realms.yaml` , current and `/realms` directory; old flag name: `--multi-realm-config`).")

	flags.StringSliceVar(&opts.CollectorsEnabled, "collectors-enabled", defaultEnabledCollectors, "List of enabled collectors (please refer to the readme for a list of all available collectors)")

	flags.StringVar(&opts.ListenAddress, "web.listen-address", "", "Address to listen on for the metrics endpoint (overrides `listenHost` from the config file).")
	flags.DurationVar(&opts.CollectorTimeout, "collector-timeout", 0, "Context timeout per collector (overrides `timeouts.collector` from the config file).")
	flags.DurationVar(&opts.RefreshInterval, "refresh-interval", 0, "Default background refresh interval for all collectors (overrides `refresh.interval` from the config file).")
	flags.StringToStringVar(&opts.RefreshIntervals, "refresh-intervals", nil, "Per collector background refresh intervals, e.g. `rbd_images=60s,rbd_image_usage=4m` (overrides `refresh.intervals` from the config file).")
}

func aliasNormalizeFunc(f *flag.FlagSet, name string) flag.NormalizedName {
	switch name {
	case "multi-realm-config":
		name = "realms-config"
	}
	return flag.NormalizedName(name)
}

func main() {
	flags.SetNormalizeFunc(aliasNormalizeFunc)
	if err := flags.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if opts.Version {
		fmt.Fprintln(os.Stdout, version.Print(os.Args[0]))
		os.Exit(0)
	}

	cfg, realmsCfg, err := config.Load(opts.ConfigFile, opts.RealmsFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("failed to load config file. %w", err))
		os.Exit(1)
	}

	applyFlagOverrides(cfg)

	refreshIntervals, err := resolveRefreshIntervals(cfg.Refresh.Intervals, opts.RefreshIntervals)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	level, err := zapcore.ParseLevel(cfg.LogLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("unable to parse log level. %w", err))
		os.Exit(1)
	}

	loggerConfig := zap.NewProductionConfig()
	loggerConfig.Level.SetLevel(level)

	logger, err := loggerConfig.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("failed to set up logger. %w", err))
		os.Exit(1)
	}

	var radosConn *rados.Conn
	if slices.ContainsFunc(opts.CollectorsEnabled, func(c string) bool {
		return strings.HasPrefix(c, "rbd_")
	}) {
		conn, err := rados.NewConn()
		if err != nil {
			logger.Fatal("failed to create new rados connection", zap.Error(err))
		}

		if cfg.RBD.CephConfig != "" {
			if err := conn.ReadConfigFile(cfg.RBD.CephConfig); err != nil {
				logger.Fatal("failed to read custom ceph/rados config file", zap.String("path", cfg.RBD.CephConfig), zap.Error(err))
			}
		} else {
			if err := conn.ReadDefaultConfigFile(); err != nil {
				logger.Fatal("failed to read default ceph/rados config file", zap.Error(err))
			}
		}

		if err := setRadosOpTimeouts(conn, cfg.RBD.OpTimeout, logger); err != nil {
			logger.Fatal("failed to set librados operation timeouts", zap.Error(err))
		}

		if err := conn.Connect(); err != nil {
			logger.Fatal("failed to create rados connection", zap.Error(err))
		}

		// Only assigned once the connection is usable, so that collectors can
		// treat a non nil connection as a connected one.
		radosConn = conn
	}

	clients := map[string]*collector.Client{}
	for _, realm := range realmsCfg.Realms {
		rgwAdminAPI, err := CreateRGWAPIConnection(cfg, realm)
		if err != nil {
			logger.Fatal(fmt.Sprintf("failed to create rgw api connection for %s realm", realm.Name), zap.Error(err))
		}

		clients[realm.Name] = &collector.Client{
			Name:        realm.Name,
			Config:      cfg,
			RGWAdminAPI: rgwAdminAPI,
			Rados:       radosConn,
		}
	}

	// Without this an RBD only deployment has no clients at all, and every
	// collector would silently be skipped because collection iterates clients.
	if len(clients) == 0 {
		clients[defaultClientName] = &collector.Client{
			Name:   defaultClientName,
			Config: cfg,
			Rados:  radosConn,
		}
	}

	if radosConn != nil && len(clients) > 1 {
		logger.Warn("RBD metrics are cluster scoped, but multiple RGW realms are configured, so every RBD series will be duplicated per realm",
			zap.Int("clients", len(clients)))
	}

	collectors, err := loadCollectors(opts.CollectorsEnabled)
	if err != nil {
		logger.Fatal("couldn't load collectors", zap.Error(err))
	}

	cs := make([]string, 0, len(collectors))
	for k := range collectors {
		cs = append(cs, k)
	}
	logger.Info("enabled collectors", zap.Strings("collectors", cs))

	for name := range refreshIntervals {
		if _, ok := collectors[name]; !ok {
			logger.Warn("refresh interval configured for a collector that is not enabled", zap.String("collector", name))
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	metricsCollector := NewExtendedCephMetricsCollector(ctx, logger, clients, collectors,
		cfg.Timeouts.Collector, cfg.Refresh.Interval, refreshIntervals)
	if err = prometheus.Register(metricsCollector); err != nil {
		logger.Fatal("couldn't register collectors", zap.Error(err))
	}

	metricsCollector.StartRefreshers(ctx)

	logger.Info(fmt.Sprintf("listening on %s", cfg.ListenHost))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<!DOCTYPE html>
<html>
	<head><title>Extended Ceph Exporter</title></head>
	<body>
		<h1>Extended Ceph Exporter</h1>
		<p><a href="` + cfg.MetricsPath + `">Metrics</a></p>
	</body>
</html>`))
	})

	handler := promhttp.HandlerFor(prometheus.DefaultGatherer,
		promhttp.HandlerOpts{
			ErrorLog:      zap.NewStdLog(logger),
			ErrorHandling: promhttp.ContinueOnError,
		})

	http.HandleFunc(cfg.MetricsPath, handler.ServeHTTP)

	http.ListenAndServe(cfg.ListenHost, nil)
}

// applyFlagOverrides folds the command line into the loaded config.
//
// A flag only wins when it was explicitly passed, so the config file stays
// authoritative for everything left at its default.
func applyFlagOverrides(cfg *config.Config) {
	if cfg.Collectors != nil && !flags.Changed("collectors-enabled") {
		opts.CollectorsEnabled = *cfg.Collectors
	}

	if flags.Changed("web.listen-address") {
		cfg.ListenHost = opts.ListenAddress
	}

	if flags.Changed("collector-timeout") {
		cfg.Timeouts.Collector = opts.CollectorTimeout
	}

	if flags.Changed("refresh-interval") {
		cfg.Refresh.Interval = opts.RefreshInterval
	}
}

// resolveRefreshIntervals merges the per collector refresh intervals from the
// config file with any given on the command line, where the command line wins.
func resolveRefreshIntervals(fromConfig map[string]time.Duration, fromFlags map[string]string) (map[string]time.Duration, error) {
	intervals := make(map[string]time.Duration, len(fromConfig)+len(fromFlags))
	for name, interval := range fromConfig {
		intervals[name] = interval
	}

	for name, raw := range fromFlags {
		interval, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to parse refresh interval %q for the %s collector. %w", raw, name, err)
		}

		intervals[name] = interval
	}

	return intervals, nil
}

// setRadosOpTimeouts fills in the librados operation timeouts while they are
// still unlimited.
//
// librados defaults both options to 0, meaning "wait forever". go-ceph calls are
// blocking cgo calls that Go cannot interrupt, so an operation the cluster never
// answers would park a collector goroutine permanently and the exporter would
// keep serving stale metrics without ever reporting a failure. Values already
// set, whether in ceph.conf or elsewhere, are left untouched.
func setRadosOpTimeouts(conn *rados.Conn, timeout time.Duration, logger *zap.Logger) error {
	if timeout <= 0 {
		logger.Warn("librados operation timeouts are disabled, a request the cluster never answers will block a collector until the exporter is restarted")

		return nil
	}

	seconds := strconv.FormatFloat(timeout.Seconds(), 'f', -1, 64)

	for _, option := range radosOpTimeoutOptions {
		value, err := conn.GetConfigOption(option)
		if err != nil {
			return fmt.Errorf("failed to read %s. %w", option, err)
		}

		if current, perr := strconv.ParseFloat(value, 64); perr == nil && current > 0 {
			logger.Debug("librados operation timeout already configured, leaving it alone",
				zap.String("option", option), zap.String("value", value))

			continue
		}

		if err := conn.SetConfigOption(option, seconds); err != nil {
			return fmt.Errorf("failed to set %s to %s. %w", option, seconds, err)
		}

		logger.Info("set librados operation timeout", zap.String("option", option), zap.String("value", seconds))
	}

	return nil
}

func CreateRGWAPIConnection(cfg *config.Config, realm *config.Realm) (*admin.API, error) {
	httpClient := &http.Client{
		Transport: http.DefaultTransport.(*http.Transport).Clone(),
		Timeout:   cfg.Timeouts.HTTP,
	}
	if realm.SkipTLSVerify {
		httpClient.Transport.(*http.Transport).TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	// Generate a connection object
	co, err := admin.New(realm.Host, realm.AccessKey, realm.SecretKey, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create RGW API connection for %s realm. %w", realm.Name, err)
	}

	return co, nil
}

func loadCollectors(list []string) (map[string]collector.Collector, error) {
	collectors := map[string]collector.Collector{}

	for _, name := range list {
		fn, ok := collector.Factories[name]
		if !ok {
			return nil, fmt.Errorf("collector '%s' not available", name)
		}
		c, err := fn()
		if err != nil {
			return nil, err
		}
		collectors[name] = c
	}

	return collectors, nil
}
