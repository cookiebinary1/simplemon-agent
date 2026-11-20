// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Package httpservices provides HTTP client for sending metrics to the server.

package httpservices

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Client struct {
	BaseURL     string
	APIKey      string
	ServerToken string
	Hostname    string
	Version     string
	HTTPClient  *http.Client
}

// Buffer pool for JSON encoding
var bufferPool = sync.Pool{
	New: func() interface{} {
		return new(bytes.Buffer)
	},
}

// sanitizeHeaderValue removes all control characters and potentially dangerous
// sequences from HTTP header values to prevent CRLF injection attacks.
// This includes CR, LF, null bytes, and other ASCII control characters.
func sanitizeHeaderValue(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for _, r := range value {
		// Only allow printable ASCII characters and safe UTF-8
		// Reject control characters (0x00-0x1F, 0x7F) which includes CR (0x0D) and LF (0x0A)
		if r >= 0x20 && r != 0x7F {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// RejectedError means the server refused the payload itself; retrying it cannot succeed.
type RejectedError struct {
	Status int
	Body   string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("server rejected batch: status %d, body: %s", e.Status, e.Body)
}

// UpgradeRequiredError means the server considers this agent version
// incompatible and refuses all batches until the agent is upgraded.
type UpgradeRequiredError struct {
	Body string
}

func (e *UpgradeRequiredError) Error() string {
	return fmt.Sprintf("server requires an agent upgrade: %s", e.Body)
}

func NewClient(baseURL, apiKey, serverToken, hostname, version string) *Client {
	return &Client{
		BaseURL:     baseURL,
		APIKey:      apiKey,
		ServerToken: serverToken,
		Hostname:    sanitizeHeaderValue(hostname),
		Version:     sanitizeHeaderValue(version),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12,
				},
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     90 * time.Second,
				DisableCompression:  false,
			},
		},
	}
}

func (c *Client) SendMetrics(metrics interface{}) error {
	// Get buffer from pool
	buf := bufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufferPool.Put(buf)

	// Gzip compress the JSON data
	gzWriter := gzip.NewWriter(buf)
	encoder := json.NewEncoder(gzWriter)
	if err := encoder.Encode(metrics); err != nil {
		gzWriter.Close()
		return fmt.Errorf("error encoding metrics: %w", err)
	}
	if err := gzWriter.Close(); err != nil {
		return fmt.Errorf("error closing gzip writer: %w", err)
	}

	req, err := http.NewRequest("POST", c.BaseURL, buf)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.APIKey))
	req.Header.Set("Server-Token", c.ServerToken)
	if c.Hostname != "" {
		req.Header.Set("X-Hostname", c.Hostname)
	}
	req.Header.Set("Api-Version", "1")
	if c.Version != "" {
		req.Header.Set("X-Agent-Version", c.Version)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusUpgradeRequired {
			return &UpgradeRequiredError{Body: string(body)}
		}
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusRequestEntityTooLarge {
			return &RejectedError{Status: resp.StatusCode, Body: string(body)}
		}
		return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	// Drain the body so the keep-alive connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	return nil
}
