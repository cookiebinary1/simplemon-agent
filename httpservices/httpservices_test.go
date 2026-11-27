// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT

package httpservices

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientSendMetrics_GzipEncoding(t *testing.T) {
	t.Parallel()

	received := make(chan map[string]any, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Content-Encoding"), "gzip"; got != want {
			t.Errorf("Content-Encoding = %s, want %s", got, want)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer test-key"; got != want {
			t.Errorf("Authorization header mismatch, got %s", got)
		}
		if got, want := r.Header.Get("Server-Token"), "server-token"; got != want {
			t.Errorf("Server-Token mismatch, got %s", got)
		}
		if got, want := r.Header.Get("X-Hostname"), "host01"; got != want {
			t.Errorf("X-Hostname mismatch, got %s", got)
		}
		if got, want := r.Header.Get("X-Agent-Version"), "v1.2.3"; got != want {
			t.Errorf("X-Agent-Version mismatch, got %s", got)
		}

		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Fatalf("failed to create gzip reader: %v", err)
		}
		defer gz.Close()

		body, err := io.ReadAll(gz)
		if err != nil {
			t.Fatalf("failed to read gz body: %v", err)
		}

		var metrics []map[string]any
		if err := json.Unmarshal(body, &metrics); err != nil {
			t.Fatalf("invalid json payload: %v", err)
		}
		if len(metrics) != 1 {
			t.Fatalf("unexpected metrics length: %d", len(metrics))
		}
		received <- metrics[0]

		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test-key", "server-token", "host01", "v1.2.3")
	payload := []map[string]any{{
		"timestamp":  "2024-01-01 00:00:00",
		"delta_time": 1.0,
	}}

	if err := client.SendMetrics(payload); err != nil {
		t.Fatalf("SendMetrics returned error: %v", err)
	}

	select {
	case got := <-received:
		if got["timestamp"] != payload[0]["timestamp"] {
			t.Fatalf("timestamp mismatch got %v", got["timestamp"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive payload in time")
	}
}

func TestClientSendMetrics_ErrorResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test-key", "server-token", "host01", "v1.2.3")

	err := client.SendMetrics([]map[string]any{{"timestamp": "2024-01-01 00:00:00"}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "unexpected status code") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestClientSendMetrics_UpgradeRequired(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUpgradeRequired)
		w.Write([]byte(`{"error":"agent version too old","min_version":"v2.0.0","agent_version":"v1.2.3"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "test-key", "server-token", "host01", "v1.2.3")

	err := client.SendMetrics([]map[string]any{{"timestamp": "2024-01-01 00:00:00"}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var upgrade *UpgradeRequiredError
	if !errors.As(err, &upgrade) {
		t.Fatalf("expected UpgradeRequiredError, got %T: %v", err, err)
	}
	if !strings.Contains(upgrade.Body, "min_version") {
		t.Fatalf("expected server body to be preserved, got %q", upgrade.Body)
	}
}

func TestClientRejectsRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Error("metrics client followed a redirect")
		}
		http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", "test-token", "test-host", "")
	if err := client.SendMetrics([]string{"test"}); err == nil {
		t.Fatal("expected a redirect response to fail the upload")
	}
}
