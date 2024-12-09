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

var (
	flags                    = flag.NewFlagSet("exporter", flag.ExitOnError)
	defaultEnabledCollectors = []string{"rgw_user_quota", "rgw_buckets"}
)

type CmdLineOpts struct {
	Version bool

	ConfigFile string
	RealmsFile string

	CollectorsEnabled []string
}

var opts CmdLineOpts

func init() {
	flags.BoolVar(&opts.Version, "version", false, "Show version info and exit")

	flags.StringVar(&opts.ConfigFile, "config", "", "Config file path (default name `config.yaml` , current and `/config` directory).")
	flags.StringVar(&opts.RealmsFile, "realms-config", "", "Config file path (default name `realms.yaml` , current and `/realms` directory; old flag name: `--multi-realm-config`).")

	flags.StringSliceVar(&opts.CollectorsEnabled, "collectors-enabled", defaultEnabledCollectors, "List of enabled collectors (please refer to the readme for a list of all available collectors)")
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
		radosConn, err := rados.NewConn()
		if err != nil {
			logger.Fatal("failed to create new rados connection", zap.Error(err))
		}

		if cfg.RBD.CephConfig != "" {
			if err := radosConn.ReadConfigFile(cfg.RBD.CephConfig); err != nil {
				logger.Fatal("failed to read custom ceph/rados config file", zap.String("path", cfg.RBD.CephConfig), zap.Error(err))
			}
		} else {
			if err := radosConn.ReadDefaultConfigFile(); err != nil {
				logger.Fatal("failed to read default ceph/rados config file", zap.Error(err))
			}
		}

		if err := radosConn.Connect(); err != nil {
			logger.Fatal("failed to create rados connection", zap.Error(err))
		}
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
	if err = prometheus.Register(NewExtendedCephMetricsCollector(ctx, logger, clients, collectors,
		cfg.Timeouts.Collector, cfg.Cache.Enabled, cfg.Cache.Duration)); err != nil {
		logger.Fatal("couldn't register collectors", zap.Error(err))
	}

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
