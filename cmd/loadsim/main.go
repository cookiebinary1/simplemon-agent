// Command loadsim simulates many SimpleMon agents and live viewers against
// an ingest server. Credentials come from `php artisan loadtest:seed`.
//
// Each simulated server sends a 10 s sample batch every 10 s over HTTP
// (same payload as the real agent), keeps the /incoming WebSocket open and
// answers START with 1 s realtime frames. A share of servers also gets a
// viewer on /outgoing that sends START and TICK like the browser does.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"simplemon-agent/httpservices"
	"simplemon-agent/internal"
	"simplemon-agent/internal/agent"
)

const version = "loadsim"

type creds struct {
	APIKey      string `json:"api_key"`
	ServerToken string `json:"server_token"`
	ServerID    string `json:"server_id"`
	ViewerToken string `json:"viewer_token"`
}

type stats struct {
	mu        sync.Mutex
	latencies []time.Duration
	batchOK   atomic.Int64
	batchErr  atomic.Int64
	wsUp      atomic.Int64
	wsErr     atomic.Int64
	viewersUp atomic.Int64
	rtFrames  atomic.Int64
	lastErr   atomic.Value
}

func (s *stats) observe(d time.Duration, err error) {
	if err != nil {
		s.batchErr.Add(1)
		s.lastErr.Store(err.Error())
		return
	}
	s.batchOK.Add(1)
	s.mu.Lock()
	s.latencies = append(s.latencies, d)
	s.mu.Unlock()
}

func main() {
	file := flag.String("creds", "loadsim.jsonl", "credentials file from loadtest:seed")
	host := flag.String("host", "localhost:18092", "ingest host[:port]")
	secure := flag.Bool("secure", false, "use https/wss")
	servers := flag.Int("servers", 0, "number of servers to simulate (0 = all in file)")
	rampUp := flag.Duration("ramp", time.Minute, "spread agent start over this duration")
	interval := flag.Duration("interval", 10*time.Second, "batch interval")
	viewers := flag.Float64("viewers", 0.05, "share of servers with a live viewer")
	noWS := flag.Bool("no-ws", false, "do not open agent WebSockets")
	duration := flag.Duration("duration", 0, "stop after this time (0 = until Ctrl+C)")
	origin := flag.String("origin", "https://localhost", "Origin header for viewer sockets")
	webAddr := flag.String("web", "", "serve fake web targets on this address, e.g. :8099")
	flag.Parse()

	list, err := readCreds(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *servers > 0 && *servers < len(list) {
		list = list[:*servers]
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *duration > 0 {
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	if *webAddr != "" {
		go serveFakeWeb(*webAddr)
	}

	httpProto, wsProto := "http", "ws"
	if *secure {
		httpProto, wsProto = "https", "wss"
	}
	st := &stats{}
	var wg sync.WaitGroup
	fmt.Printf("simulating %d servers, %.0f%% viewers, ramp %s\n", len(list), *viewers*100, *rampUp)

	for i, c := range list {
		delay := time.Duration(0)
		if len(list) > 1 {
			delay = *rampUp * time.Duration(i) / time.Duration(len(list))
		}
		withViewer := rand.Float64() < *viewers
		wg.Add(1)
		go func(c creds) {
			defer wg.Done()
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			sim := &server{c: c, st: st, interval: *interval, rng: rand.New(rand.NewSource(rand.Int63()))}
			sim.httpClient = httpservices.NewClient(fmt.Sprintf("%s://%s/api/v1/metrics/batch", httpProto, *host), c.APIKey, c.ServerToken, "loadsim-"+c.ServerID[:8], version)
			if !*noWS {
				go sim.agentSocket(ctx, fmt.Sprintf("%s://%s/incoming", wsProto, *host))
			}
			if withViewer && c.ViewerToken != "" {
				go viewer(ctx, fmt.Sprintf("%s://%s/outgoing", wsProto, *host), *origin, c, st)
			}
			sim.run(ctx)
		}(c)
	}

	go report(ctx, st)
	<-ctx.Done()
	wg.Wait()
	printReport(st, true)
}

func readCreds(path string) ([]creds, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var list []creds
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var c creds
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			return nil, fmt.Errorf("bad line in %s: %w", path, err)
		}
		list = append(list, c)
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("%s has no credentials", path)
	}
	return list, sc.Err()
}

type server struct {
	c          creds
	st         *stats
	interval   time.Duration
	rng        *rand.Rand
	httpClient *httpservices.Client
	cpu, mem   float64
	minute     int64
	mu         sync.Mutex
	ws         *httpservices.WSClient
}

// run sends one aggregated sample per interval, processes once per minute.
func (s *server) run(ctx context.Context) {
	s.cpu, s.mem = s.rng.Float64()*0.5, 20+s.rng.Float64()*50
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			minute := now.Unix() / 60
			entry := s.sample(now, s.interval.Seconds(), minute != s.minute)
			s.minute = minute
			start := time.Now()
			err := s.httpClient.SendMetrics([]agent.MetricEntry{entry})
			s.st.observe(time.Since(start), err)
			if err == nil {
				s.send(`{"action": "ready"}`)
			}
		}
	}
}

func (s *server) send(msg string) {
	s.mu.Lock()
	ws := s.ws
	s.mu.Unlock()
	if ws != nil {
		_ = ws.SendMessage(msg)
	}
}

// agentSocket keeps /incoming open and streams 1 s frames after START.
func (s *server) agentSocket(ctx context.Context, url string) {
	for ctx.Err() == nil {
		ws, err := httpservices.NewWSClient(url, s.c.APIKey, s.c.ServerToken, version)
		if err != nil {
			s.st.wsErr.Add(1)
			sleep(ctx, 5*time.Second+time.Duration(s.rng.Intn(5000))*time.Millisecond)
			continue
		}
		s.st.wsUp.Add(1)
		s.mu.Lock()
		s.ws = ws
		s.mu.Unlock()

		var lastTick atomic.Int64
		stop := make(chan struct{})
		go func() {
			<-ctx.Done()
			ws.Close()
		}()
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case now := <-t.C:
					if time.Since(time.Unix(0, lastTick.Load())) < 30*time.Second {
						_ = ws.SendMetrics(s.sample(now, 1, false))
						s.st.rtFrames.Add(1)
					}
				}
			}
		}()
		for {
			msg, err := ws.ReceiveMessageWithContext(ctx)
			if err != nil {
				break
			}
			var cmd struct{ Action string }
			if json.Unmarshal([]byte(msg), &cmd) == nil && (cmd.Action == "START" || cmd.Action == "TICK") {
				lastTick.Store(time.Now().UnixNano())
			}
		}
		close(stop)
		s.mu.Lock()
		s.ws = nil
		s.mu.Unlock()
		s.st.wsUp.Add(-1)
		ws.Close()
		sleep(ctx, 2*time.Second)
	}
}

// viewer acts like an open server detail page: START, then TICK every 5 s.
func viewer(ctx context.Context, url, origin string, c creds, st *stats) {
	dialer := websocket.Dialer{Subprotocols: []string{c.ViewerToken, c.ServerID}, HandshakeTimeout: 10 * time.Second}
	for ctx.Err() == nil {
		conn, _, err := dialer.DialContext(ctx, url, http.Header{"Origin": {origin}})
		if err != nil {
			st.lastErr.Store("viewer: " + err.Error())
			sleep(ctx, 5*time.Second)
			continue
		}
		st.viewersUp.Add(1)
		go func() {
			<-ctx.Done()
			conn.Close()
		}()
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"action":"START"}`))
		t := time.NewTicker(5 * time.Second)
	loop:
		for {
			select {
			case <-done:
				break loop
			case <-t.C:
				if conn.WriteMessage(websocket.TextMessage, []byte(`{"action":"TICK"}`)) != nil {
					break loop
				}
			}
		}
		t.Stop()
		conn.Close()
		st.viewersUp.Add(-1)
		sleep(ctx, 2*time.Second)
	}
}

// sample builds a realistic entry; values drift slowly like a real host.
func (s *server) sample(now time.Time, delta float64, withProcesses bool) agent.MetricEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cpu = clamp(s.cpu+(s.rng.Float64()-0.5)*0.1, 0.01, 0.99)
	s.mem = clamp(s.mem+(s.rng.Float64()-0.5)*2, 5, 95)
	const total = 16 << 30
	used := uint64(float64(total) * s.mem / 100)
	cores := make([]float64, 8)
	for i := range cores {
		cores[i] = clamp(s.cpu+(s.rng.Float64()-0.5)*0.2, 0, 1)
	}
	procs := []internal.ProcessInfo{}
	memProcs := []internal.ProcessInfo{}
	ioProcs := []internal.ProcessInfo{}
	if withProcesses {
		for i, name := range []string{"nginx", "php-fpm", "postgres", "redis-server", "node", "dockerd", "systemd", "sshd", "cron", "containerd"} {
			v := s.cpu / float64(i+2)
			procs = append(procs, internal.ProcessInfo{Name: name, Value: v, ValuePercent: v * 100, Count: 1 + i%4})
			memProcs = append(memProcs, internal.ProcessInfo{Name: name, Value: s.mem / float64(i+3), ValueBytes: used / uint64(i+3), Count: 1 + i%4})
			ioProcs = append(ioProcs, internal.ProcessInfo{Name: name, ReadBytes: uint64(s.rng.Intn(1 << 20)), WriteBytes: uint64(s.rng.Intn(1 << 20)), Count: 1})
		}
	}
	rx, tx := uint64(s.rng.Intn(5<<20)*int(delta)), uint64(s.rng.Intn(2<<20)*int(delta))
	return agent.MetricEntry{
		Timestamp: now.UTC().Format(time.RFC3339Nano),
		DeltaTime: delta,
		CPU:       &internal.CPUMetrics{OverallUsage: s.cpu, OverallPercent: s.cpu * 100, CoreUsage: cores, Processes: procs},
		Memory:    &internal.MemoryMetrics{Total: total, Used: used, Available: total - used, UsedRelative: s.mem, Processes: memProcs},
		IO:        &internal.IoMetrics{TotalRead: uint64(s.rng.Intn(10 << 20)), TotalWrite: uint64(s.rng.Intn(10 << 20)), ReadOps: 100, WriteOps: 200, Processes: ioProcs},
		Disks: []internal.DiskMetric{
			{Path: "/", Type: "local", Fstype: "ext4", Total: 160 << 30, Used: 60 << 30, Free: 100 << 30, UsedRelative: 37.5},
			{Path: "/boot", Type: "local", Fstype: "ext4", Total: 1 << 30, Used: 200 << 20, Free: 824 << 20, UsedRelative: 19.5},
		},
		Network: &internal.NetworkMetrics{RxBytes: rx, TxBytes: tx, RxPackets: rx / 1000, TxPackets: tx / 1000,
			Interfaces: []internal.NetworkInterface{{Name: "eth0", RxBytes: rx, TxBytes: tx, RxPackets: rx / 1000, TxPackets: tx / 1000}}},
		System: &internal.SystemMetrics{Load1: s.cpu * 8, Load5: s.cpu * 7, Load15: s.cpu * 6, UptimeSeconds: 864000, TCPConnections: 120},
	}
}

// serveFakeWeb answers web checks with random latency and ~1 % errors.
func serveFakeWeb(addr string) {
	http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Duration(20+rand.Intn(300)) * time.Millisecond)
		if rand.Intn(100) == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "<html><title>loadsim</title><body>ok</body></html>")
	}))
}

func report(ctx context.Context, st *stats) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			printReport(st, false)
		}
	}
}

// printReport prints counters and latency percentiles since the last report.
func printReport(st *stats, final bool) {
	st.mu.Lock()
	lat := st.latencies
	st.latencies = nil
	st.mu.Unlock()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[int(float64(len(lat)-1)*p)].Round(time.Millisecond)
	}
	label := time.Now().Format("15:04:05")
	if final {
		label = "final"
	}
	fmt.Printf("[%s] batches ok=%d err=%d | p50=%s p95=%s p99=%s | agent ws=%d wsErr=%d | viewers=%d rtFrames=%d\n",
		label, st.batchOK.Load(), st.batchErr.Load(), pct(0.5), pct(0.95), pct(0.99),
		st.wsUp.Load(), st.wsErr.Load(), st.viewersUp.Load(), st.rtFrames.Load())
	if e, ok := st.lastErr.Swap("").(string); ok && e != "" {
		fmt.Printf("  last error: %.200s\n", e)
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}
