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

	Timeouts Timeouts `yaml:"timeouts"`

	Cache Cache `yaml:"Cache"`

	RBD RBD `yaml:"rbd"`
}

type Timeouts struct {
	Collector time.Duration `yaml:"collector" default:"60s"`
	HTTP      time.Duration `yaml:"http" default:"55s"`
}

type Cache struct {
	Enabled  bool          `yaml:"enabled"`
	Duration time.Duration `yaml:"duration" default:"20s"`
}

type RBD struct {
	CephConfig string     `yaml:"cephConfig"`
	Pools      []*RBDPool `yaml:"pools"`
}

type RBDPool struct {
	Name       string
	Namespaces []string `yaml:"namespaces"`
}
