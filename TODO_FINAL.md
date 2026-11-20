# Simplemon Agent - TODO (Final)

**Vytvorené**: 2025-12-27

---

## 📋 Obsah

1. [Kritické (Security)](#kritické-security)
2. [Vysoká priorita (Data Loss / Concurrency)](#vysoká-priorita-data-loss--concurrency)
3. [Stredná priorita (Robustness)](#stredná-priorita-robustness)
4. [Nízka priorita (Code Quality)](#nízka-priorita-code-quality)
5. [Vylepšenia (Nice-to-have)](#vylepšenia-nice-to-have)
6. [Implementačný plán](#implementačný-plán)

---

## 🔴 Kritické (Security)

### 1. Secret logging v config/entrypoint

**Problém**: `ApiKey` a `ServerToken` sa printujú na stdout v plain texte.

**Riziko**: Credentials unikajú do logov, Docker logov, systemd journals, support bundles.

**Súbory**:
- `config/config.go:106-107`
- `cmd/simplemon-agent/main.go:22`

**Príklad problému**:
```go
// config/config.go:106-107
fmt.Printf("Api Key: %s\n", config.ApiKey)           // ❌ NEBEZPEČNÉ
fmt.Printf("Server Token: %s\n", config.ServerToken) // ❌ NEBEZPEČNÉ

// cmd/simplemon-agent/main.go:22
fmt.Printf("Config: %+v\n", cfg)  // ❌ Printuje celý struct vrátane secrets
```

**Fix**:
```go
// Maskovať tokeny - zobrazovať len posledné 4 znaky
func maskSecret(s string) string {
    if len(s) <= 4 {
        return "****"
    }
    return "****" + s[len(s)-4:]
}

fmt.Printf("Api Key: %s\n", maskSecret(config.ApiKey))
fmt.Printf("Server Token: %s\n", maskSecret(config.ServerToken))
```

**Priorita**: 🔥 CRITICAL - Opraviť ihneď
**Effort**: 2 hodiny

---

## 🟠 Vysoká priorita (Data Loss / Concurrency)

### 2. Concurrent batch send - Race condition

**Problém**: `sendBatchMetrics()` sa spúšťa v goroutine pri každom batch ticku. Keď je HTTP request pomalý, viacero sendov beží paralelne.

**Dôsledok**:
- Out-of-order batches
- Duplicitné re-queue
- Možná strata dát
- **Race condition** na `batch` slice

**Súbor**: `internal/agent/agent.go:160`

**Príklad problému**:
```go
// agent.go:160
case <-batchTicker.C:
    go a.sendBatchMetrics()  // ❌ Každý tick spustí novú goroutine!
```

Ak HTTP trvá 10s a batch interval je 5s:
```
T=0s:  Goroutine 1 starts → reads batch, sends HTTP
T=5s:  Goroutine 2 starts → reads batch (RACE!), sends HTTP
T=10s: Goroutine 1 finishes
T=10s: Goroutine 3 starts → reads batch (RACE!)
```

**Fix - Možnosť A (jednoduchšia)**: Spustiť inline
```go
case <-batchTicker.C:
    a.sendBatchMetrics()  // ✅ Blokuje až do dokončenia
```

**Fix - Možnosť B (lepšia)**: Worker pattern
```go
// V Agent struct
batchCh chan []MetricEntry

// V New()
batchCh: make(chan []MetricEntry, 10),

// V Run()
go a.batchWorker()

// Worker
func (a *Agent) batchWorker() {
    for batch := range a.batchCh {
        a.httpClient.SendMetrics(batch)
    }
}

// V batchTicker
case <-batchTicker.C:
    a.batchMu.Lock()
    if len(a.batch) > 0 {
        select {
        case a.batchCh <- a.batch:
            a.batch = nil
        default:
            logger.Warn("batch channel full, skipping")
        }
    }
    a.batchMu.Unlock()
```

**Priorita**: 🔥 CRITICAL - Môže spôsobiť data corruption
**Effort**: 4 hodiny (Možnosť B)

---

### 3. Drop batch if size > 500 on error

**Problém**: Pri HTTP chybe sa batche väčšie ako 500 položiek zahadzujú.

**Dôsledok**: Strata dát počas API downtime.

**Súbor**: `internal/agent/agent.go:499-502`

**Príklad problému**:
```go
if len(batchToSend) > 500 {
    logger.Warn("batch too large, discarding", "size", len(batchToSend))
    return  // ❌ Data loss!
}
```

**Fix - Bounded queue s drop-oldest stratégiou**:
```go
const maxBatchSize = 1000  // Maximálna veľkosť queue v pamäti

func (a *Agent) sendBatchMetrics() {
    a.batchMu.Lock()
    batchToSend := a.batch
    a.batch = nil
    a.batchMu.Unlock()

    if len(batchToSend) == 0 {
        return
    }

    err := a.httpClient.SendMetrics(batchToSend)
    if err != nil {
        a.lastError.Store(err.Error())
        logger.Error("error sending metrics batch", "error", err, "batchSize", len(batchToSend))

        // Re-add to batch, ale chráň pred memory overflow
        a.batchMu.Lock()
        combined := append(batchToSend, a.batch...)

        // Drop oldest ak je príliš veľký
        if len(combined) > maxBatchSize {
            dropped := len(combined) - maxBatchSize
            logger.Warn("queue overflow, dropping oldest", "dropped", dropped)
            a.batch = combined[dropped:]  // Zahoď najstaršie
        } else {
            a.batch = combined
        }
        a.batchMu.Unlock()
        return
    }

    a.batchesSent.Add(1)
    logger.Info("batch sent", "size", len(batchToSend))
}
```

**Priorita**: 🔥 HIGH - Data loss
**Effort**: 3 hodiny

---

### 4. Shutdown race condition

**Poznámka**: Rieši sa automaticky pri zavedení worker patternu v #2.

**Priorita**: 🟠 HIGH
**Effort**: 0-1 hodina po #2

---

### 5. Missing mutex protection pre prevCPUStat

**Problém**: `prevCPUStat` sa zapisuje bez mutex, zatiaľ čo `prevCoreStats` je chránený.

**Súbor**: `metrics/cpu.go:207`

**Fix**:
```go
// Presunúť prevCPUStat zápis do mutexu
if prevCPUStat != nil {
    overallUsage = calculateCPUUsage(prevCPUStat, allStats.Overall)
}

statsMutex.Lock()
prevCPUStat = allStats.Overall  // ✅ Chránené

for i := 0; i < numCPUCores; i++ {
    // ...
}
statsMutex.Unlock()
```

**Priorita**: 🟠 HIGH - Race condition
**Effort**: 30 minút

---

## 🟡 Stredná priorita (Robustness)

### 6. WebSocket reconnect ignoruje shutdown context

**Problém**: `connectWebSocket()` má nekonečný loop s backoff a nedá sa zrušiť.

**Dôsledok**: Slow shutdown alebo stuck process pri SIGTERM.

**Súbor**: `internal/agent/agent.go:195-232`

**Fix**: Pridať context parameter
```go
func (a *Agent) connectWebSocket(ctx context.Context) error {
    a.wsReconnectMu.Lock()
    defer a.wsReconnectMu.Unlock()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }

        wsClient, err := httpservices.NewWSClient(...)
        if err != nil {
            a.wsConnected.Store(false)
            logger.Error("WebSocket connection failed", "error", err)

            if a.retryCount < maxRetries {
                a.reconnectDelay *= 2
                a.retryCount++
            }

            logger.Info("retrying WebSocket connection", "delay", a.reconnectDelay)

            select {
            case <-time.After(a.reconnectDelay):
                continue
            case <-ctx.Done():
                return ctx.Err()
            }
        }

        a.retryCount = 0
        a.reconnectDelay = defaultReconnectDelay
        a.wsClient = wsClient
        a.wsConnected.Store(true)
        logger.Info("connected to WebSocket")
        return nil
    }
}
```

**Priorita**: 🟡 MEDIUM
**Effort**: 3 hodiny

---

### 7. Strict WS message string matching

**Problém**: `handleMessage()` matchuje exact JSON stringy, čo je krehké.

**Súbor**: `internal/agent/agent.go:274-290`

**Fix**: Parse JSON properly
```go
type WSMessage struct {
    Action string `json:"action"`
}

func (a *Agent) handleMessage(message string) {
    var msg WSMessage
    if err := json.Unmarshal([]byte(message), &msg); err != nil {
        logger.Error("invalid WS message format", "error", err, "raw", message)
        return
    }

    switch msg.Action {
    case "START":
        a.startRTStreaming()
        go a.sendBatchMetrics()
    case "STOP":
        a.stopRTStreaming()
    case "TICK":
        a.rtMu.Lock()
        a.lastTickTime = time.Now()
        a.rtMu.Unlock()
    default:
        logger.Warn("unknown WS action", "action", msg.Action)
    }
}
```

**Priorita**: 🟡 MEDIUM
**Effort**: 2 hodiny

---

### 8. All-or-nothing metrics collection

**Problém**: Ak zlyhá jeden collector (IO/Disk), celá metrika sa zahodí.

**Fix**: Partial payloads
```go
type MetricEntry struct {
    Timestamp string                  `json:"timestamp"`
    DeltaTime float64                 `json:"delta_time"`
    Memory    *internal.MemoryMetrics `json:"memory,omitempty"`
    CPU       *internal.CPUMetrics    `json:"cpu,omitempty"`
    Disks     []internal.DiskMetric   `json:"disks,omitempty"`
    IO        *internal.IoMetrics     `json:"io,omitempty"`
    Errors    []string                `json:"errors,omitempty"`
}
```

**Priorita**: 🟡 MEDIUM
**Effort**: 4 hodiny

---

### 9. Potential nil pointer v RT streaming

**Problém**: `wsClient` môže byť nil medzi checkom a použitím (TOCTOU).

**Fix**: Lock pre wsClient access
```go
// Pridať do Agent struct
wsClientMu sync.RWMutex

// V rtStreamingLoop
wsClient := func() *httpservices.WSClient {
    a.wsClientMu.RLock()
    defer a.wsClientMu.RUnlock()
    return a.wsClient
}()

if wsClient != nil {
    if err := wsClient.SendMetrics(entry); err != nil {
        logger.Error("error sending RT metrics", "error", err)
    }
}
```

**Priorita**: 🟡 MEDIUM
**Effort**: 1 hodina

---

## 🟢 Nízka priorita (Code Quality)

### 10. WS ping logging používa fmt.Println

**Fix**: route through structured logger.

**Priorita**: 🟢 LOW
**Effort**: 1 hodina

---

### 11. Interval sanity checks

**Fix**: add validation + bounds.

**Priorita**: 🟢 LOW
**Effort**: 2 hodiny

---

### 12. Magic numbers v kóde

**Fix**: centralizovať konštanty.

**Priorita**: 🟢 LOW
**Effort**: 1 hodina

---

### 13. Duplicitný kód v process aggregation

**Fix**: helper function (optional).

**Priorita**: 🟢 LOW
**Effort**: 4 hodiny

---

## 🔴 CRITICAL - Nové kritické problémy

### 10. Goroutine leak v handleWebSocketMessages

**Problém**: `wsClient.ReceiveMessage()` blokuje na 60s read deadline. Ak kontext sa zruší počas čakania, goroutine sa "zalepí" a neuvoľní.

**Súbor**: `internal/agent/agent.go:271-303`

**Scenár**:
```
1. handleWebSocketMessages() čaká na ReceiveMessage() (60s deadline)
2. User pošle SIGTERM
3. ctx.Done() je signalled, ale goroutine je v ReceiveMessage()
4. Goroutine čaká až do 60s timeout -> LEAK
```

**Fix**: Použiť context-aware ReceiveMessage
```go
// V WSClient
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
        return res.msg, res.err
    }
}

// V agent.go
message, err := a.wsClient.ReceiveMessageContext(ctx)
```

**Priorita**: 🔥 CRITICAL - Goroutine leak
**Effort**: 2 hodiny

---

### 11. Type assertion panic v health endpoint

**Problém**: `atomic.Value` type assertion bez check môže panic.

**Súbor**: `internal/agent/agent.go:643`

**Kód**:
```go
if v := a.lastError.Load(); v != nil {
    lastErr = v.(string)  // ❌ PANIC ak nie je string!
}
```

**Fix**:
```go
if v := a.lastError.Load(); v != nil {
    if s, ok := v.(string); ok {
        lastErr = s
    } else {
        lastErr = fmt.Sprintf("invalid error type: %T", v)
    }
}
```

**Priorita**: 🔥 CRITICAL - DoS risk
**Effort**: 15 minút

---

### 12. Header injection vulnerability

**Problém**: `X-Hostname` header je z `os.Hostname()` bez sanitizácie. CRLF injection risk.

**Súbor**: `httpservices/httpservices.go:74`

**Exploit scenario**:
```bash
# Malicious /etc/hostname
echo "evil.com\r\nX-Forwarded-For: 127.0.0.1" > /etc/hostname
```

**Fix**:
```go
func sanitizeHeaderValue(value string) string {
    // Remove newlines, carriage returns
    return strings.Map(func(r rune) rune {
        if r == '\n' || r == '\r' {
            return -1  // drop
        }
        return r
    }, value)
}

if c.Hostname != "" {
    req.Header.Set("X-Hostname", sanitizeHeaderValue(c.Hostname))
}
```

**Priorita**: 🔥 CRITICAL - Security
**Effort**: 30 minút

---

### 13. Race condition na rtStopCh

**Problém**: TOCTOU bug pri kopírovaní `rtStopCh` z mutexu.

**Súbor**: `internal/agent/agent.go:376-378, 366-369`

**Scenár**:
```
1. rtStreamingLoop: Lock(), stopCh := a.rtStopCh, Unlock()
2. stopRTStreaming: close(a.rtStopCh); a.rtStopCh = nil
3. rtStreamingLoop používa stale stopCh
4. startRTStreaming: a.rtStopCh = make(chan struct{}) <- NEW channel
5. Stará goroutine čaká na STARÝ channel -> LEAK
```

**Fix**: Držať mutex pri čítaní
```go
func (a *Agent) rtStreamingLoop() {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            a.rtMu.Lock()
            if !a.rtActive {
                a.rtMu.Unlock()
                return  // Exit early
            }

            // Všetko pod mutexom
            if time.Since(a.lastTickTime) > rtTimeout {
                a.rtActive = false
                a.rtMu.Unlock()
                return
            }
            a.rtMu.Unlock()

            // Send metrics (mimo mutexu)
            // ...

        case <-func() chan struct{} {
            a.rtMu.Lock()
            defer a.rtMu.Unlock()
            return a.rtStopCh
        }():
            return
        }
    }
}
```

**Priorita**: 🟠 HIGH - Race condition
**Effort**: 3 hodiny

---

### 14. Swallowed parse errors v CPU metrics

**Problém**: 10x `strconv.ParseUint()` errors sú ignorované bez logu.

**Súbor**: `metrics/cpu.go:95-122`

**Dôsledok**: Ak je `/proc/stat` corrupted, vracia nuly bez upozornenia.

**Fix**:
```go
func parseCPUFields(fields []string) (*cpuStat, error) {
    stat := &cpuStat{}
    var err error

    if len(fields) > 1 {
        if stat.user, err = strconv.ParseUint(fields[1], 10, 64); err != nil {
            return nil, fmt.Errorf("invalid user field: %w", err)
        }
    }
    // ... pre všetky fields

    return stat, nil
}

// V readAllCPUStats()
stat, err := parseCPUFields(fields)
if err != nil {
    logger.Warn("failed to parse CPU stats", "error", err, "line", line)
    continue
}
```

**Priorita**: 🟠 HIGH - Silent failures
**Effort**: 1 hodina

---

## 🟠 HIGH Priority - Nové architektúrne problémy

### 15. Busy-wait loop v handleWebSocketMessages

**Problém**: 100ms sleep loop keď `wsClient == nil`.

**Súbor**: `internal/agent/agent.go:286-289`

**Dopad**: Zbytočný CPU spinning počas reconnection.

**Fix**: Signalizovať cez channel
```go
// V Agent struct
wsReadyCh chan struct{}

// V connectWebSocket()
a.wsClient = wsClient
close(a.wsReadyCh)  // Signal ready
a.wsReadyCh = make(chan struct{})

// V handleWebSocketMessages()
for {
    select {
    case <-ctx.Done():
        return
    case <-a.wsReadyCh:
        // wsClient je ready
    }

    if a.wsClient == nil {
        continue
    }
    // ... ReceiveMessage
}
```

**Priorita**: 🟠 HIGH - Performance
**Effort**: 2 hodiny

---

### 16. Batch overflow logic zahodí najnovšie dáta

**Problém**: Pri overflow sa zahadzujú **oldest** entries, ale append logika pridáva retry batch PRED nové dáta.

**Súbor**: `internal/agent/agent.go:609-615`

**Kód**:
```go
combined := append(batchToSend, a.batch...)  // Retry + New
if len(combined) > maxBatchSize {
    dropped := len(combined) - maxBatchSize
    combined = combined[dropped:]  // ❌ Zahodí RETRY (najstaršie)
}
```

**Problém**: Zahodí sa retry batch namiesto nových dát!

**Fix**: Opačné poradie - drop oldest FROM retry batch
```go
combined := append(batchToSend, a.batch...)
if len(combined) > maxBatchSize {
    // Drop from beginning (oldest retry data)
    dropped := len(combined) - maxBatchSize
    logger.Warn("batch overflow, dropping oldest retry data", "dropped", dropped)
    combined = combined[dropped:]
}
a.batch = combined
```

Alebo lepšie: **Keep newest**
```go
// Keep newest maxBatchSize entries
if len(combined) > maxBatchSize {
    dropped := len(combined) - maxBatchSize
    logger.Warn("batch overflow, dropping oldest", "dropped", dropped)
    // Drop from beginning
    a.batch = combined[dropped:]
} else {
    a.batch = combined
}
```

**Priorita**: 🟠 HIGH - Data loss
**Effort**: 1 hodina

---

### 17. Global state v metrics package

**Problém**: `prevCPUStat`, `prevProcessStats`, `processCache` sú global variables.

**Súbory**:
- `metrics/cpu.go:26-32`
- `metrics/io.go:21-25`
- `metrics/common.go:19-24`

**Dôsledky**:
- Nemožné testovať paralelne
- Nemožné mať viacero metricov instancií
- Skrytá závislosť na `SetMetricInterval()`

**Fix**: Encapsulovať do `MetricsCollector` struct
```go
type MetricsCollector struct {
    // CPU state
    prevCPUStat      *cpuStat
    prevCoreStats    map[int]*cpuStat
    prevProcessStats map[int]processCPUStat
    statsMutex       sync.RWMutex

    // IO state
    prevProcessIOStats map[int]processIO
    ioStatsMutex       sync.RWMutex

    // Common
    processCache     []Process
    processCacheTime time.Time
    processMutex     sync.RWMutex

    metricInterval   int64
    numCPUCores      int
}

func NewMetricsCollector(metricInterval int64) *MetricsCollector {
    return &MetricsCollector{
        prevCoreStats:      make(map[int]*cpuStat),
        prevProcessStats:   make(map[int]processCPUStat),
        prevProcessIOStats: make(map[int]processIO),
        metricInterval:     metricInterval,
        numCPUCores:        runtime.NumCPU(),
    }
}

func (m *MetricsCollector) GatherCPUMetrics() (*internal.CPUMetrics, error) {
    // ... use m.prevCPUStat instead of global
}
```

**Benefit**:
- ✅ Testovateľné
- ✅ Thread-safe multiple instances
- ✅ Jasné dependencies

**Priorita**: 🟠 HIGH - Architecture
**Effort**: 6 hodín

---

### 18. God Object - Agent refactor

**Problém**: `Agent` struct má 15+ zodpovedností.

**Súbor**: `internal/agent/agent.go:31-77`

**Návrh**: Split do sub-managers
```go
type Agent struct {
    cfg          *config.Config

    // Sub-managers
    wsManager    *WebSocketManager
    batchManager *BatchManager
    rtStreamer   *RTStreamer
    collector    *MetricsCollector
    healthSrv    *HealthServer

    // Coordination
    ctx          context.Context
    cancel       context.CancelFunc
}

type WebSocketManager struct {
    client       *httpservices.WSClient
    url          string
    apiKey       string
    serverToken  string

    reconnectMu  sync.Mutex
    retryCount   int
    retryDelay   time.Duration
    connected    atomic.Bool
}

type BatchManager struct {
    httpClient   *httpservices.Client
    batch        []MetricEntry
    batchMu      sync.Mutex
    maxSize      int
    interval     time.Duration
}

type RTStreamer struct {
    active       bool
    activeMu     sync.RWMutex
    stopCh       chan struct{}
    lastTick     time.Time
    tickMu       sync.RWMutex
}
```

**Benefit**:
- ✅ Jednoduchšie testovanie
- ✅ Jasnejšie zodpovednosti
- ✅ Lepšia synchronizácia

**Priorita**: 🟡 MEDIUM - Long-term improvement
**Effort**: 12 hodín

---

## 🟡 MEDIUM Priority - Robustnosť a kvalita

### 19. Silent process scan failures

**Problém**: Chyby pri čítaní `/proc/[pid]/*` sú ignorované bez debug logu.

**Súbor**: `metrics/cpu.go:232-236`

**Fix**:
```go
for _, proc := range processes {
    cpuUsage, err := getProcessCPUUsage(proc.PID)
    if err != nil {
        // Rozlíšiť typy chýb
        if errors.Is(err, os.ErrPermission) {
            logger.Debug("permission denied for process", "pid", proc.PID, "name", proc.Name)
        } else if !errors.Is(err, os.ErrNotExist) {
            // Process died - OK, ale iné chyby log
            logger.Warn("failed to get CPU for process", "pid", proc.PID, "error", err)
        }
        continue
    }
    // ...
}
```

**Priorita**: 🟡 MEDIUM - Observability
**Effort**: 2 hodiny

---

### 20. Config validation vracia len prvú chybu

**Problém**: Multi-field validation stops na prvej chybe.

**Súbor**: `config/config.go:122-142`

**Fix**: Aggregate errors
```go
func (c *Config) Validate() error {
    var errs []string

    if c.ServiceHost == "" {
        errs = append(errs, "serviceHost is required")
    }
    if c.ApiKey == "" {
        errs = append(errs, "apiKey is required")
    }
    if c.ServerToken == "" {
        errs = append(errs, "serverToken is required")
    }
    if c.MetricInterval < minMetricIntervalMs {
        errs = append(errs, fmt.Sprintf("metricInterval must be >= %dms", minMetricIntervalMs))
    }
    // ... atď

    if len(errs) > 0 {
        return fmt.Errorf("validation errors:\n  - %s", strings.Join(errs, "\n  - "))
    }
    return nil
}
```

**Priorita**: 🟡 MEDIUM - UX improvement
**Effort**: 1 hodina

---

### 21. Health server shutdown môže blokovať

**Problém**: Synchronný `Shutdown()` s 5s timeout môže zablokovať graceful shutdown.

**Súbor**: `internal/agent/agent.go:178-182`

**Fix**: Asynchrónne s logging
```go
if a.healthServer != nil {
    go func() {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()

        if err := a.healthServer.Shutdown(ctx); err != nil {
            logger.Error("health server shutdown error", "error", err)
        } else {
            logger.Info("health server stopped")
        }
    }()
}
```

**Priorita**: 🟡 MEDIUM - Graceful shutdown
**Effort**: 30 minút

---

### 22. Error handling v JSON health response

**Problém**: JSON encoding error je ignorovaná.

**Súbor**: `internal/agent/agent.go:654`

**Fix**:
```go
if err := json.NewEncoder(w).Encode(response); err != nil {
    logger.Error("failed to encode health response", "error", err)
    w.WriteHeader(http.StatusInternalServerError)
}
```

**Priorita**: 🟢 LOW - Edge case
**Effort**: 15 minút

---

### 23. Cross-platform config paths

**Problém**: `/etc/simplemon.conf` nefunguje na Windows.

**Súbor**: `config/config.go:50-51`

**Fix**:
```go
var globalConfigPath string
if runtime.GOOS == "windows" {
    globalConfigPath = filepath.Join(os.Getenv("ProgramData"), "simplemon", "simplemon.conf")
} else {
    globalConfigPath = "/etc/simplemon.conf"
}
```

**Priorita**: 🟢 LOW - Cross-platform
**Effort**: 1 hodina

---

## 💡 Vylepšenia (Nice-to-have)

### 24. Efektivita zberu metrík - Rozdielne intervaly

**Implementácia**: Optimalizácia A z analýzy

**Config rozšírenie**:
```go
type Config struct {
    MetricInterval int `json:"metricInterval"` // CPU/RAM - 1000ms
    DiskInterval   int `json:"diskInterval"`   // Disk - 10000ms (default)
    IoInterval     int `json:"ioInterval"`     // I/O - 5000ms (default)
}
```

**Agent refactor**:
```go
func (a *Agent) Run(ctx context.Context) error {
    metricTicker := time.NewTicker(a.metricInterval)  // 1s
    diskTicker := time.NewTicker(a.diskInterval)      // 10s
    ioTicker := time.NewTicker(a.ioInterval)          // 5s
    batchTicker := time.NewTicker(a.batchInterval)

    for {
        select {
        case <-metricTicker.C:
            a.collectCPUMemory()
        case <-diskTicker.C:
            a.collectDisk()
        case <-ioTicker.C:
            a.collectIO()
        case <-batchTicker.C:
            a.sendBatchMetrics()
        case <-ctx.Done():
            return a.shutdown()
        }
    }
}
```

**Benefit**: 60-80% menej /proc reads

**Priorita**: 💡 NICE-TO-HAVE
**Effort**: 4-6 hodín

---

### 25. Verbose logging optimalizácia

**Problém**: `json.Marshal()` každú sekundu v verbose mode.

**Súbor**: `internal/agent/agent.go:560-564`

**Fix**:
```go
if a.cfg.Verbose {
    logger.Debug("gathered metrics",
        "cpu_pct", entry.CPU.OverallPercent,
        "mem_pct", entry.Memory.UsedRelative,
        "disk_count", len(entry.Disks),
        "top_cpu_proc", entry.CPU.Processes[0].Name,
    )
}
```

**Benefit**: Žiadne JSON marshalling overhead

**Priorita**: 💡 NICE-TO-HAVE
**Effort**: 30 minút

---

### 26. Adaptive collection - Process scan len v RT mode

**Návrh**:
```go
func (a *Agent) collectMetrics() {
    a.rtMu.RLock()
    needsProcessDetails := a.rtActive
    a.rtMu.RUnlock()

    if needsProcessDetails {
        // Full collection s per-process details
        cpuMetrics, _ = metrics.GatherCPUMetrics()
    } else {
        // Len system-level aggregates (bez process scan)
        cpuMetrics, _ = metrics.GatherCPUSystemOnly()
    }
}
```

**Benefit**: Úspora CPU keď nie je RT stream aktívny

**Priorita**: 💡 NICE-TO-HAVE
**Effort**: 4 hodiny

---

### 27. Local spool pre failed batches

Disk-backed buffer pre dlhé API outages.

**Priorita**: 💡 NICE-TO-HAVE
**Effort**: 8 hodín

---

### 15. Telemetry counters

Expose counters for batch retries, drops, WS reconnects.

**Priorita**: 💡 NICE-TO-HAVE
**Effort**: 4 hodiny

---

### 16. Performance profiling

Profiling + /proc optimizácie.

**Priorita**: 💡 NICE-TO-HAVE
**Effort**: 6 hodín

---

### 17. Nové metriky

Network, load average, process count, sensors.

**Priorita**: 💡 NICE-TO-HAVE
**Effort**: 8-12 hodín

---

### 18. Cleanup interval optimization

Move cleanup to time-based ticker.

**Priorita**: 💡 NICE-TO-HAVE
**Effort**: 2 hodiny

---

## 📊 Sumár všetkých problémov

| Priorita | Počet | Effort | Kategória |
|----------|-------|--------|-----------|
| 🔥 CRITICAL | 5 | 6h | Goroutine leaks, security, panics |
| 🟠 HIGH | 9 | 23.5h | Race conditions, data loss, architecture |
| 🟡 MEDIUM | 5 | 7h | Robustnosť, UX, graceful shutdown |
| 🟢 LOW | 3 | 2.25h | Edge cases, cross-platform |
| 💡 NICE-TO-HAVE | 6 | 30-36h | Performance, features |
| **TOTAL** | **28** | **68.75-74.75h** | |

### Rozdelenie problémov podľa typu:

**Concurrency issues** (8): #2, #4, #5, #9, #10, #13, #15, #16
**Security issues** (3): #1, #11, #12
**Data integrity** (4): #3, #8, #14, #16
**Architecture** (3): #17, #18, #24
**Error handling** (4): #14, #19, #20, #22
**Performance** (3): #15, #24, #25, #26

---

## 📅 Implementačný plán

### Sprint 0 - CRITICAL Security & Leaks (2 dni) 🔥

**Priorita**: Tieto fixe MUSIA byť v produkcii ASAP

**Deň 1**:
- [ ] #11 Type assertion panic (15min)
- [ ] #12 Header injection (30min)
- [ ] #10 Goroutine leak v handleWebSocketMessages (2h)
- [ ] #1 Secret logging (2h) - už čiastočne hotové

**Deň 2**:
- [ ] #13 Race condition na rtStopCh (3h)
- [ ] Testing critical fixov
- [ ] **Emergency release 1.0.1** 🚨

---

### Sprint 1 - HIGH Priority Fixe (1 týždeň)

**Deň 1-2**:
- [ ] #2 Concurrent batch send - worker pattern (4h)
- [ ] #5 Missing mutex pre prevCPUStat (30min)
- [ ] #14 Swallowed parse errors v CPU (1h)
- [ ] #15 Busy-wait loop fix (2h)

**Deň 3-4**:
- [ ] #3 Drop batch >500 fix (3h)
- [ ] #16 Batch overflow logic (1h)
- [ ] #6 WebSocket reconnect context (3h)

**Deň 5**:
- [ ] #7 Strict WS message parsing (2h)
- [ ] Testing všetkých HIGH fixov
- [ ] Code review
- [ ] **Release 1.1.0**

---

### Sprint 2 - Architecture Refactor (2 týždne)

**Týždeň 1 - Metrics Collector Refactor**:
- [ ] #17 Global state → MetricsCollector struct (6h)
- [ ] Migrácia GatherCPUMetrics (2h)
- [ ] Migrácia GatherMemoryMetrics (1h)
- [ ] Migrácia GatherIOMetrics (1h)
- [ ] Unit testy pre MetricsCollector (4h)
- [ ] Integration s Agent (2h)

**Týždeň 2 - Agent Decomposition**:
- [ ] #18 Split Agent → sub-managers (12h)
  - WebSocketManager (3h)
  - BatchManager (3h)
  - RTStreamer (2h)
  - HealthServer (2h)
  - Integration (2h)
- [ ] Unit testy pre každý manager (6h)
- [ ] **Release 2.0.0** (major refactor)

---

### Sprint 3 - MEDIUM Priority & Polish (1 týždeň)

**Deň 1-2**:
- [ ] #8 All-or-nothing → partial metrics (4h)
- [ ] #9 Nil pointer v RT streaming (1h)
- [ ] #19 Silent process scan failures (2h)

**Deň 3-4**:
- [ ] #20 Config validation aggregate errors (1h)
- [ ] #21 Health server shutdown async (30min)
- [ ] #22 JSON health response error (15min)
- [ ] #23 Cross-platform config paths (1h)

**Deň 5**:
- [ ] Integration testing všetkých MEDIUM fixov
- [ ] **Release 2.1.0**

---

### Sprint 4 - Testing & Coverage (1 týždeň)

**Deň 1-2**:
- [ ] Unit testy pre memory metrics (4h)
- [ ] Unit testy pre disk metrics (2h)
- [ ] Unit testy pre io metrics (4h)

**Deň 3-4**:
- [ ] Integration testy pre WS lifecycle (4h)
- [ ] Integration testy pre batch retry (3h)
- [ ] Testy pre graceful shutdown (2h)

**Deň 5**:
- [ ] Code coverage report (target: 75%+)
- [ ] Fix coverage gaps
- [ ] **Release 2.2.0**

---

### Backlog - Performance & Features

**Performance Optimizations** (podľa potreby):
- [ ] #24 Rozdielne intervaly pre metriky (4-6h) ⭐ Vysoký impact
- [ ] #25 Verbose logging optimalizácia (30min)
- [ ] #26 Adaptive collection - RT mode only (4h)
- [ ] Performance profiling session (6h)

**New Features** (low priority):
- [ ] #27 Local spool pre failed batches (8h)
- [ ] #28 Telemetry counters (4h)
- [ ] #29 Nové metriky (network, load avg) (8-12h)
- [ ] #30 TLS verification control (2h)
- [ ] #31 Prometheus metrics export (6h)

---

## 🎯 Roadmap Timeline

```
Sprint 0 (2 dni)  → v1.0.1 🚨 EMERGENCY
   ↓
Sprint 1 (5 dni)  → v1.1.0 ✅ Stability
   ↓
Sprint 2 (10 dni) → v2.0.0 🏗️ Architecture Refactor
   ↓
Sprint 3 (5 dni)  → v2.1.0 💎 Polish
   ↓
Sprint 4 (5 dni)  → v2.2.0 🧪 Testing
   ↓
Backlog           → v3.0.0+ 🚀 Performance & Features
```

**Total effort pre production-ready**: ~27 dní (135 hodín)

---

## 🚨 Top Priority Recommendations

### Immediate (Sprint 0) - DO NOW:
1. **#11** Type assertion panic - 15min ⚡
2. **#12** Header injection - 30min ⚡
3. **#10** Goroutine leak - 2h
4. **#13** rtStopCh race - 3h

**Why**: Security vulnerabilities + resource leaks = **production outages**

### Short-term (Sprint 1):
5. **#2** Concurrent batch send - 4h
6. **#14** Swallowed errors - 1h
7. **#15** Busy-wait - 2h
8. **#16** Batch overflow - 1h

**Why**: Data integrity + CPU waste = **reliability issues**

### Medium-term (Sprint 2-3):
9. **#17** Global state refactor - 6h
10. **#8** Partial metrics - 4h

**Why**: Testability + data gaps = **maintainability**

---

## 📊 Final Statistics

| Metriky | Hodnota |
|---------|---------|
| **Celkový počet issues** | 28 |
| **Critical issues** | 5 |
| **Lines of code analyzed** | ~2256 |
| **Test coverage (current)** | ~15% |
| **Test coverage (target)** | 75%+ |
| **Estimated total effort** | 68-75h |
| **Time to production-ready** | ~27 pracovných dní |

---

## ✅ Definition of Done

Každá položka je "Done" keď:

1. ✅ Kód je implementovaný podľa návrhu
2. ✅ Unit testy pokrývajú happy path + edge cases
3. ✅ Code review passed (ak team)
4. ✅ Manual testing OK
5. ✅ Documentation updated
6. ✅ No new linter warnings
7. ✅ Merged to main
