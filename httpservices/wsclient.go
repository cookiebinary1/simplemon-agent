// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// WebSocket client for real-time metric streaming and server commands.

package httpservices

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsReadTimeout  = 60 * time.Second
	wsPingInterval = 30 * time.Second
)

// wsWriteTimeout bounds every write; a variable so tests can shorten it.
var wsWriteTimeout = 10 * time.Second

// WSClient is one WebSocket connection to the server.
type WSClient struct {
	conn      *websocket.Conn
	writeMu   sync.Mutex // gorilla allows only one concurrent writer
	done      chan struct{}
	closeOnce sync.Once
}

// WSDialerHook lets the load simulator adjust the dialer (target address,
// TLS). The agent leaves it nil.
var WSDialerHook func(*websocket.Dialer)

// NewWSClient connects to the server and starts the keep-alive pings.
func NewWSClient(url, apiKey, serverToken, version string) (*WSClient, error) {
	dialer := &websocket.Dialer{
		TLSClientConfig:  &tls.Config{MinVersion: tls.VersionTLS12},
		HandshakeTimeout: 10 * time.Second,
	}
	if WSDialerHook != nil {
		WSDialerHook(dialer)
	}
	headers := http.Header{"Server-Token": {serverToken}}
	if apiKey != "" {
		headers.Set("Authorization", "Bearer "+apiKey)
	}
	if v := sanitizeHeaderValue(version); v != "" {
		headers.Set("X-Agent-Version", v)
	}

	conn, resp, err := dialer.Dial(url, headers)
	if err != nil {
		if resp == nil {
			return nil, fmt.Errorf("websocket connection failed: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, &AuthError{Body: string(body)}
		}
		return nil, fmt.Errorf("websocket connection failed: %w, response: %s", err, body)
	}

	c := &WSClient{conn: conn, done: make(chan struct{})}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	})
	go c.ping()
	return c, nil
}

func (c *WSClient) ping() {
	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			// WriteControl may run concurrently with WriteMessage.
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteTimeout)); err != nil {
				slog.Warn("websocket ping failed", "error", err)
				return
			}
		}
	}
}

// write sends one text frame. The deadline keeps a stalled connection from
// blocking the caller (and every later writer) forever.
func (c *WSClient) write(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// SendMetrics sends v as a JSON text frame.
func (c *WSClient) SendMetrics(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding websocket message: %w", err)
	}
	if err := c.write(data); err != nil {
		return fmt.Errorf("sending websocket message: %w", err)
	}
	return nil
}

// SendMessage sends a raw text frame.
func (c *WSClient) SendMessage(message string) error {
	if err := c.write([]byte(message)); err != nil {
		return fmt.Errorf("sending websocket message: %w", err)
	}
	return nil
}

// ReceiveMessageWithContext waits for the next text frame. Cancelling ctx
// unblocks the read.
func (c *WSClient) ReceiveMessageWithContext(ctx context.Context) (string, error) {
	stop := context.AfterFunc(ctx, func() {
		_ = c.conn.SetReadDeadline(time.Now())
	})
	defer stop()

	_ = c.conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	_, message, err := c.conn.ReadMessage()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("receiving websocket message: %w", err)
	}
	return string(message), nil
}

// Close closes the connection and stops the pings. It is safe to call more
// than once and never waits for a blocked writer.
func (c *WSClient) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}
