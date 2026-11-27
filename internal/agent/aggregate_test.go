package agent

import (
	"testing"
	"time"

	"github.com/cookiebinary1/simplemon-agent/internal"
)

func sample(ts string, cpu, mem float64, read, rx uint64, procs []internal.ProcessInfo) MetricEntry {
	return MetricEntry{
		Timestamp: ts,
		DeltaTime: 1,
		CPU:       &internal.CPUMetrics{OverallUsage: cpu, OverallPercent: cpu * 100, CoreUsage: []float64{cpu, cpu}, Processes: procs},
		Memory:    &internal.MemoryMetrics{Total: 100, Used: uint64(mem), Available: 100 - uint64(mem), UsedRelative: mem, Processes: procs},
		IO:        &internal.IoMetrics{TotalRead: read, Processes: procs},
		Network: &internal.NetworkMetrics{RxBytes: rx, Interfaces: []internal.NetworkInterface{
			{Name: "eth0", RxBytes: rx},
		}},
		Disks:  []internal.DiskMetric{{Path: "/", Total: 10}},
		System: &internal.SystemMetrics{Load1: cpu},
	}
}

func TestAggregateSamples(t *testing.T) {
	samples := []MetricEntry{
		sample("t1", 0.2, 40, 100, 1000, []internal.ProcessInfo{{Name: "a", Value: 0.4, Count: 1}}),
		sample("t2", 0.4, 60, 300, 3000, []internal.ProcessInfo{{Name: "a", Value: 0.2, Count: 2}, {Name: "b", Value: 0.6, Count: 1}}),
	}

	got := aggregateSamples(samples, true)

	if got.Timestamp != "t2" || got.DeltaTime != 2 {
		t.Fatalf("timestamp/delta = %s/%v", got.Timestamp, got.DeltaTime)
	}
	if !near(got.CPU.OverallUsage, 0.3) || !near(got.CPU.CoreUsage[1], 0.3) {
		t.Errorf("cpu = %v %v", got.CPU.OverallUsage, got.CPU.CoreUsage)
	}
	if got.Memory.Used != 50 || !near(got.Memory.UsedRelative, 50) || got.Memory.Total != 100 {
		t.Errorf("memory = %+v", got.Memory)
	}
	// Counters are summed so bytes / delta_time stays a correct rate.
	if got.IO.TotalRead != 400 || got.Network.RxBytes != 4000 || got.Network.Interfaces[0].RxBytes != 4000 {
		t.Errorf("counters io=%d net=%d", got.IO.TotalRead, got.Network.RxBytes)
	}
	if got.System.Load1 != 0.4 || len(got.Disks) != 1 {
		t.Errorf("latest values not kept")
	}
	// Missing process counts as zero: b = 0.6/2, a = (0.4+0.2)/2.
	procs := got.CPU.Processes
	if len(procs) != 2 || !near(procs[0].Value, 0.3) || !near(procs[1].Value, 0.3) {
		t.Errorf("processes = %+v", procs)
	}
}

// Per-process I/O is summed like the I/O totals, so shares add up to 100 %.
func TestAggregateIOProcessesAreSummed(t *testing.T) {
	io := func(read uint64, procs ...internal.ProcessInfo) MetricEntry {
		return MetricEntry{Timestamp: "t", DeltaTime: 1, IO: &internal.IoMetrics{TotalRead: read, Processes: procs}}
	}
	got := aggregateSamples([]MetricEntry{
		io(300, internal.ProcessInfo{Name: "db", Value: 200, ValueBytes: 200, ReadBytes: 200}, internal.ProcessInfo{Name: "web", Value: 100, ValueBytes: 100, ReadBytes: 100}),
		io(100, internal.ProcessInfo{Name: "db", Value: 100, ValueBytes: 100, ReadBytes: 100}),
	}, true)
	procs := got.IO.Processes
	if got.IO.TotalRead != 400 || len(procs) != 2 || procs[0].Name != "db" || procs[0].ReadBytes != 300 || procs[1].ReadBytes != 100 {
		t.Fatalf("io = %+v", got.IO)
	}
	if !near(procs[0].ValuePercent, 75) || !near(procs[1].ValuePercent, 25) {
		t.Errorf("shares = %v %v", procs[0].ValuePercent, procs[1].ValuePercent)
	}
}

func TestAggregateSamplesWithoutProcesses(t *testing.T) {
	got := aggregateSamples([]MetricEntry{sample("t1", 0.1, 10, 0, 0, []internal.ProcessInfo{{Name: "a", Value: 1}})}, false)
	if got.CPU.Processes == nil || len(got.CPU.Processes) != 0 || len(got.Memory.Processes) != 0 || len(got.IO.Processes) != 0 {
		t.Errorf("processes should be empty lists")
	}
}

func TestFlushWindowStoresProcessesOncePerMinute(t *testing.T) {
	a := &Agent{}
	base := time.Date(2026, 1, 1, 10, 0, 5, 0, time.UTC)
	for i := 0; i < 7; i++ {
		a.window = []MetricEntry{sample("t", 0.1, 10, 0, 0, []internal.ProcessInfo{{Name: "a", Value: 1}})}
		a.flushWindow(base.Add(time.Duration(i*10) * time.Second))
	}
	with := 0
	for _, e := range a.batch {
		if len(e.CPU.Processes) > 0 {
			with++
		}
	}
	// 10:00:05 .. 10:01:05 spans two wall-clock minutes.
	if len(a.batch) != 7 || with != 2 {
		t.Errorf("batch=%d withProcesses=%d", len(a.batch), with)
	}
	a.flushWindow(base)
	if len(a.batch) != 7 {
		t.Errorf("empty window must not add a sample")
	}
}

func near(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}

func TestSlimEntryKeepsOriginal(t *testing.T) {
	full := sample("t", 0.5, 50, 1, 1, []internal.ProcessInfo{{Name: "a", Value: 1}})
	slim := slimEntry(full)
	if slim.CPU.Processes != nil || slim.Memory.Processes != nil || slim.IO.Processes != nil {
		t.Errorf("slim entry still has processes")
	}
	if len(full.CPU.Processes) != 1 || slim.CPU.OverallUsage != 0.5 {
		t.Errorf("original modified or values lost")
	}
}
