# SimpleMon Agent - Code Audit Report

**Date:** 2026-02-05
**Scope:** Full security & code quality audit pre open-source release
**Files reviewed:** All `.go` files, `go.mod`, `.gitignore`, git history

---

## Executive Summary

Agent je funkčne solidný, ale pred open-source release treba riešiť **duplikovaný kód** (starý global path vs. nový Collector) a niekoľko security vylepšení. Žiadne hardcoded credentials, žiadne kritické zraniteľnosti.

| Kategória | CRITICAL | HIGH | MEDIUM | LOW |
|-----------|----------|------|--------|-----|
| Security  | 0        | 2    | 3      | 2   |
| Code Quality | 1    | 3    | 4      | 3   |

---

## SECURITY

### [HIGH] S1 — Chýba explicitná TLS konfigurácia

**Súbory:** `httpservices/httpservices.go:57-64`, `httpservices/wsclient.go:44`

HTTP client aj WebSocket dialer nesetujú `TLSClientConfig`. Go síce defaultne validuje certifikáty a používa TLS 1.2+, ale pre produkčný open-source agent je lepšie byť explicitný:

```go
// httpservices.go - Transport
Transport: &http.Transport{
    TLSClientConfig: &tls.Config{
        MinVersion: tls.VersionTLS12,
    },
    // ...existing settings...
}

// wsclient.go - Dialer
dialer := &websocket.Dialer{
    TLSClientConfig: &tls.Config{
        MinVersion: tls.VersionTLS12,
    },
}
```

**Prečo:** Explicitná konfigurácia zabraňuje budúcim regresiám a ukazuje zámer.

---

### [HIGH] S2 — Health server bez timeoutov

**Súbor:** `internal/agent/agent.go:698-701`

```go
a.healthServer = &http.Server{
    Addr:    fmt.Sprintf(":%d", a.cfg.HealthPort),
    Handler: mux,
    // Chýba: ReadTimeout, WriteTimeout, IdleTimeout
}
```

**Riziko:** Slowloris DoS — pomalí klienti držia spojenia donekonečna.

**Fix:**
```go
a.healthServer = &http.Server{
    Addr:         fmt.Sprintf(":%d", a.cfg.HealthPort),
    Handler:      mux,
    ReadTimeout:  15 * time.Second,
    WriteTimeout: 15 * time.Second,
    IdleTimeout:  60 * time.Second,
}
```

---

### [MEDIUM] S3 — Health endpoint leakuje lastError

**Súbor:** `internal/agent/agent.go:662-686`

Health endpoint vracia `lastError` field, ktorý môže obsahovať interné chybové hlášky servera (URL, IP adresy, error stacky).

**Fix:** Buď odstrániť `lastError` z JSON odpovede, alebo ho sanitizovať (len "connection_error", "send_error" atď.).

---

### [MEDIUM] S4 — API key na command line

**Súbor:** `config/config.go:59`

```go
apiKey := flag.String("apiKey", "", "API Key")
```

Command-line argumenty sú viditeľné cez `ps aux` a `/proc/PID/cmdline`.

**Fix:** Preferovať env variables alebo config file. Pridať upozornenie do dokumentácie. Prípadne pridať `SIMPLEMON_API_KEY` env var support.

---

### [MEDIUM] S5 — WebSocket message nevaliduje vstup

**Súbor:** `internal/agent/agent.go:338-363`

Server posiela JSON správy (`START`, `STOP`, `TICK`). Validácia je minimálna — len parsuje `action` field. Ak by server bol kompromitovaný, neexistuje ochrana proti neočakávaným fieldsom.

**Riziko:** Nízke (agent len reaguje na 3 known actions), ale pre prezentačný kód by mal byť vstup striktnejší.

---

### [LOW] S6 — Chýba rate limiting na health endpoint

Health endpoint nemá rate limiting. Na lokálnom serveri to nie je veľký problém, ale pri exposnutí na sieť by mohol byť zneužitý.

---

### [LOW] S7 — gopsutil závislosť je zastaralá

**Súbor:** `go.mod:7`

```
github.com/shirou/gopsutil v3.21.11+incompatible
```

Verzia z 2021. Aktuálna je v4.x. Navyše sa zdá, že `gopsutil` je reálne použitý **len v `cmd/test/main.go`** — test utility. Ak sa nepoužíva v produkcii, odstrániť zo závislostí.

---

### Pozitívne security findings

- `.gitignore` správne ignoruje `simplemon.conf` a `.env`
- Žiadne hardcoded credentials v kóde (len v test files, čo je OK)
- `sanitizeHeaderValue()` chráni proti CRLF injection
- `maskSecret()` maskuje API key v logoch
- Proper mutex usage pre shared state
- Graceful shutdown cez SIGINT/SIGTERM
- Gzip kompresia metrík
- Žiadne SQL, žiadne exec, žiadne file path traversal riziká

---

## CODE QUALITY

### [CRITICAL] Q1 — Duplikovaný metrics kód (global vs Collector)

Toto je najväčší problém celého projektu.

**Problém:** Existujú **dva paralelné implementácie** zbierania metrík:

| Global funkcie (starý kód) | Collector metódy (nový kód) |
|---|---|
| `cpu.go`: `GatherCPUMetrics()`, `getProcessCPUUsage()`, `cleanupStaleProcessStats()` | `collector.go`: `GatherCPU()`, `getProcessCPUUsage()`, `cleanupStaleCPUStats()` |
| `memory.go`: `GatherMemoryMetrics()`, `getProcessMemoryUsage()` | `collector.go`: `GatherMemory()`, `getProcessMemoryUsage()` |
| `io.go`: `GatherIOMetrics()`, `cleanupStaleIOStats()` | `collector.go`: `GatherIO()`, `cleanupStaleIOStatsLocked()` |
| `common.go`: `GetProcesses()` (global cache) | `collector.go`: `GetProcesses()` (instance cache) |

**Globálny stav v `cpu.go` (riadky 30-38):**
```go
var (
    prevCPUStat      *cpuStat
    prevCoreStats    = make(map[int]*cpuStat)
    prevProcessStats = make(map[int]processCPUStat)
    statsMutex       sync.RWMutex
    metricInterval   int64
    numCPUCores      = runtime.NumCPU()
    cleanupCounter   int
)
```

**Tiež v `io.go` (riadky 26-30):**
```go
var (
    prevProcessIOStats = make(map[int]processIO)
    ioStatsMutex       sync.RWMutex
    ioCleanupCounter   int
)
```

**Tiež v `common.go` (riadky 23-28):**
```go
var (
    processCache     []Process
    processCacheTime time.Time
    processMutex     sync.RWMutex
    cacheDuration    = 900 * time.Millisecond
)
```

**Tiež v `memory.go` (riadok 25):**
```go
var pageSize = uint64(syscall.Getpagesize())
```

**Tiež v `collector.go` (riadok 512):**
```go
var defaultCollector = NewCollector(1000)
```

Agent reálne používa **len Collector** (cez `agent.go:121`). Globálne funkcie sú **dead code**.

**Fix:** Zmazať všetky globálne funkcie a global state z `cpu.go`, `memory.go`, `io.go`, `common.go`. Ponechať len:
- Typy (`cpuStat`, `processCPUStat`, `memInfo`, `processIO`, `Process`)
- Helper funkcie (`readAllCPUStats`, `parseCPUFields`, `calculateCPUUsage`, `readMemInfo`, `getProcessIO`, `getProcessName`, `GetBaseProcessName`, `isPhysicalFS`, `GatherDiskMetrics`)
- Collector v `collector.go`

Zmazať: `GatherCPUMetrics()`, `GatherMemoryMetrics()`, `GatherIOMetrics()`, `GetProcesses()` (globálna), `getProcessCPUUsage()` (globálna), `getProcessMemoryUsage()` (globálna), `cleanupStaleProcessStats()` (globálna), `cleanupStaleIOStats()` (globálna), `SetMetricInterval()`, `defaultCollector`, a všetok global state (`prevCPUStat`, `prevCoreStats`, `prevProcessStats`, `statsMutex`, `metricInterval`, `cleanupCounter`, `prevProcessIOStats`, `ioStatsMutex`, `ioCleanupCounter`, `processCache`, `processCacheTime`, `processMutex`, `pageSize` v memory.go).

V `agent.go:124` zmazať:
```go
metrics.SetMetricInterval(int64(cfg.MetricInterval))
```

---

### [HIGH] Q2 — `cmd/test/main.go` nepatrí do open-source release

**Súbor:** `cmd/test/main.go`

Interný test utility s komentármi v slovenčine a priamym použitím `gopsutil`. Pre open-source:
- Buď zmazať celý `cmd/test/`
- Alebo prepísať ako ukážkový standalone tool

Ak sa zmaže, `gopsutil` závislosť sa dá odstrániť z `go.mod`.

---

### [HIGH] Q3 — Komentáre v slovenčine v `cmd/test/main.go`

```go
// Získaj CPU percentá
// Získaj pamäťové metriky
// Získaj top 10 procesov podľa CPU využitia
// Získaj IO metriky
```

Pre open-source musia byť **všetky komentáre v angličtine**.

---

### [HIGH] Q4 — Chýba `go vet` / `staticcheck` čistota

Neoverené na CI. Pre open-source by mal byť:
- `go vet ./...`
- `staticcheck ./...`
- Prípadne `golangci-lint`

---

### [MEDIUM] Q5 — Nekonzistentný error wrapping

Niektoré miesta používajú `%v` (stráca error chain), iné `%w` (správne):

| Súbor | Pattern |
|-------|---------|
| `httpservices.go:80,84,88` | `%v` — chýba wrapping |
| `wsclient.go:141,149,177` | `%w` — správne |

**Fix:** Všade kde sa vracia `fmt.Errorf` s pôvodným `err`, použiť `%w`.

---

### [MEDIUM] Q6 — Magic numbers roztrúsené mimo constants

| Hodnota | Miesto | Čo to je |
|---------|--------|----------|
| `900ms` | `common.go:27` | Process cache duration |
| `30s` | `agent.go:91` | RT timeout |
| `1000` | `agent.go:92` | Max batch size |
| `1` | `agent.go:93` | Batch trigger buffer |
| `100ms` | `agent.go:307` | WS client poll interval |
| `5s` | `agent.go:89` | Default reconnect delay |
| `5` | `agent.go:90` | Max retries |
| `1s` | `agent.go:402` | RT streaming tick |
| `100` | `collector.go:193` | USER_HZ assumption |

Niektoré sú v `const` bloku v `agent.go`, ale `cacheDuration` a `USER_HZ` nie.

**Fix:** Presunúť do `metrics/constants.go` resp. `agent.go` const bloku.

---

### [MEDIUM] Q7 — Tiché ignorovanie parse errorov

**Súbor:** `cpu.go:100-128` (funkcia `parseCPUFields`)

```go
stat.user, _ = strconv.ParseUint(fields[1], 10, 64)
```

Všetky parse errory sú tichpo ignorované. Ak `/proc/stat` vráti neočakávané dáta, dostaneme nulové hodnoty bez warningov.

**Riziko:** Data quality — pri bugoch ťažko diagnostikovateľné.

**Fix:** Minimálne logovať warning ak parse zlyhá.

---

### [MEDIUM] Q8 — `lastMeasurement` prístup bez mutexu

**Súbor:** `agent.go:469,580`

```go
deltaTime := currentTime.Sub(a.lastMeasurement).Seconds()  // read
// ...
a.lastMeasurement = currentTime  // write, outside mutex
```

Technicky data race. V praxi safe (len jeden goroutine volá `collectMetrics`), ale `go test -race` by to mohol chytiť.

---

### [LOW] Q9 — Redundantné nil receiver checks

**Súbor:** `wsclient.go:124,157`

```go
func (c *WSClient) SendMetrics(metrics interface{}) error {
    if c == nil {
        return fmt.Errorf("websocket client is nil")
    }
```

Ak `c` je nil, Go panikne pri prístupe ku `c.closed` pred tým, než sa dostane k tomuto checku. Tieto kontroly sú mŕtvy kód.

---

### [LOW] Q10 — Nepoužitý `defaultCollector`

**Súbor:** `collector.go:512`

```go
var defaultCollector = NewCollector(1000)
```

Nikde sa nepoužíva. Zmazať.

---

### [LOW] Q11 — Test coverage je slabá

Existujú testy pre:
- `cpu_test.go` — unit testy parsingu (dobré)
- `httpservices_test.go` — gzip encoding + error response (dobré)
- `agent_test.go` — RT streaming, messages, basic lifecycle (dobré)

Chýbajú testy pre:
- `config.go` — žiadne testy validácie a loadovania
- `memory.go` / `io.go` / `disk.go` — žiadne unit testy
- `wsclient.go` — žiadne testy
- `logger.go` — žiadne testy

Pre open-source by minimálne `config.go` validation mal mať testy.

---

## ŠTRUKTÚRA PROJEKTU

### Aktuálna štruktúra

```
agent/
├── cmd/
│   ├── simplemon-agent/main.go    ← Entry point (OK)
│   └── test/main.go               ← Test utility (ZMAZAŤ)
├── config/
│   └── config.go                  ← Config loading (OK)
├── httpservices/
│   ├── httpservices.go            ← HTTP client (OK)
│   ├── httpservices_test.go       ← Tests (OK)
│   └── wsclient.go               ← WebSocket client (OK)
├── internal/
│   ├── agent/
│   │   ├── agent.go              ← Main orchestrator (OK)
│   │   └── agent_test.go         ← Tests (OK)
│   ├── logger/
│   │   └── logger.go             ← Logger wrapper (OK)
│   └── types.go                  ← Shared types (OK)
├── metrics/
│   ├── collector.go              ← NEW: Stateful collector (OK, keeper)
│   ├── common.go                 ← Process utils + DEAD global state
│   ├── constants.go              ← Constants (OK, rozšíriť)
│   ├── cpu.go                    ← CPU parsing + DEAD global functions
│   ├── cpu_test.go               ← Tests (OK)
│   ├── disk.go                   ← Disk metrics (OK)
│   ├── io.go                     ← IO parsing + DEAD global functions
│   └── memory.go                 ← Memory parsing + DEAD global functions
├── go.mod
├── go.sum
└── .gitignore
```

---

## ZÁVISLOSTI

| Package | Verzia | Stav | Akcia |
|---------|--------|------|-------|
| `gorilla/websocket` | v1.5.3 | OK | - |
| `shirou/gopsutil` | v3.21.11 | Zastaralá (2021) | Odstrániť (len v cmd/test) |
| `golang.org/x/sys` | v0.24.0 | OK | - |
| `go-ole/go-ole` | v1.2.6 | Indirect (gopsutil) | Odíde s gopsutil |
| `tklauser/*` | - | Indirect (gopsutil) | Odíde s gopsutil |
| `yusufpapurcu/wmi` | v1.2.4 | Indirect (gopsutil) | Odíde s gopsutil |
| `stretchr/testify` | v1.9.0 | Indirect | Overiť či je skutočne potrebný |

Po odstránení `cmd/test/` a `gopsutil` sa indirect závislosti zredukujú z 5 na 0.

---

## AKČNÝ PLÁN

### Priorita 1 — Blocker pre release

| # | Akcia | Odhad |
|---|-------|-------|
| 1 | Zmazať globálny state a duplicitné funkcie z `cpu.go`, `memory.go`, `io.go`, `common.go` | 1-2h |
| 2 | Zmazať `cmd/test/` a `gopsutil` závislosť | 15min |
| 3 | Zmazať `defaultCollector` a `SetMetricInterval()` volanie v `agent.go:124` | 5min |
| 4 | Pridať HTTP server timeouty na health endpoint | 5min |

### Priorita 2 — Dôležité pre kvalitu

| # | Akcia | Odhad |
|---|-------|-------|
| 5 | Pridať explicitnú TLS config (`MinVersion: tls.VersionTLS12`) | 10min |
| 6 | Opraviť error wrapping `%v` → `%w` v `httpservices.go` | 5min |
| 7 | Odstrániť/sanitizovať `lastError` z health endpointu | 10min |
| 8 | Presunúť roztrúsené magic numbers do constants | 15min |
| 9 | Pridať testy pre `config.go` Validate() | 30min |

### Priorita 3 — Nice to have

| # | Akcia | Odhad |
|---|-------|-------|
| 10 | Zmazať nil receiver checks z `wsclient.go` | 5min |
| 11 | Pridať env var support pre API key (`SIMPLEMON_API_KEY`) | 15min |
| 12 | Pridať `golangci-lint` do CI | 20min |
| 13 | `go mod tidy` po odstránení gopsutil | 2min |

---

## ZÁVER

Kód je funkčne dobrý — concurrency je dobre zvládnutá, graceful shutdown funguje, metriky sa zbierajú efektívne priamym čítaním `/proc`. Hlavný problém je **nedokončený refactoring** (globálny vs. Collector pattern), ktorý zdvojnásobuje veľkosť `metrics/` package-u. Po vyčistení duplicít a security vylepšeniach bude kód pripravený na open-source.

**Odhadovaný celkový čas na Prioritu 1 + 2: ~3 hodiny.**
