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
	"time"
)

// Client uploads metric batches over HTTP.
type Client struct {
	BaseURL     string
	APIKey      string
	ServerToken string
	Hostname    string
	Version     string
	HTTPClient  *http.Client
}

// sanitizeHeaderValue drops control characters (including CR and LF), so a
// hostile hostname cannot inject extra headers.
func sanitizeHeaderValue(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, value)
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

// AuthError means the server rejected the API key or server token (HTTP 401).
// Retrying quickly cannot help; the agent backs off until it is reconfigured.
type AuthError struct {
	Body string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("server rejected agent credentials: %s", e.Body)
}

// NewClient returns a client that posts to url. Redirects are not followed,
// so credentials are never sent to another host.
func NewClient(baseURL, apiKey, serverToken, hostname, version string) *Client {
	return &Client{
		BaseURL:     baseURL,
		APIKey:      apiKey,
		ServerToken: serverToken,
		Hostname:    sanitizeHeaderValue(hostname),
		Version:     sanitizeHeaderValue(version),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12,
				},
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// SendMetrics posts metrics as gzip-compressed JSON.
func (c *Client) SendMetrics(metrics any) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := json.NewEncoder(gz).Encode(metrics); err != nil {
		return fmt.Errorf("encoding metrics: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("compressing metrics: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.BaseURL, &buf)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	// Older servers also expect the account API key; new installs have none.
	if c.APIKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.APIKey))
	}
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
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusUpgradeRequired {
			return &UpgradeRequiredError{Body: string(body)}
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return &AuthError{Body: string(body)}
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
