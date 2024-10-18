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
	"strings"
	"time"

	"github.com/ceph/go-ceph/rados"
	"github.com/ceph/go-ceph/rgw/admin"
	"github.com/galexrt/extended-ceph-exporter/collector"
	"github.com/galexrt/extended-ceph-exporter/pkg/config"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
	flag "github.com/spf13/pflag"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
)

var (
	flags                    = flag.NewFlagSet("exporter", flag.ExitOnError)
	defaultEnabledCollectors = []string{"rgw_user_quota", "rgw_buckets"}
)

type CmdLineOpts struct {
	Version  bool
	LogLevel string

	CollectorsEnabled []string

	MultiRealm       bool
	MultiRealmConfig string

	RGWHost      string
	RGWAccessKey string
	RGWSecretKey string

	SkipTLSVerify bool

	ListenHost  string
	MetricsPath string

	CtxTimeout  time.Duration
	HttpTimeout time.Duration

	CachingEnabled bool
	CacheDuration  time.Duration
}

var opts CmdLineOpts

var requiredFlags = []string{"rgw-host", "rgw-access-key", "rgw-secret-key"}

func init() {
	flags.BoolVar(&opts.Version, "version", false, "Show version info and exit")
	flags.StringVar(&opts.LogLevel, "log-level", "INFO", "Set log level")

	flags.StringSliceVar(&opts.CollectorsEnabled, "collectors-enabled", defaultEnabledCollectors, "List of enabled collectors (please refer to the readme for a list of all available collectors)")

	flags.BoolVar(&opts.MultiRealm, "multi-realm", false, "Enable multi realm mode (requires realms.yaml config, see --multi-realm-config flag)")
	flags.StringVar(&opts.MultiRealmConfig, "multi-realm-config", "realms.yaml", "Path to your realms.yaml config file")

	flags.StringVar(&opts.RGWHost, "rgw-host", "", "RGW Host URL")
	flags.StringVar(&opts.RGWAccessKey, "rgw-access-key", "", "RGW Access Key")
	flags.StringVar(&opts.RGWSecretKey, "rgw-secret-key", "", "RGW Secret Key")

	flags.BoolVar(&opts.SkipTLSVerify, "skip-tls-verify", false, "Skip TLS cert verification")

	flags.StringVar(&opts.ListenHost, "listen-host", ":9138", "Exporter listen host")
	flags.StringVar(&opts.MetricsPath, "metrics-path", "/metrics", "Set the metrics endpoint path")

	flags.DurationVar(&opts.CtxTimeout, "context-timeout", 60*time.Second, "Context timeout for collecting metrics per collector")
	flags.DurationVar(&opts.HttpTimeout, "http-timeout", 55*time.Second, "HTTP request timeout for collecting metrics for RGW API HTTP client")

	flags.BoolVar(&opts.CachingEnabled, "cache-enabled", false, "Enable metrics caching to reduce load")
	flags.DurationVar(&opts.CacheDuration, "cache-duration", 20*time.Second, "Cache duration in seconds")
}

func flagNameFromEnvName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

func parseFlagsAndEnvVars() error {
	godotenv.Load(".env")

	for _, v := range os.Environ() {
		vals := strings.SplitN(v, "=", 2)

		if !strings.HasPrefix(vals[0], "CEPH_METRICS_") {
			continue
		}
		flagName := flagNameFromEnvName(strings.ReplaceAll(vals[0], "CEPH_METRICS_", ""))

		fn := flags.Lookup(flagName)
		if fn == nil || fn.Changed {
			continue
		}

		if err := fn.Value.Set(vals[1]); err != nil {
			return err
		}
		fn.Changed = true
	}

	return flags.Parse(os.Args[1:])
}

func main() {
	if err := parseFlagsAndEnvVars(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if opts.Version {
		fmt.Fprintln(os.Stdout, version.Print(os.Args[0]))
		os.Exit(0)
	}

	loggerConfig := zap.NewProductionConfig()
	level, err := zapcore.ParseLevel(opts.LogLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("unable to parse log level. %w", err))
		os.Exit(1)
	}
	loggerConfig.Level.SetLevel(level)

	logger, err := loggerConfig.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("failed to set up logger. %w", err))
		os.Exit(1)
	}

	var realms []*config.Realm
	if !opts.MultiRealm {
		// Check if required flags are given
		for _, f := range requiredFlags {
			flag := flags.Lookup(f)
			if flag == nil {
				logger.Fatal(fmt.Sprintf("required flag %s not found during lookup in flags list, please report this to the developer", f))
			}
			if !flag.Changed {
				logger.Fatal(fmt.Sprintf("required flag %s not set", flag.Name))
			}
		}

		realms = append(realms, &config.Realm{
			Name:          "default",
			Host:          opts.RGWHost,
			AccessKey:     opts.RGWAccessKey,
			SecretKey:     opts.RGWSecretKey,
			SkipTLSVerify: opts.SkipTLSVerify,
		})
	} else {
		realmsCfg := &config.RGW{}

		yamlFile, err := os.ReadFile(opts.MultiRealmConfig)
		if err != nil {
			logger.Fatal("failed to load realms config file", zap.Error(err))
		}
		if err := yaml.Unmarshal(yamlFile, realmsCfg); err != nil {
			logger.Fatal("failed to unmarshal realms config file", zap.Error(err))
		}
		realms = append(realms, realmsCfg.Realms...)
	}

	var radosConn *rados.Conn
	if slices.Contains(opts.CollectorsEnabled, "") {
		radosConn, err := rados.NewConn()
		if err != nil {
			logger.Fatal("failed to create new rados connection", zap.Error(err))
		}

		if err := radosConn.ReadDefaultConfigFile(); err != nil {
			logger.Fatal("failed to read default ceph/rados config file", zap.Error(err))
		}

		if err := radosConn.Connect(); err != nil {
			logger.Fatal("failed to create rados connection", zap.Error(err))
		}
	}

	clients := map[string]*collector.Client{}
	for _, realm := range realms {
		rgwAdminAPI, err := CreateRGWAPIConnection(realm)
		if err != nil {
			logger.Fatal(fmt.Sprintf("failed to create rgw api connection for %s realm", realm.Name), zap.Error(err))
		}

		clients[realm.Name] = &collector.Client{
			Name:        realm.Name,
			RGWAdminAPI: rgwAdminAPI,
			Rados:       radosConn,
		}
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = prometheus.Register(NewExtendedCephMetricsCollector(ctx, logger, clients, collectors, opts.CtxTimeout, opts.CachingEnabled, opts.CacheDuration)); err != nil {
		logger.Fatal("couldn't register collector", zap.Error(err))
	}

	logger.Info(fmt.Sprintf("listening on %s", opts.ListenHost))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<!DOCTYPE html>
<html>
	<head><title>Extended Ceph Exporter</title></head>
	<body>
		<h1>Extended Ceph Exporter</h1>
		<p><a href="` + opts.MetricsPath + `">Metrics</a></p>
	</body>
</html>`))
	})

	handler := promhttp.HandlerFor(prometheus.DefaultGatherer,
		promhttp.HandlerOpts{
			ErrorLog:      zap.NewStdLog(logger),
			ErrorHandling: promhttp.ContinueOnError,
		})

	http.HandleFunc(opts.MetricsPath, handler.ServeHTTP)

	http.ListenAndServe(opts.ListenHost, nil)
}

func CreateRGWAPIConnection(realm *config.Realm) (*admin.API, error) {
	httpClient := &http.Client{
		Transport: http.DefaultTransport.(*http.Transport).Clone(),
		Timeout:   opts.HttpTimeout,
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
