// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Package agent provides the main orchestrator for metric collection and transmission.
// It manages WebSocket connections, batch metric sending, and real-time streaming.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cookiebinary1/simplemon-agent/config"
	"github.com/cookiebinary1/simplemon-agent/httpservices"
	"github.com/cookiebinary1/simplemon-agent/internal"
	"github.com/cookiebinary1/simplemon-agent/metrics"
)

// MetricEntry represents a single metric measurement
type MetricEntry struct {
	Timestamp string                   `json:"timestamp"`
	DeltaTime float64                  `json:"delta_time"`
	Memory    *internal.MemoryMetrics  `json:"memory"`
	CPU       *internal.CPUMetrics     `json:"cpu"`
	Disks     []internal.DiskMetric    `json:"disks"`
	IO        *internal.IoMetrics      `json:"io"`
	Network   *internal.NetworkMetrics `json:"network,omitempty"`
	System    *internal.SystemMetrics  `json:"system,omitempty"`
	Info      *internal.SystemInfo     `json:"info,omitempty"`
	Errors    []string                 `json:"errors,omitempty"`
}

// Agent is the main orchestrator for metric collection and transmission
type Agent struct {
	cfg              *config.Config
	httpClient       *httpservices.Client
	wsClient         *httpservices.WSClient
	metricsCollector *metrics.Collector

	// Metric collection
	metricInterval time.Duration
	batchInterval  time.Duration
	storeInterval  time.Duration

	// Current metrics (protected by mutex)
	metricMu     sync.RWMutex
	currentEntry MetricEntry
	history      []MetricEntry // last rtHistorySize 1 s samples for RT charts

	// Batch storage (protected by mutex)
	batchMu sync.Mutex
	batch   []MetricEntry
	// Per-second samples waiting to be averaged into one stored sample
	window []MetricEntry
	// Minute (unix) of the last stored sample that carried process lists
	lastProcessMinute int64
	// Batch sending (serialize HTTP sends)
	batchSendMu    sync.Mutex
	batchTriggerCh chan struct{}

	// RT streaming state (protected by mutex)
	rtMu         sync.Mutex
	rtActive     bool
	rtStopCh     chan struct{}
	lastTickTime time.Time

	// WebSocket reconnection
	wsReconnectMu  sync.Mutex
	reconnectDelay time.Duration
	wsClientMu     sync.RWMutex

	// Timing
	lastMeasurement time.Time
	lastInfo        time.Time

	// URLs
	httpURL  string
	wsURL    string
	hostname string

	// Health status
	healthServer *http.Server
	metricsCount atomic.Int64
	batchesSent  atomic.Int64
	lastError    atomic.Pointer[string]
	wsConnected  atomic.Bool
	// Unix nanos timestamp until which the version-blocked warning is suppressed.
	upgradeBlockedUntil atomic.Int64
	// Consecutive 401 responses and the unix nanos until which sends are paused.
	authFailures     atomic.Int32
	authBlockedUntil atomic.Int64
}

// Backoff after rejected credentials: 30s, 1m, 2m, 4m, 8m, then 10m.
const (
	authBackoffStart = 30 * time.Second
	authBackoffMax   = 10 * time.Minute
)

// authBackoff returns the wait after the given number of consecutive 401s.
func authBackoff(failures int32) time.Duration {
	delay := authBackoffStart
	for i := int32(1); i < failures && delay < authBackoffMax; i++ {
		delay *= 2
	}
	return min(delay, authBackoffMax)
}

// recordAuthFailure pauses all server traffic after a 401 and returns the pause.
func (a *Agent) recordAuthFailure(err error) time.Duration {
	delay := authBackoff(a.authFailures.Add(1))
	a.authBlockedUntil.Store(time.Now().Add(delay).UnixNano())
	slog.Error("server rejected agent credentials, check api key and server token",
		"error", err,
		"retry_in", delay,
	)
	return delay
}

// recordAuthSuccess clears the credential backoff.
func (a *Agent) recordAuthSuccess() {
	a.authFailures.Store(0)
	a.authBlockedUntil.Store(0)
}

const (
	defaultMetricInterval = 1 * time.Second
	defaultBatchInterval  = 10 * time.Second
	defaultStoreInterval  = 10 * time.Second
	defaultReconnectDelay = 5 * time.Second
	maxReconnectDelay     = 5 * time.Minute
	rtTimeout             = 30 * time.Second
	rtHistorySize         = 120
	maxBatchSize          = 1000
	batchTriggerBuffer    = 1
	// How often to repeat the "server rejected this agent version" warning.
	upgradeBlockLogInterval = time.Hour
)

// Version is reported to the server in the X-Agent-Version header so the
// panel can track and enforce versions. main sets it from the build.
var Version = "dev"

// New creates a new Agent instance
func New(cfg *config.Config) *Agent {
	httpProtocol := "http"
	wsProtocol := "ws"
	if cfg.Secure {
		httpProtocol = "https"
		wsProtocol = "wss"
	}

	hostname, err := os.Hostname()
	if err != nil {
		slog.Warn("failed to get hostname", "error", err)
	}

	httpURL := fmt.Sprintf("%s://%s", httpProtocol, cfg.ServiceHost)
	wsURL := fmt.Sprintf("%s://%s", wsProtocol, cfg.ServiceHost)

	// Use configured intervals or defaults
	metricInterval := time.Duration(cfg.MetricInterval) * time.Millisecond
	batchInterval := time.Duration(cfg.BatchInterval) * time.Millisecond
	storeInterval := time.Duration(cfg.StoreInterval) * time.Millisecond
	if metricInterval <= 0 {
		metricInterval = defaultMetricInterval
	}
	if batchInterval <= 0 {
		batchInterval = defaultBatchInterval
	}
	if storeInterval <= 0 {
		storeInterval = defaultStoreInterval
	}
	if storeInterval < metricInterval {
		storeInterval = metricInterval
	}

	return &Agent{
		cfg:              cfg,
		httpClient:       httpservices.NewClient(httpURL+"/api/v1/metrics/batch", cfg.ApiKey, cfg.ServerToken, hostname, Version),
		metricsCollector: metrics.NewCollector(),
		metricInterval:   metricInterval,
		batchInterval:    batchInterval,
		storeInterval:    storeInterval,
		reconnectDelay:   defaultReconnectDelay,
		lastMeasurement:  time.Now(),
		httpURL:          httpURL,
		wsURL:            wsURL,
		hostname:         hostname,
		batchTriggerCh:   make(chan struct{}, batchTriggerBuffer),
	}
}

// Run starts the agent and blocks until context is cancelled
func (a *Agent) Run(ctx context.Context) error {
	// Start health server if configured
	if a.cfg.HealthPort > 0 {
		a.healthServer = a.newHealthServer()
		go func(server *http.Server) {
			slog.Info("starting health server", "addr", server.Addr)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("health server error", "error", err)
			}
		}(a.healthServer)
	}

	// Connect to WebSocket
	go func() {
		if err := a.connectWebSocket(ctx); err != nil && ctx.Err() == nil {
			slog.Error("initial WebSocket connection failed", "error", err)
		}
	}()

	// Start metric collection ticker
	metricTicker := time.NewTicker(a.metricInterval)
	defer metricTicker.Stop()

	// Start batch send ticker
	batchTicker := time.NewTicker(a.batchInterval)
	defer batchTicker.Stop()

	// Average per-second samples into one stored sample
	storeTicker := time.NewTicker(a.storeInterval)
	defer storeTicker.Stop()

	// Start WebSocket message handler
	go a.handleWebSocketMessages(ctx)
	go a.batchWorker(ctx)

	slog.Info("agent started",
		"metricInterval", a.metricInterval,
		"batchInterval", a.batchInterval,
		"storeInterval", a.storeInterval,
		"hostname", a.hostname,
	)

	for {
		select {
		case <-ctx.Done():
			return a.shutdown()

		case <-metricTicker.C:
			a.collectMetrics()

		case <-storeTicker.C:
			a.flushWindow(time.Now())

		case <-batchTicker.C:
			a.triggerBatchSend()
		}
	}
}

// shutdown performs graceful shutdown
func (a *Agent) shutdown() error {
	slog.Info("shutting down agent...")

	// Stop health server
	if a.healthServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.healthServer.Shutdown(ctx)
	}

	// Stop RT streaming if active
	a.stopRTStreaming()

	// Close WebSocket
	a.wsClientMu.Lock()
	wsClient := a.wsClient
	a.wsClient = nil
	a.wsClientMu.Unlock()
	if wsClient != nil {
		wsClient.Close()
	}

	// Send remaining samples
	a.flushWindow(time.Now())
	a.sendBatchMetrics()

	slog.Info("agent stopped",
		"metricsCollected", a.metricsCount.Load(),
		"batchesSent", a.batchesSent.Load(),
	)
	return nil
}

// connectWebSocket establishes WebSocket connection with exponential backoff
func (a *Agent) connectWebSocket(ctx context.Context) error {
	a.wsReconnectMu.Lock()
	defer a.wsReconnectMu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wsClient, err := httpservices.NewWSClient(a.wsURL+"/incoming", a.cfg.ApiKey, a.cfg.ServerToken, Version)
		if err != nil {
			a.wsConnected.Store(false)
			errStr := err.Error()
			a.lastError.Store(&errStr)
			var authErr *httpservices.AuthError
			if errors.As(err, &authErr) {
				select {
				case <-time.After(a.recordAuthFailure(err)):
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
			delay := a.reconnectDelay
			a.reconnectDelay = min(a.reconnectDelay*2, maxReconnectDelay)
			slog.Error("WebSocket connection failed", "error", err, "retry_in", delay)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		if ctx.Err() != nil {
			wsClient.Close()
			return ctx.Err()
		}

		// Reset on success
		a.recordAuthSuccess()
		a.reconnectDelay = defaultReconnectDelay
		a.wsClientMu.Lock()
		a.wsClient = wsClient
		a.wsClientMu.Unlock()
		a.wsConnected.Store(true)
		slog.Info("connected to WebSocket")
		return nil
	}
}

// reconnectWebSocket handles WebSocket reconnection
func (a *Agent) reconnectWebSocket(ctx context.Context) {
	a.wsClientMu.Lock()
	wsClient := a.wsClient
	a.wsClient = nil
	a.wsClientMu.Unlock()
	if wsClient != nil {
		wsClient.Close()
	}
	if err := a.connectWebSocket(ctx); err != nil {
		slog.Error("WebSocket reconnection failed", "error", err)
	}
}

// handleWebSocketMessages processes incoming WebSocket messages
func (a *Agent) handleWebSocketMessages(ctx context.Context) {
	slog.Info("handleWebSocketMessages started")
	for {
		// Check context before attempting to receive
		select {
		case <-ctx.Done():
			slog.Info("handleWebSocketMessages: context done")
			return
		default:
		}

		a.wsClientMu.RLock()
		wsClient := a.wsClient
		a.wsClientMu.RUnlock()

		if wsClient == nil {
			slog.Debug("handleWebSocketMessages: wsClient is nil, waiting...")
			select {
			case <-ctx.Done():
				slog.Info("handleWebSocketMessages: context done while waiting for client")
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		// Use context-aware receive to allow graceful shutdown
		message, err := wsClient.ReceiveMessageWithContext(ctx)
		if err != nil {
			// If context was cancelled, exit gracefully
			if ctx.Err() != nil {
				slog.Info("handleWebSocketMessages: context cancelled during receive")
				return
			}
			slog.Error("WebSocket receive error", "error", err)
			a.wsConnected.Store(false)
			errStr := err.Error()
			a.lastError.Store(&errStr)
			a.reconnectWebSocket(ctx)
			continue
		}

		slog.Debug("received WebSocket message", "message", message)
		a.handleMessage(message)
	}
}

type wsMessage struct {
	Action  string `json:"action"`
	History bool   `json:"history"` // START from a newly opened chart asks for the buffered samples
}

// handleMessage processes a single WebSocket message
func (a *Agent) handleMessage(message string) {
	var msg wsMessage
	if err := json.Unmarshal([]byte(message), &msg); err != nil {
		slog.Warn("invalid WS message", "error", err, "raw", message)
		return
	}

	switch msg.Action {
	case "START":
		slog.Info("received START - activating RT mode")
		a.startRTStreaming()
		a.triggerBatchSend()
		if msg.History {
			go a.sendHistory()
		}

	case "STOP":
		slog.Info("received STOP - deactivating RT mode")
		a.stopRTStreaming()

	case "TICK":
		a.rtMu.Lock()
		a.lastTickTime = time.Now()
		a.rtMu.Unlock()
		slog.Debug("received TICK (keep-alive)")
	default:
		slog.Debug("unknown WS action", "action", msg.Action)
	}
}

// rtHistory is sent once per START so a new viewer sees the last minutes at 1 s resolution.
type rtHistory struct {
	Type    string        `json:"type"`
	Samples []MetricEntry `json:"samples"`
}

// sendHistory sends the buffered 1 s samples; only the newest keeps process lists.
func (a *Agent) sendHistory() {
	a.metricMu.RLock()
	samples := append([]MetricEntry(nil), a.history...)
	if len(samples) > 0 {
		latest := a.currentEntry
		latest.Info = nil
		samples[len(samples)-1] = latest
	}
	a.metricMu.RUnlock()
	if len(samples) == 0 {
		return
	}

	a.wsClientMu.RLock()
	wsClient := a.wsClient
	a.wsClientMu.RUnlock()
	if wsClient == nil {
		return
	}
	if err := wsClient.SendMetrics(rtHistory{Type: "history", Samples: samples}); err != nil {
		slog.Error("error sending RT history", "error", err)
	}
}

// slimEntry drops process lists and host info to keep the history small.
func slimEntry(e MetricEntry) MetricEntry {
	e.Info = nil
	e.Errors = nil
	if e.CPU != nil {
		cpu := *e.CPU
		cpu.Processes = nil
		e.CPU = &cpu
	}
	if e.Memory != nil {
		mem := *e.Memory
		mem.Processes = nil
		e.Memory = &mem
	}
	if e.IO != nil {
		io := *e.IO
		io.Processes = nil
		e.IO = &io
	}
	return e
}

// startRTStreaming starts real-time metric streaming
func (a *Agent) startRTStreaming() {
	a.rtMu.Lock()
	defer a.rtMu.Unlock()

	// Already running
	if a.rtActive {
		return
	}

	a.rtActive = true
	a.lastTickTime = time.Now()
	a.rtStopCh = make(chan struct{})

	go a.rtStreamingLoop(a.rtStopCh)
	slog.Info("RT streaming started")
}

// stopRTStreaming stops real-time metric streaming
func (a *Agent) stopRTStreaming() {
	a.rtMu.Lock()
	defer a.rtMu.Unlock()

	if !a.rtActive {
		return
	}

	a.rtActive = false
	if a.rtStopCh != nil {
		close(a.rtStopCh)
		a.rtStopCh = nil
	}
	slog.Info("RT streaming stopped")
}

// rtStreamingLoop streams metrics when new samples are available. It owns
// stop, so a quick STOP+START cannot leave an old loop running beside the new one.
func (a *Agent) rtStreamingLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastSentTimestamp string
	slog.Info("rtStreamingLoop started")

	for {
		select {
		case <-ticker.C:
			a.rtMu.Lock()
			lastTick := a.lastTickTime
			a.rtMu.Unlock()

			// Stop streaming when the viewers stopped sending TICKs.
			if !lastTick.IsZero() && time.Since(lastTick) > rtTimeout {
				slog.Warn("RT streaming timeout - no TICK for 30+ seconds")
				a.stopRTStreaming()
				return
			}

			a.metricMu.RLock()
			entry := a.currentEntry
			a.metricMu.RUnlock()

			if entry.Timestamp == "" || entry.Timestamp == lastSentTimestamp {
				continue
			}

			a.wsClientMu.RLock()
			wsClient := a.wsClient
			a.wsClientMu.RUnlock()
			if wsClient != nil {
				slog.Debug("rtStreamingLoop: sending metrics",
					"timestamp", entry.Timestamp,
					"hasCPU", entry.CPU != nil,
					"hasMemory", entry.Memory != nil,
					"hasDisks", len(entry.Disks) > 0,
					"hasIO", entry.IO != nil,
				)
				if err := wsClient.SendMetrics(entry); err != nil {
					slog.Error("error sending RT metrics", "error", err)
				} else {
					lastSentTimestamp = entry.Timestamp
				}
			}

		case <-stop:
			slog.Info("rtStreamingLoop: received stop signal")
			return
		}
	}
}

// collectMetrics gathers all system metrics in parallel
func (a *Agent) collectMetrics() {
	currentTime := time.Now()
	deltaTime := currentTime.Sub(a.lastMeasurement).Seconds()

	var (
		memoryMetrics                  *internal.MemoryMetrics
		cpuMetrics                     *internal.CPUMetrics
		diskMetrics                    []internal.DiskMetric
		ioMetrics                      *internal.IoMetrics
		memErr, cpuErr, diskErr, ioErr error
		wg                             sync.WaitGroup
	)

	processes, err := metrics.ReadProcesses()
	if err != nil {
		slog.Error("error listing processes", "error", err)
		return
	}

	// Gather all metrics in parallel using Collector
	wg.Add(4)

	go func() {
		defer wg.Done()
		memoryMetrics, memErr = a.metricsCollector.GatherMemory(processes)
	}()

	go func() {
		defer wg.Done()
		cpuMetrics, cpuErr = a.metricsCollector.GatherCPU(processes)
	}()

	go func() {
		defer wg.Done()
		diskMetrics, diskErr = metrics.GatherDiskMetrics()
	}()

	go func() {
		defer wg.Done()
		ioMetrics, ioErr = a.metricsCollector.GatherIO(processes)
	}()

	// Network and system data are optional; a failure here must not drop the sample.
	networkMetrics, netErr := a.metricsCollector.GatherNetwork()
	if netErr != nil {
		slog.Debug("error gathering network metrics", "error", netErr)
	}
	systemMetrics, sysErr := metrics.GatherSystem()
	if sysErr != nil {
		slog.Debug("error gathering system metrics", "error", sysErr)
	}

	wg.Wait()

	var errors []string
	if memErr != nil {
		slog.Error("error gathering memory metrics", "error", memErr)
		errors = append(errors, "memory: "+memErr.Error())
	}
	if cpuErr != nil {
		slog.Error("error gathering CPU metrics", "error", cpuErr)
		errors = append(errors, "cpu: "+cpuErr.Error())
	}
	if diskErr != nil {
		slog.Error("error gathering disk metrics", "error", diskErr)
		errors = append(errors, "disk: "+diskErr.Error())
	}
	if ioErr != nil {
		slog.Error("error gathering I/O metrics", "error", ioErr)
		errors = append(errors, "io: "+ioErr.Error())
	}

	if len(errors) > 0 {
		slog.Warn("skipping incomplete metric sample", "errors", errors)
		return
	}

	entry := MetricEntry{
		Timestamp: currentTime.UTC().Format(time.RFC3339Nano),
		DeltaTime: deltaTime,
		Memory:    memoryMetrics,
		CPU:       cpuMetrics,
		Disks:     diskMetrics,
		IO:        ioMetrics,
		Network:   networkMetrics,
		System:    systemMetrics,
		Info:      a.dueSystemInfo(currentTime),
		Errors:    errors,
	}

	// Update current entry
	a.metricMu.Lock()
	a.currentEntry = entry
	a.history = append(a.history, slimEntry(entry))
	if len(a.history) > rtHistorySize {
		a.history = a.history[len(a.history)-rtHistorySize:]
	}
	a.metricMu.Unlock()

	// Add to the averaging window
	a.batchMu.Lock()
	a.window = append(a.window, entry)
	a.batchMu.Unlock()

	a.lastMeasurement = currentTime
	a.metricsCount.Add(1)

	if a.cfg.Verbose {
		jsonMetrics, err := json.Marshal(entry)
		if err == nil {
			slog.Debug("gathered metrics", "json", string(jsonMetrics))
		}
	}
}

// flushWindow averages the collected samples into one stored sample.
// Process lists are stored once per wall-clock minute; they make up most
// of the stored rows and change slowly.
func (a *Agent) flushWindow(now time.Time) {
	a.batchMu.Lock()
	defer a.batchMu.Unlock()

	if len(a.window) == 0 {
		return
	}

	minute := now.Unix() / 60
	withProcesses := minute != a.lastProcessMinute
	if withProcesses {
		a.lastProcessMinute = minute
	}

	a.batch = append(a.batch, aggregateSamples(a.window, withProcesses))
	a.window = nil
}

func (a *Agent) triggerBatchSend() {
	select {
	case a.batchTriggerCh <- struct{}{}:
	default:
	}
}

func (a *Agent) batchWorker(ctx context.Context) {
	slog.Info("batchWorker started")
	for {
		select {
		case <-ctx.Done():
			slog.Info("batchWorker: context done")
			return
		case <-a.batchTriggerCh:
			a.sendBatchMetrics()
		}
	}
}

// sendBatchMetrics uploads the queued samples and tells the server over the
// WebSocket that a new batch is stored.
func (a *Agent) sendBatchMetrics() {
	if !a.uploadBatch() {
		return
	}
	a.wsClientMu.RLock()
	wsClient := a.wsClient
	a.wsClientMu.RUnlock()
	if wsClient != nil {
		if err := wsClient.SendMessage(`{"action": "ready"}`); err != nil {
			slog.Error("error sending ready signal", "error", err)
		}
	}
}

// uploadBatch sends the queued samples over HTTP. It reports whether a batch
// was stored; failed batches are queued again unless retrying cannot help.
func (a *Agent) uploadBatch() bool {
	a.batchSendMu.Lock()
	defer a.batchSendMu.Unlock()

	a.batchMu.Lock()
	batchToSend := a.batch
	a.batch = nil
	a.batchMu.Unlock()

	if len(batchToSend) == 0 {
		return false
	}

	// Credentials were rejected: drop data instead of hammering the server.
	if time.Now().UnixNano() < a.authBlockedUntil.Load() {
		return false
	}

	err := a.httpClient.SendMetrics(batchToSend)
	if err != nil {
		errStr := err.Error()
		a.lastError.Store(&errStr)

		var upgrade *httpservices.UpgradeRequiredError
		if errors.As(err, &upgrade) {
			// The server refuses this agent version entirely. Keeping the
			// batch would grow the queue forever, so drop it and warn at a
			// slow rate until the agent is upgraded.
			now := time.Now().UnixNano()
			if now > a.upgradeBlockedUntil.Load() {
				slog.Error("server rejected this agent version, metrics are NOT being collected - upgrade the agent",
					"version", Version,
					"details", upgrade.Body,
				)
				a.upgradeBlockedUntil.Store(now + upgradeBlockLogInterval.Nanoseconds())
			}
			return false
		}

		var authErr *httpservices.AuthError
		if errors.As(err, &authErr) {
			a.recordAuthFailure(err)
			return false
		}

		slog.Error("error sending metrics batch", "error", err, "batchSize", len(batchToSend))

		var rejected *httpservices.RejectedError
		if errors.As(err, &rejected) {
			slog.Error("dropping rejected batch", "batchSize", len(batchToSend))
			return false
		}

		// Re-add to batch for retry
		a.batchMu.Lock()
		combined := append(batchToSend, a.batch...)
		if len(combined) > maxBatchSize {
			dropped := len(combined) - maxBatchSize
			slog.Warn("batch queue overflow, dropping oldest retry data", "dropped", dropped, "retrySize", len(batchToSend), "newSize", len(a.batch))
			combined = combined[dropped:]
		}
		a.batch = combined
		a.batchMu.Unlock()
		return false
	}

	a.recordAuthSuccess()
	a.batchesSent.Add(1)
	slog.Debug("batch sent", "size", len(batchToSend))
	return true
}

// newHealthServer serves unauthenticated status, so it listens on loopback
// unless healthHost asks for another address.
func (a *Agent) newHealthServer() *http.Server {
	host := a.cfg.HealthHost
	if host == "" {
		host = "127.0.0.1"
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		lastErr := ""
		if v := a.lastError.Load(); v != nil {
			lastErr = *v
		}

		status := "healthy"
		statusCode := http.StatusOK
		if !a.wsConnected.Load() {
			status = "degraded"
			statusCode = http.StatusServiceUnavailable
		}

		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":           status,
			"wsConnected":      a.wsConnected.Load(),
			"metricsCollected": a.metricsCount.Load(),
			"batchesSent":      a.batchesSent.Load(),
			"hasError":         lastErr != "",
		})
	})

	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if a.wsConnected.Load() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ready"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("not ready"))
		}
	})

	return &http.Server{
		Addr:         net.JoinHostPort(host, strconv.Itoa(a.cfg.HealthPort)),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

// systemInfoInterval controls how often static host information is resent.
const systemInfoInterval = time.Hour

// dueSystemInfo returns host information on the first sample and then once per interval.
func (a *Agent) dueSystemInfo(now time.Time) *internal.SystemInfo {
	if !a.lastInfo.IsZero() && now.Sub(a.lastInfo) < systemInfoInterval {
		return nil
	}
	a.lastInfo = now
	return metrics.GatherSystemInfo()
}
