/*
Copyright 2024 Alexander Trost All rights reserved.

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

package config

import "time"

// Multi-Realm Config
type RGW struct {
	Realms []*Realm `yaml:"realms"`
}

type Realm struct {
	Name          string `yaml:"name"`
	Host          string `yaml:"host"`
	AccessKey     string `yaml:"accessKey"`
	SecretKey     string `yaml:"secretKey"`
	SkipTLSVerify bool   `yaml:"skipTLSVerify"`
}

type Config struct {
	LogLevel string `yaml:"logLevel" default:"INFO"`

	ListenHost  string `yaml:"listenHost" default:":9138"`
	MetricsPath string `yaml:"metricsPath" default:"/metrics"`

	SkipTLSVerify bool `yaml:"skipTLSVerify"`

	Collectors *[]string `yaml:"collectors,omitempty"`

	Timeouts Timeouts `yaml:"timeouts"`

	Refresh Refresh `yaml:"refresh"`

	RBD RBD `yaml:"rbd"`
}

type Timeouts struct {
	Collector time.Duration `yaml:"collector" default:"3m"`
	HTTP      time.Duration `yaml:"http" default:"55s"`
}

// Refresh configures the background refreshers. Every enabled collector is
// refreshed by its own goroutine, so an expensive collector never delays a
// scrape and a cheap one can be kept much fresher than an expensive one.
type Refresh struct {
	// Interval applies to every collector without an entry in Intervals.
	Interval time.Duration `yaml:"interval" default:"4m"`
	// Intervals overrides Interval for individual collectors, keyed by
	// collector name.
	Intervals map[string]time.Duration `yaml:"intervals"`
}

type RBD struct {
	CephConfig string     `yaml:"cephConfig"`
	Pools      []*RBDPool `yaml:"pools"`

	// OpTimeout bounds how long a single librados operation may wait for the
	// cluster. It is only applied when librados has no timeout configured yet,
	// so a value from ceph.conf always wins. Set it to 0 to leave librados at
	// its own default, which is to wait forever.
	OpTimeout time.Duration `yaml:"opTimeout" default:"30s"`
}

type RBDPool struct {
	Name       string
	Namespaces []string `yaml:"namespaces"`
}
