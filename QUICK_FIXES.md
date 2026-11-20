# Quick Fixes - Emergency Patches

**Účel**: Rýchle fixe pre 5 kritických problémov
**Čas**: ~6 hodín
**Target**: Release v1.0.1

---

## 🚨 Fix #1: Type Assertion Panic (15 min)

**File**: `internal/agent/agent.go:643`

**Nájdi**:
```go
if v := a.lastError.Load(); v != nil {
    lastErr = v.(string)
}
```

**Zmeň na**:
```go
if v := a.lastError.Load(); v != nil {
    if s, ok := v.(string); ok {
        lastErr = s
    } else {
        lastErr = fmt.Sprintf("invalid error type: %T", v)
    }
}
```

**Test**:
```bash
go test -run TestHealthEndpoint internal/agent
```

---

## 🔒 Fix #2: Header Injection (30 min)

**File**: `httpservices/httpservices.go`

**Pridaj funkciu** (pred `NewClient`):
```go
// sanitizeHeaderValue removes CRLF characters to prevent header injection
func sanitizeHeaderValue(value string) string {
    return strings.Map(func(r rune) rune {
        if r == '\n' || r == '\r' {
            return -1 // drop character
        }
        return r
    }, value)
}
```

**Zmeň riadok 74**:
```go
// Bolo:
req.Header.Set("X-Hostname", c.Hostname)

// Zmeň na:
req.Header.Set("X-Hostname", sanitizeHeaderValue(c.Hostname))
```

**Test**:
```bash
# Test s malicious hostname
export HOSTNAME=$'evil.com\r\nX-Injected: malicious'
go run cmd/simplemon-agent/main.go -config simplemon.conf
# Check HTTP logs - nesmie byť X-Injected header
```

---

## 💥 Fix #3: Goroutine Leak v WebSocket (2h)

**File**: `httpservices/wsclient.go`

**Pridaj novú metódu**:
```go
// ReceiveMessageContext receives a message with context cancellation support
func (c *WSClient) ReceiveMessageContext(ctx context.Context) (string, error) {
    type result struct {
        msg string
        err error
    }

    resultCh := make(chan result, 1)
    go func() {
        c.WSConn.SetReadDeadline(time.Now().Add(60 * time.Second))
        _, message, err := c.WSConn.ReadMessage()
        resultCh <- result{msg: string(message), err: err}
    }()

    select {
    case <-ctx.Done():
        return "", ctx.Err()
    case res := <-resultCh:
        if res.err != nil {
            return "", fmt.Errorf("error receiving message over WebSocket: %v", res.err)
        }
        return res.msg, nil
    }
}
```

**File**: `internal/agent/agent.go:293`

**Zmeň**:
```go
// Bolo:
message, err := a.wsClient.ReceiveMessage()

// Zmeň na:
message, err := a.wsClient.ReceiveMessageContext(ctx)
```

**Test**:
```bash
# Start agent
go run cmd/simplemon-agent/main.go &
PID=$!

# Wait 2s
sleep 2

# Send SIGTERM
kill -TERM $PID

# Agent by mal skončiť do 1s (nie 60s!)
# Check: ps aux | grep simplemon
```

---

## 🔄 Fix #4: Race Condition na rtStopCh (3h)

**File**: `internal/agent/agent.go`

Toto je komplexnejšie. Náhrada celej `rtStreamingLoop()`:

**Zmeň funkciu** (riadky 375-436):
```go
func (a *Agent) rtStreamingLoop() {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    var lastSentTimestamp string
    logger.Info("rtStreamingLoop started")

    for {
        select {
        case <-ticker.C:
            // Check if RT is still active
            a.rtMu.Lock()
            if !a.rtActive {
                a.rtMu.Unlock()
                logger.Info("rtStreamingLoop: RT not active, exiting")
                return
            }

            // Check timeout
            lastTick := a.lastTickTime
            if !lastTick.IsZero() && time.Since(lastTick) > rtTimeout {
                logger.Warn("RT streaming timeout - no TICK for 30+ seconds")
                a.rtActive = false
                a.rtMu.Unlock()
                return
            }
            a.rtMu.Unlock()

            // Get current entry
            a.metricMu.RLock()
            entry := a.currentEntry
            a.metricMu.RUnlock()

            if entry.Timestamp == "" || entry.Timestamp == lastSentTimestamp {
                continue
            }

            // Send metrics
            a.wsClientMu.RLock()
            wsClient := a.wsClient
            a.wsClientMu.RUnlock()

            if wsClient != nil {
                logger.Debug("rtStreamingLoop: sending metrics", "timestamp", entry.Timestamp)
                if err := wsClient.SendMetrics(entry); err != nil {
                    logger.Error("error sending RT metrics", "error", err)
                } else {
                    lastSentTimestamp = entry.Timestamp
                }
            }

        case <-func() <-chan struct{} {
            a.rtMu.Lock()
            defer a.rtMu.Unlock()
            return a.rtStopCh
        }():
            logger.Info("rtStreamingLoop: received stop signal")
            return
        }
    }
}
```

**Test**:
```bash
go test -race -run TestRTStreaming internal/agent
```

---

## ⚡ Fix #5: Concurrent Batch Send (4h)

**File**: `internal/agent/agent.go`

### Krok 1: Pridaj do Agent struct (po riadku 77):
```go
    // Batch worker
    batchWorkerCh chan struct{}
    batchWorkerWg sync.WaitGroup
```

### Krok 2: Pridaj v `New()` funkciu (po riadku 119):
```go
    batchWorkerCh: make(chan struct{}, 1),
```

### Krok 3: V `Run()` funkciu, pridaj pred `for` loop (po riadku 158):
```go
    // Start batch worker
    a.batchWorkerWg.Add(1)
    go a.batchWorker()
```

### Krok 4: Zmeň batch ticker handling (riadok 178):
```go
// Bolo:
case <-batchTicker.C:
    go a.sendBatchMetrics()

// Zmeň na:
case <-batchTicker.C:
    // Trigger batch send (non-blocking)
    select {
    case a.batchWorkerCh <- struct{}{}:
    default:
        // Worker je busy, skip tento tick
        logger.Debug("batch worker busy, skipping tick")
    }
```

### Krok 5: Pridaj novú funkciu `batchWorker()` (po `sendBatchMetrics()`):
```go
// batchWorker processes batch sends serially
func (a *Agent) batchWorker() {
    defer a.batchWorkerWg.Done()
    logger.Info("batch worker started")

    for range a.batchWorkerCh {
        a.sendBatchMetrics()
    }

    logger.Info("batch worker stopped")
}
```

### Krok 6: Upraviť `shutdown()` (po riadku 198):
```go
// Bolo:
a.sendBatchMetrics()

// Zmeň na:
// Stop batch worker
close(a.batchWorkerCh)
a.batchWorkerWg.Wait()

// Send final batch
a.sendBatchMetrics()
```

**Test**:
```bash
# Stress test s veľa metrík
go test -race -run TestBatchSend internal/agent
```

---

## 🧪 Celkový Test Plan

Po všetkých fixoch:

```bash
# 1. Unit testy
go test -race ./...

# 2. Build
go build -o simplemon-agent cmd/simplemon-agent/main.go

# 3. Manuálny test
./simplemon-agent -config simplemon.conf &
AGENT_PID=$!

# 4. Pozri health endpoint
curl http://localhost:8080/health

# 5. Stress test (1000 metrík)
for i in {1..1000}; do
    echo "Iteration $i"
    sleep 0.1
done

# 6. Graceful shutdown
kill -TERM $AGENT_PID

# 7. Verify clean exit (< 2s)
wait $AGENT_PID
echo "Exit code: $?"

# 8. Check pre goroutine leaks
go test -run TestGoroutineLeaks internal/agent
```

---

## 📦 Release Checklist

Pre v1.0.1:

- [ ] Všetky 5 fixe aplikované
- [ ] Unit testy pass (`go test ./...`)
- [ ] Race detector pass (`go test -race ./...`)
- [ ] Manuálne testovanie OK
- [ ] Git commit s popisom
- [ ] Git tag `v1.0.1`
- [ ] Build binárky
- [ ] Deploy do production

---

## 🔍 Verifikácia po Deploy

```bash
# 1. Check health endpoint
curl https://your-agent:8080/health

# 2. Monitor memory usage
watch -n 1 'ps aux | grep simplemon-agent'

# 3. Check pre goroutine leaks (po 24h)
curl https://your-agent:8080/debug/pprof/goroutine

# 4. Verify logs (žiadne panics)
journalctl -u simplemon-agent -f | grep -i panic

# 5. Monitor batch success rate
# (check server-side metrics)
```

---

## ⚠️ Rollback Plan

Ak sa niečo pokazí:

```bash
# 1. Stop nový agent
systemctl stop simplemon-agent

# 2. Rollback binary
cp /backup/simplemon-agent-v1.0.0 /usr/bin/simplemon-agent

# 3. Restart
systemctl start simplemon-agent

# 4. Verify
curl http://localhost:8080/health
```

---

## 📞 Support

Ak máš problémy:
1. Check logs: `journalctl -u simplemon-agent -n 100`
2. Check health: `curl localhost:8080/health`
3. Report issue s logs + config

---

**Koniec quick fixes guide** - Good luck! 🚀
