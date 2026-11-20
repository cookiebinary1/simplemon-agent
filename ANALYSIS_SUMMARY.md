# Simplemon Agent - Code Review Summary

**Dátum**: 2025-12-27
**Analyzovaný kód**: ~2256 riadkov Go
**Verzia**: main branch (commit 1bf83bc)

---

## 🎯 Executive Summary

Simplemon Agent je **monitoring agent pre Linux** systémy, ktorý zbiera CPU, RAM, Disk a I/O metriky a odosiela ich cez WebSocket + HTTP do centrálneho servera.

### Overall Assessment: ⚠️ **NEEDS IMMEDIATE ATTENTION**

**Kód je funkčný**, ale obsahuje **5 kritických bezpečnostných/stability problémov** a **9 high-priority issues**, ktoré môžu viesť k:
- Production outages (goroutine leaks)
- Security vulnerabilities (header injection, panic DoS)
- Data loss (race conditions, batch overflow)
- Resource waste (busy-wait loops)

---

## 🔴 TOP 5 CRITICAL Issues (MUST FIX NOW)

### 1. 🚨 Goroutine Leak v WebSocket Handler
**File**: `internal/agent/agent.go:271-303`
**Impact**: Memory leak, eventual OOM crash
**Severity**: CRITICAL

WebSocket `ReceiveMessage()` blokuje na 60s read deadline. Keď príde SIGTERM, goroutine sa "zalepí" v blocking call a neuvoľní sa až do timeout.

**Fix**: Context-aware ReceiveMessage (2 hodiny)

---

### 2. 🔒 Header Injection Vulnerability
**File**: `httpservices/httpservices.go:74`
**Impact**: HTTP request smuggling
**Severity**: CRITICAL (Security)

`X-Hostname` header používa `os.Hostname()` bez sanitizácie. Malicious `/etc/hostname` s CRLF characters môže injektovať HTTP headers.

**Exploit**:
```bash
echo "evil.com\r\nX-Forwarded-For: 127.0.0.1" > /etc/hostname
```

**Fix**: Sanitize newlines (30 minút)

---

### 3. 💥 Type Assertion Panic
**File**: `internal/agent/agent.go:643`
**Impact**: DoS na health endpoint
**Severity**: CRITICAL

`atomic.Value` type assertion bez check:
```go
lastErr = v.(string)  // PANIC ak nie je string!
```

**Fix**: Checked type assertion (15 minút)

---

### 4. 🔄 Race Condition na rtStopCh
**File**: `internal/agent/agent.go:350, 366-369`
**Impact**: Goroutine leak, data race
**Severity**: CRITICAL

TOCTOU bug pri kopírovaní channel z mutex. Stará goroutine môže čakať na starý channel aj po novom `startRTStreaming()`.

**Fix**: Držať mutex pri select (3 hodiny)

---

### 5. ⚡ Concurrent Batch Send Race
**File**: `internal/agent/agent.go:160`
**Impact**: Data corruption, duplicates, loss
**Severity**: CRITICAL

```go
case <-batchTicker.C:
    go a.sendBatchMetrics()  // ❌ Nová goroutine každý tick!
```

Ak HTTP trvá 10s a batch interval je 5s, viacero sendov beží paralelne → race condition na `batch` slice.

**Fix**: Worker pattern s channel (4 hodiny)

---

## 🟠 HIGH Priority Issues (9 items)

| # | Problém | Impact | Effort |
|---|---------|--------|--------|
| 6 | WebSocket reconnect ignoruje context | Slow shutdown | 3h |
| 7 | Strict JSON matching | Fragility | 2h |
| 8 | All-or-nothing metrics | Data gaps | 4h |
| 9 | Nil pointer v RT streaming | Potential panic | 1h |
| 14 | Swallowed parse errors | Silent failures | 1h |
| 15 | Busy-wait loop (100ms sleep) | CPU waste | 2h |
| 16 | Batch overflow drops new data | Data loss | 1h |
| 17 | Global state v metrics | Untestable | 6h |
| 18 | God Object Agent | Tight coupling | 12h |

---

## ✅ Positive Findings

Kód má aj **veľa dobrých vecí**:

1. ✅ **Process caching** (900ms TTL) - Vynikajúca optimalizácia
2. ✅ **Paralelný zber metrík** - CPU/RAM/Disk/IO súčasne
3. ✅ **Buffer pooling** - Pre JSON encoding
4. ✅ **Gzip compression** - Pre HTTP batches
5. ✅ **Health endpoints** - `/health` a `/ready`
6. ✅ **Graceful shutdown** - Signal handling
7. ✅ **Structured logging** - Konzistentné použitie logger
8. ✅ **Config validation** - Základná validácia intervalov
9. ✅ **Mutex protection** - Väčšina concurrent access je chránená
10. ✅ **Konštanty extrahované** - `constants.go` pre magic numbers

---

## 📊 Code Quality Metrics

| Metrika | Current | Target | Gap |
|---------|---------|--------|-----|
| Test Coverage | ~15% | 75% | 60% |
| Cyclomatic Complexity | HIGH (Agent) | MEDIUM | Refactor needed |
| Global State | 8 vars | 0 | Encapsulate |
| Magic Numbers | ~12 | 0 | Extract |
| Code Duplication | Medium | Low | DRY refactor |

---

## 🎯 Recommended Action Plan

### Phase 1: EMERGENCY (2 dni) 🚨

**Goal**: Fix critical security + stability issues

```
Day 1:
- Fix #11 Type assertion panic (15min)
- Fix #12 Header injection (30min)
- Fix #10 Goroutine leak (2h)
- Fix #1 Secret logging (2h) - už čiastočne hotové

Day 2:
- Fix #13 rtStopCh race (3h)
- Testing
- Release v1.0.1
```

**Why urgent**: Tieto problémy môžu viesť k production outages alebo security breaches.

---

### Phase 2: Stability (5 dní) ✅

**Goal**: Fix data integrity + performance issues

```
Week 1:
- Concurrent batch send (#2) - 4h
- Parse error handling (#14) - 1h
- Busy-wait elimination (#15) - 2h
- Batch overflow logic (#16) - 1h
- WebSocket context (#6) - 3h
- JSON message parsing (#7) - 2h
- Release v1.1.0
```

---

### Phase 3: Architecture (10 dní) 🏗️

**Goal**: Refactor global state + decompose Agent

```
Week 1: MetricsCollector refactor (#17) - 16h
Week 2: Agent decomposition (#18) - 18h
Release v2.0.0
```

**Why important**: Umožní testovanie, paralelizáciu, maintenance.

---

### Phase 4: Polish (5 dní) 💎

**Goal**: Robustness improvements

```
- Partial metrics (#8) - 4h
- Silent failures (#19) - 2h
- Config validation (#20) - 1h
- Cross-platform (#23) - 1h
- Release v2.1.0
```

---

### Phase 5: Testing (5 dní) 🧪

**Goal**: 75%+ test coverage

```
- Unit tests (memory, disk, IO) - 10h
- Integration tests (WS, batch) - 9h
- Coverage report + gaps - 4h
- Release v2.2.0
```

---

## 💡 Performance Optimization Opportunities

### Already Optimized ✅
- Process cache (900ms TTL)
- Parallel metric collection
- Buffer pooling
- Single-pass CPU stats

### Can Be Improved 📈

1. **Rozdielne intervaly** (4-6h, HIGH IMPACT)
   - CPU/RAM: 1s (rýchlo sa menia)
   - Disk: 10s (pomaly sa mení)
   - I/O: 5s (stredne)
   - **Benefit**: 60-80% menej /proc reads

2. **Verbose logging** (30min)
   - Nelogovať celý JSON každú sekundu
   - Len kľúčové metriky
   - **Benefit**: Žiadny marshalling overhead

3. **Adaptive collection** (4h)
   - Process scan len v RT mode
   - Mimo RT: len system-level stats
   - **Benefit**: Nižší CPU load keď nie je aktívny stream

4. **Sampling** (2h, pre 500+ procesov)
   - Sample každý N-tý proces
   - **Benefit**: Konštantný overhead aj na veľkých systémoch

---

## 🔍 Testing Gaps

### Current Coverage: ~15%

**Existujúce testy**:
- ✅ `metrics/cpu_test.go` (3 testy)
- ✅ `internal/agent/agent_test.go` (základné)
- ✅ `httpservices/httpservices_test.go`

**Chýbajúce testy**:
- ❌ `metrics/memory_test.go`
- ❌ `metrics/disk_test.go`
- ❌ `metrics/io_test.go`
- ❌ `metrics/common_test.go`
- ❌ Integration testy pre WS lifecycle
- ❌ Testy pre batch retry logic
- ❌ Testy pre RT streaming
- ❌ Graceful shutdown testy

**Recommendation**: Pridať 40+ unit + integration testov.

---

## 📝 Documentation Gaps

### Missing:
1. Architecture documentation (sequence diagrams)
2. API documentation (OpenAPI spec)
3. GoDoc comments pre public APIs
4. Thread-safety guarantees
5. Startup/shutdown sequence

### Recommended:
- Create `docs/ARCHITECTURE.md`
- Add package-level docs
- Document concurrency patterns
- Add code examples

---

## 🚀 Future Features (Backlog)

Po production-ready (v2.2.0+):

1. **Local spool** - Disk-backed buffer pre API outages (8h)
2. **Telemetry counters** - Prometheus format (4h)
3. **Network metriky** - Bytes sent/received, packets (6h)
4. **Load average** - 1min, 5min, 15min (2h)
5. **Container metrics** - Docker/Podman stats (8h)
6. **TLS config** - Self-signed cert support (2h)
7. **Multi-server** - Backup monitoring endpoints (6h)

---

## 🎓 Lessons Learned

### Good Patterns:
- ✅ Process caching strategy
- ✅ Buffer pooling for HTTP
- ✅ Structured logging
- ✅ Context-based shutdown (väčšinou)

### Anti-Patterns Found:
- ❌ Global state v metrics package
- ❌ God Object Agent
- ❌ Busy-wait loops
- ❌ Swallowed errors
- ❌ TOCTOU bugs v channel handling

### Key Takeaways:
1. **Thread safety je ťažká** - 8 race conditions nájdených
2. **Testovanie je kľúčové** - Veľa bugov by našli testy
3. **Globálny state škodí** - Nemožné paralelne testovať
4. **Context propagation** - Chýba na viacerých miestach
5. **Error handling** - Veľa silent failures

---

## 📈 Success Metrics

Po dokončení všetkých fáz:

| Metrika | Before | After | Improvement |
|---------|--------|-------|-------------|
| Critical bugs | 5 | 0 | -100% |
| Test coverage | 15% | 75%+ | +400% |
| Goroutine leaks | 3 | 0 | -100% |
| Race conditions | 8 | 0 | -100% |
| Global variables | 8 | 0 | -100% |
| Code duplication | Medium | Low | -50% |
| Maintainability | C | A | +2 grades |

---

## 🤝 Recommendations for Team

### Immediate Actions:
1. **Stop new features** - Fix critical bugs first
2. **Emergency release** - v1.0.1 s critical fixes
3. **Add monitoring** - Track goroutine count, memory usage
4. **Enable race detector** - `go test -race ./...`
5. **Setup CI/CD** - Automatic testing on every commit

### Long-term:
1. **Architecture review** - Refactor global state
2. **Test-driven development** - Write tests first
3. **Code review checklist** - Thread safety, error handling
4. **Performance benchmarks** - Track regression
5. **Documentation** - Keep it up-to-date

---

## 📚 Resources

- **TODO_FINAL.md** - Detailný implementation plán
- **Repository**: simplemon-agent
- **Go version**: 1.22+
- **Dependencies**: gorilla/websocket, gopsutil

---

## ✅ Conclusion

Simplemon Agent má **solídnu foundation** s dobrými optimalizáciami (process caching, paralelizmus), ale potrebuje:

1. **Okamžité fixe** - 5 critical bugs (6 hodín)
2. **Stability improvements** - 9 high-priority issues (17 hodín)
3. **Architecture refactor** - Global state + God Object (34 hodín)
4. **Testing** - 75%+ coverage (23 hodín)

**Celkový effort**: ~80 hodín (~2 človeko-mesiace)

**Odporúčanie**: Začnite so **Sprint 0** (Emergency fixes) ešte tento týždeň. Production s kritickými bugmi je **risk**.

---

**Pripravil**: Claude Code Analysis Agent
**Dátum**: 2025-12-27
**Status**: ✅ FINAL
