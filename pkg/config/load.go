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

import (
	"fmt"
	"os"
	"reflect"

	"github.com/creasty/defaults"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

func StringExpandEnv() mapstructure.DecodeHookFuncKind {
	return func(
		f reflect.Kind,
		t reflect.Kind,
		data interface{},
	) (interface{}, error) {
		if f != reflect.String || t != reflect.String {
			return data, nil
		}

		return os.ExpandEnv(data.(string)), nil
	}
}

func Load(configPath string, realmsPath string) (*Config, *RGW, error) {
	c, err := loadConfig(configPath)
	if err != nil {
		return nil, nil, err
	}

	r, err := loadRealms(realmsPath)
	if err != nil {
		return nil, nil, err
	}

	return c, r, nil
}

func loadConfig(path string) (*Config, error) {
	v := viper.New()
	// Viper reading setup
	v.SetConfigType("yaml")
	v.SetConfigName("config")
	v.AddConfigPath(".")
	v.AddConfigPath("/config")

	if path != "" {
		v.SetConfigFile(path)
	}

	// Find and read the config file
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	c := &Config{}
	if err := defaults.Set(c); err != nil {
		return nil, fmt.Errorf("failed to set config defaults: %w", err)
	}

	if err := v.Unmarshal(c, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
		StringExpandEnv(),
	))); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return c, nil
}

func loadRealms(path string) (*RGW, error) {
	v := viper.New()
	// Viper reading setup
	v.SetConfigType("yaml")
	v.SetConfigName("realms")
	v.AddConfigPath(".")
	v.AddConfigPath("/realms")

	if path != "" {
		v.SetConfigFile(path)
	}

	// Find and read the config file
	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	r := &RGW{}
	if err := defaults.Set(r); err != nil {
		return nil, fmt.Errorf("failed to set realms config defaults: %w", err)
	}

	if err := v.Unmarshal(r, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
		StringExpandEnv(),
	))); err != nil {
		return nil, fmt.Errorf("failed to unmarshal realms config: %w", err)
	}

	return r, nil
}

func LoadTestConfig() (*Config, *RGW, error) {
	c := &Config{}
	if err := defaults.Set(c); err != nil {
		return nil, nil, fmt.Errorf("failed to set config defaults: %w", err)
	}

	return c, &RGW{}, nil
}
