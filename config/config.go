// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Package config loads and validates the agent configuration file.

package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config is the JSON configuration file of the agent.
type Config struct {
	ServiceHost    string `json:"serviceHost"`    // Backend host with optional port, no scheme
	Secure         bool   `json:"secure"`         // Use HTTPS and WSS
	ApiKey         string `json:"apiKey"`         // Legacy account API key, optional; serverToken authenticates the agent
	ServerToken    string `json:"serverToken"`    // Per-server token issued by the panel
	Verbose        bool   `json:"verbose"`        // Debug logging
	MetricInterval int    `json:"metricInterval"` // Collection interval in ms (default 1000)
	BatchInterval  int    `json:"batchInterval"`  // Upload interval in ms (default 10000)
	StoreInterval  int    `json:"storeInterval"`  // Stored sample interval in ms; samples are averaged (default 10000)
	HealthPort     int    `json:"healthPort"`     // Port for /health and /ready (0 = disabled)
}

const (
	defaultMetricIntervalMs = 1000
	defaultBatchIntervalMs  = 10000
	defaultStoreIntervalMs  = 10000
	minMetricIntervalMs     = 100
	maxMetricIntervalMs     = 60000
	minBatchIntervalMs      = 1000
	maxBatchIntervalMs      = 300000
)

// SearchPaths are tried in order when no config path is given.
var SearchPaths = []string{"./simplemon.conf", "/etc/simplemon.conf"}

// Load reads the config from path, or from the first file in SearchPaths
// when path is empty. It fills in default intervals. It returns the path it
// used so the caller can log it.
func Load(path string) (*Config, string, error) {
	var data []byte
	var err error
	if path != "" {
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, path, fmt.Errorf("reading config: %w", err)
		}
	} else {
		for _, candidate := range SearchPaths {
			if data, err = os.ReadFile(candidate); err == nil {
				path = candidate
				break
			}
		}
		if path == "" {
			return nil, "", fmt.Errorf("no config file found (tried %v)", SearchPaths)
		}
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, path, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if cfg.MetricInterval <= 0 {
		cfg.MetricInterval = defaultMetricIntervalMs
	}
	if cfg.BatchInterval <= 0 {
		cfg.BatchInterval = defaultBatchIntervalMs
	}
	if cfg.StoreInterval <= 0 {
		cfg.StoreInterval = defaultStoreIntervalMs
	}
	return &cfg, path, nil
}

// Validate checks required fields and interval ranges.
func (c *Config) Validate() error {
	if c.ServiceHost == "" {
		return fmt.Errorf("serviceHost is required")
	}
	if c.ServerToken == "" {
		return fmt.Errorf("serverToken is required")
	}
	if c.MetricInterval < minMetricIntervalMs || c.MetricInterval > maxMetricIntervalMs {
		return fmt.Errorf("metricInterval must be between %d and %d ms", minMetricIntervalMs, maxMetricIntervalMs)
	}
	if c.BatchInterval < minBatchIntervalMs || c.BatchInterval > maxBatchIntervalMs {
		return fmt.Errorf("batchInterval must be between %d and %d ms", minBatchIntervalMs, maxBatchIntervalMs)
	}
	if c.BatchInterval < c.MetricInterval {
		return fmt.Errorf("batchInterval must be >= metricInterval")
	}
	if c.StoreInterval < c.MetricInterval || c.StoreInterval > maxBatchIntervalMs {
		return fmt.Errorf("storeInterval must be between metricInterval and %d ms", maxBatchIntervalMs)
	}
	return nil
}

// MaskSecret keeps only the last 4 characters of a secret for logging.
func MaskSecret(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}
