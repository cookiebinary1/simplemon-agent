// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// WebSocket client for real-time metric streaming and server communication.

package httpservices

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"simplemon-agent/internal/logger"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsReadDeadline = 60 * time.Second
	wsPingInterval = 30 * time.Second
	wsPingDeadline = 1 * time.Second
)

// WSClient manages a WebSocket connection to the server.
type WSClient struct {
	BaseURL     string
	APIKey      string
	ServerToken string
	WSConn      *websocket.Conn
	done        chan struct{}
	writeMu     sync.Mutex    // protects writes to the connection
	connMu      sync.RWMutex  // protects access to WSConn
	closeOnce   sync.Once
	closed      atomic.Bool
}

// NewWSClient creates a new WebSocket client and connects to the server.
// It starts a background ping goroutine to keep the connection alive.
func NewWSClient(baseURL, apiKey, serverToken, version string) (*WSClient, error) {
	dialer := &websocket.Dialer{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
		HandshakeTimeout: 10 * time.Second,
	}
	headers := http.Header{
		"Authorization": {"Bearer " + apiKey},
		"Server-Token":  {serverToken},
	}
	if v := sanitizeHeaderValue(version); v != "" {
		headers.Set("X-Agent-Version", v)
	}

	conn, resp, err := dialer.Dial(baseURL, headers)
	if err != nil {
		if resp != nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("websocket connection failed: %v, response: %s", err, string(body))
		}
		return nil, fmt.Errorf("websocket connection failed: %v", err)
	}

	client := &WSClient{
		BaseURL:     baseURL,
		APIKey:      apiKey,
		ServerToken: serverToken,
		WSConn:      conn,
		done:        make(chan struct{}),
	}

	// Set up ping handler
	conn.SetPingHandler(func(string) error {
		client.writeMu.Lock()
		defer client.writeMu.Unlock()
		return conn.WriteControl(websocket.PongMessage, []byte{}, time.Now().Add(wsPingDeadline))
	})

	// Set up pong handler
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	})

	// Start ping goroutine
	go client.ping()

	return client, nil
}

// ping sends periodic ping messages to keep the connection alive.
func (c *WSClient) ping() {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Check if client is closed
			if c.closed.Load() {
				return
			}

			// Get connection under lock
			c.connMu.RLock()
			conn := c.WSConn
			c.connMu.RUnlock()

			if conn == nil {
				return
			}

			c.writeMu.Lock()
			err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(wsPingDeadline))
			c.writeMu.Unlock()

			if err != nil {
				logger.Error("WebSocket ping failed", "error", err)
				return
			}
		case <-c.done:
			return
		}
	}
}

// SendMetrics sends metrics data as JSON over the WebSocket connection.
func (c *WSClient) SendMetrics(metrics interface{}) error {
	if c.closed.Load() {
		return fmt.Errorf("websocket client is closed")
	}

	c.connMu.RLock()
	conn := c.WSConn
	c.connMu.RUnlock()

	if conn == nil {
		return fmt.Errorf("websocket connection is nil")
	}

	jsonData, err := json.Marshal(metrics)
	if err != nil {
		return fmt.Errorf("error marshaling metrics: %w", err)
	}

	c.writeMu.Lock()
	err = conn.WriteMessage(websocket.TextMessage, jsonData)
	c.writeMu.Unlock()

	if err != nil {
		return fmt.Errorf("error sending data over WebSocket: %w", err)
	}

	return nil
}

// SendMessage sends a text message over the WebSocket connection.
func (c *WSClient) SendMessage(message string) error {
	if c.closed.Load() {
		return fmt.Errorf("websocket client is closed")
	}

	c.connMu.RLock()
	conn := c.WSConn
	c.connMu.RUnlock()

	if conn == nil {
		return fmt.Errorf("websocket connection is nil")
	}

	c.writeMu.Lock()
	err := conn.WriteMessage(websocket.TextMessage, []byte(message))
	c.writeMu.Unlock()

	if err != nil {
		return fmt.Errorf("error sending message over WebSocket: %w", err)
	}

	return nil
}

// ReceiveMessage receives a text message from the WebSocket connection.
// It blocks until a message is received or the read deadline expires.
func (c *WSClient) ReceiveMessage() (string, error) {
	c.connMu.RLock()
	conn := c.WSConn
	c.connMu.RUnlock()

	if conn == nil {
		return "", fmt.Errorf("websocket connection is nil")
	}

	conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	_, message, err := conn.ReadMessage()
	if err != nil {
		return "", fmt.Errorf("error receiving message over WebSocket: %w", err)
	}
	return string(message), nil
}

// ReceiveMessageWithContext receives a message with context cancellation support.
// When context is cancelled, the read deadline is set to immediately expire
// to unblock the read operation.
func (c *WSClient) ReceiveMessageWithContext(ctx context.Context) (string, error) {
	c.connMu.RLock()
	conn := c.WSConn
	c.connMu.RUnlock()

	if conn == nil {
		return "", fmt.Errorf("websocket connection is nil")
	}

	// Start a goroutine that will expire the deadline when context is cancelled
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			// Context cancelled - set immediate deadline to unblock ReadMessage
			c.connMu.RLock()
			if c.WSConn != nil {
				c.WSConn.SetReadDeadline(time.Now())
			}
			c.connMu.RUnlock()
		case <-done:
			// Normal completion
		}
	}()

	conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	_, message, err := conn.ReadMessage()
	if err != nil {
		// Check if error is due to context cancellation
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("error receiving message over WebSocket: %w", err)
	}
	return string(message), nil
}

// Close closes the WebSocket connection and stops the ping goroutine.
// It is safe to call Close multiple times.
func (c *WSClient) Close() {
	if c == nil {
		return
	}

	// Mark as closed first
	c.closed.Store(true)

	// Signal ping goroutine to stop
	c.closeOnce.Do(func() {
		close(c.done)
	})

	// Close the connection under lock
	c.connMu.Lock()
	conn := c.WSConn
	c.WSConn = nil
	c.connMu.Unlock()

	if conn != nil {
		c.writeMu.Lock()
		_ = conn.Close()
		c.writeMu.Unlock()
	}
}
