// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Package config handles configuration loading and validation for the agent.

package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds the agent configuration settings.
type Config struct {
	ServiceHost    string `json:"serviceHost"`    // Server address (host:port)
	Secure         bool   `json:"secure"`         // Use HTTPS/WSS if true
	ApiKey         string `json:"apiKey"`         // API key for authentication
	ServerToken    string `json:"serverToken"`    // Server token for authentication
	Verbose        bool   `json:"verbose"`        // Enable verbose logging
	MetricInterval int    `json:"metricInterval"` // Metric collection interval in ms (default 1000)
	BatchInterval  int    `json:"batchInterval"`  // Batch send interval in ms (default 10000)
	StoreInterval  int    `json:"storeInterval"`  // Stored sample interval in ms; samples are averaged (default 10000)
	HealthPort     int    `json:"healthPort"`     // Port for health endpoint (0 = disabled)
}

const (
	defaultMetricIntervalMs = 1000
	defaultBatchIntervalMs  = 10000
	defaultStoreIntervalMs  = 10000
	minMetricIntervalMs     = 100
	maxMetricIntervalMs     = 60000
	minBatchIntervalMs      = 1000
	maxBatchIntervalMs      = 300000

	localConfigPath  = "./simplemon.conf"
	globalConfigPath = "/etc/simplemon.conf"
)

// maskSecret masks a secret value for safe logging.
// Shows only the last 4 characters.
func maskSecret(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}

// LoadConfig reads configuration from a file and command-line flags.
// It searches for config files in this order:
// 1. Path specified via -config flag
// 2. ./simplemon.conf (local directory)
// 3. /etc/simplemon.conf (global)
//
// Command-line flags override values from the config file.
// Returns an error if no config file is found or parsing fails.
func LoadConfig() (*Config, error) {
	configFilePath := flag.String("config", "", "Path to the config file")
	apiKey := flag.String("apiKey", "", "API Key")
	serviceHost := flag.String("serviceHost", "", "Service Host")
	secure := flag.Bool("secure", false, "Use secure connection")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	flag.Parse()

	var config Config
	var configPath string
	var configFile []byte
	var err error

	// Determine which config file to use
	if *configFilePath != "" {
		configPath = *configFilePath
		configFile, err = os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
		}
	} else {
		// Try local config file first
		localPath := filepath.Join(".", "simplemon.conf")
		configFile, err = os.ReadFile(localPath)
		if err == nil {
			configPath = localPath
		} else {
			// Try global config file
			configFile, err = os.ReadFile(globalConfigPath)
			if err != nil {
				return nil, fmt.Errorf("no config file found (tried %s and %s)", localPath, globalConfigPath)
			}
			configPath = globalConfigPath
		}
	}

	fmt.Printf("[config] Using config file: %s\n", configPath)

	// Parse JSON config
	if err := json.Unmarshal(configFile, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}

	// Override config with command line arguments if provided
	if *apiKey != "" {
		config.ApiKey = *apiKey
	}
	if *serviceHost != "" {
		config.ServiceHost = *serviceHost
	}
	if *secure {
		config.Secure = true
	}
	if *verbose {
		config.Verbose = true
	}

	// Set defaults for intervals
	if config.MetricInterval <= 0 {
		config.MetricInterval = defaultMetricIntervalMs
	}
	if config.BatchInterval <= 0 {
		config.BatchInterval = defaultBatchIntervalMs
	}
	if config.StoreInterval <= 0 {
		config.StoreInterval = defaultStoreIntervalMs
	}

	// Log configuration (with masked secrets)
	fmt.Printf("[config] Service Host: %s\n", config.ServiceHost)
	fmt.Printf("[config] Secure: %t\n", config.Secure)
	fmt.Printf("[config] Api Key: %s\n", maskSecret(config.ApiKey))
	fmt.Printf("[config] Server Token: %s\n", maskSecret(config.ServerToken))
	fmt.Printf("[config] Metric Interval: %dms\n", config.MetricInterval)
	fmt.Printf("[config] Batch Interval: %dms\n", config.BatchInterval)
	fmt.Printf("[config] Store Interval: %dms\n", config.StoreInterval)
	if config.HealthPort > 0 {
		fmt.Printf("[config] Health Port: %d\n", config.HealthPort)
	}

	return &config, nil
}

// Validate checks that the configuration has all required fields
// and that values are within acceptable ranges.
func (c *Config) Validate() error {
	if c.ServiceHost == "" {
		return fmt.Errorf("serviceHost is required")
	}
	if c.ApiKey == "" {
		return fmt.Errorf("apiKey is required")
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
	if c.StoreInterval != 0 && (c.StoreInterval < c.MetricInterval || c.StoreInterval > maxBatchIntervalMs) {
		return fmt.Errorf("storeInterval must be between metricInterval and %d ms", maxBatchIntervalMs)
	}
	return nil
}
