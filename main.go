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
	"github.com/sirupsen/logrus"
	flag "github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

var (
	flags                    = flag.NewFlagSet("exporter", flag.ExitOnError)
	defaultEnabledCollectors = "rgw_user_quota,rgw_buckets"
	log                      = logrus.New()
)

type CmdLineOpts struct {
	Version  bool
	LogLevel string

	CollectorsEnabled string
	MultiRealm        bool
	MultiRealmConfig  string

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

	flags.StringVar(&opts.CollectorsEnabled, "collectors-enabled", defaultEnabledCollectors, "List of enabled collectors")

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
		log.Fatal(err)
	}

	if opts.Version {
		fmt.Fprintln(os.Stdout, version.Print(os.Args[0]))
		os.Exit(0)
	}

	log.Out = os.Stdout

	// Set log level
	l, err := logrus.ParseLevel(opts.LogLevel)
	if err != nil {
		log.Fatal(err)
	}
	log.SetLevel(l)

	var realms []*config.Realm
	if !opts.MultiRealm {
		// Check if required flags are given
		for _, f := range requiredFlags {
			flag := flags.Lookup(f)
			if flag == nil {
				log.Fatalf("required flag %s not found during lookup in flags list, please report this to the developer", f)
			}
			if !flag.Changed {
				log.Fatalf("required flag %s not set", flag.Name)
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
		realmsCfg := &config.Realms{}

		yamlFile, err := os.ReadFile(opts.MultiRealmConfig)
		if err != nil {
			log.WithError(err).Printf("failed to load realms config file")
		}
		if err := yaml.Unmarshal(yamlFile, realmsCfg); err != nil {
			log.WithError(err).Fatalf("failed to unmarshal realms config file")
		}
		realms = append(realms, realmsCfg.Realms...)
	}

	radosConn, err := rados.NewConn()
	if err != nil {
		log.WithError(err).Fatalf("failed to create new rados connection")
	}

	if err := radosConn.ReadDefaultConfigFile(); err != nil {
		log.WithError(err).Fatalf("failed to read default ceph/rados config file")
	}

	if err := radosConn.Connect(); err != nil {
		log.WithError(err).Fatalf("failed to connect to rados")
	}

	clients := map[string]*collector.Client{}
	for _, realm := range realms {
		rgwAdminAPI, err := CreateRGWAPIConnection(realm)
		if err != nil {
			log.WithError(err).Fatalf("failed to create rgw api connection for %s realm", realm.Name)
		}

		clients[realm.Name] = &collector.Client{
			Name:        realm.Name,
			RGWAdminAPI: rgwAdminAPI,
			Rados:       radosConn,
		}
	}

	collectors, err := loadCollectors(opts.CollectorsEnabled)
	if err != nil {
		log.WithError(err).Fatalf("couldn't load collectors")
	}
	log.Infof("enabled collectors:")
	for n := range collectors {
		log.Infof(" - %s", n)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = prometheus.Register(NewExtendedCephMetricsCollector(ctx, log, clients, collectors, opts.CtxTimeout, opts.CachingEnabled, opts.CacheDuration)); err != nil {
		log.WithError(err).Fatalf("couldn't register collector")
	}

	log.Infof("listening on %s", opts.ListenHost)
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
			ErrorLog:      log,
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

func loadCollectors(list string) (map[string]collector.Collector, error) {
	collectors := map[string]collector.Collector{}
	for _, name := range strings.Split(list, ",") {
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
