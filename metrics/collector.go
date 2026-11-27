// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Collector keeps the previous counters needed to turn cumulative /proc
// values into per-interval deltas.

package metrics

import (
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"syscall"

	"github.com/cookiebinary1/simplemon-agent/internal"
)

const (
	// Forget counters of exited processes every this many samples.
	processCleanupInterval = 60
	maxTopProcesses        = 10
	// USER_HZ, the unit of utime/stime in /proc/<pid>/stat. It is 100 on
	// every architecture Linux supports.
	clockTicks = 100
)

// Collector is safe for concurrent use. CPU, I/O and network keep separate
// state, so the agent gathers them in parallel.
type Collector struct {
	cpuMu            sync.Mutex
	prevCPUStat      *cpuStat
	prevCoreStats    map[int]*cpuStat
	prevProcessStats map[int]processCPUStat
	cpuSamples       int

	ioMu        sync.Mutex
	prevIOStats map[int]processIO
	ioSamples   int

	netMu   sync.Mutex
	prevNet map[string]netCounters

	numCPUCores int
	pageSize    uint64
}

// NewCollector returns a Collector with empty baselines. The first sample
// of every counter based metric reads zero.
func NewCollector() *Collector {
	return &Collector{
		prevCoreStats:    make(map[int]*cpuStat),
		prevProcessStats: make(map[int]processCPUStat),
		prevIOStats:      make(map[int]processIO),
		numCPUCores:      runtime.NumCPU(),
		pageSize:         uint64(syscall.Getpagesize()),
	}
}

// GatherCPU returns overall, per-core and per-process CPU usage since the previous call.
func (c *Collector) GatherCPU(processes []Process) (*internal.CPUMetrics, error) {
	allStats, err := readAllCPUStats()
	if err != nil {
		return nil, err
	}

	c.cpuMu.Lock()
	defer c.cpuMu.Unlock()

	c.cpuSamples++
	if c.cpuSamples >= processCleanupInterval {
		c.cpuSamples = 0
		forgetExited(c.prevProcessStats, processes)
	}

	var overallUsage float64
	coreUsage := make([]float64, c.numCPUCores)
	if c.prevCPUStat != nil {
		overallUsage = calculateCPUUsage(c.prevCPUStat, allStats.Overall)
	}
	c.prevCPUStat = allStats.Overall

	for i := 0; i < c.numCPUCores; i++ {
		if currStat, exists := allStats.Cores[i]; exists {
			if prevStat, hasPrev := c.prevCoreStats[i]; hasPrev {
				coreUsage[i] = calculateCPUUsage(prevStat, currStat)
			}
			c.prevCoreStats[i] = currStat
		} else {
			coreUsage[i] = overallUsage
		}
	}

	processMap := make(map[string]*internal.ProcessInfo)
	for _, proc := range processes {
		cpuUsage := c.processCPUUsage(proc)

		info := groupFor(processMap, proc.Name)
		info.Value += cpuUsage
		info.Count++
	}

	processInfos := topProcesses(processMap)
	for i := range processInfos {
		processInfos[i].ValuePercent = processInfos[i].Value * 100
	}

	return &internal.CPUMetrics{
		OverallUsage:   overallUsage,
		OverallPercent: overallUsage * 100,
		CoreUsage:      coreUsage,
		Processes:      processInfos,
	}, nil
}

// processCPUUsage returns the share of all cores the process used since the
// previous sample, 0..1. Must be called with cpuMu held.
func (c *Collector) processCPUUsage(proc Process) float64 {
	current := proc.cpu
	prev, exists := c.prevProcessStats[proc.PID]
	c.prevProcessStats[proc.PID] = current
	// A new start time means the PID was reused by another process.
	if !exists || prev.startTime != current.startTime || current.utime < prev.utime || current.stime < prev.stime {
		return 0
	}
	elapsed := current.measuredAt.Sub(prev.measuredAt).Seconds()
	if elapsed <= 0 {
		return 0
	}
	cpuSeconds := float64(current.utime-prev.utime+current.stime-prev.stime) / clockTicks
	return cpuSeconds / elapsed / float64(c.numCPUCores)
}

// forgetExited drops baselines of processes that no longer exist.
func forgetExited[V any](prev map[int]V, processes []Process) {
	alive := make(map[int]struct{}, len(processes))
	for _, p := range processes {
		alive[p.PID] = struct{}{}
	}
	for pid := range prev {
		if _, ok := alive[pid]; !ok {
			delete(prev, pid)
		}
	}
}

// GatherMemory collects memory metrics.
func (c *Collector) GatherMemory(processes []Process) (*internal.MemoryMetrics, error) {
	memInfo, err := readMemInfo()
	if err != nil {
		return nil, err
	}

	available := min(memInfo.available, memInfo.total)
	used := memInfo.total - available

	// Summing RSS counts shared pages once per process: 70 Postgres backends
	// sharing 1 GB of buffers showed up as 70 GB. A group is its private
	// memory plus the largest shared set, counted once.
	processMap := make(map[string]*internal.ProcessInfo)
	maxShared := make(map[string]uint64)
	for _, proc := range processes {
		shared := proc.sharedPages
		if shared > proc.rssPages {
			shared = proc.rssPages
		}
		private := (proc.rssPages - shared) * c.pageSize
		shared *= c.pageSize

		info := groupFor(processMap, proc.Name)
		info.ValueBytes += private
		info.Count++
		maxShared[proc.Name] = max(maxShared[proc.Name], shared)
	}
	for name, info := range processMap {
		info.ValueBytes += maxShared[name]
		info.Value = float64(info.ValueBytes)
	}

	processInfos := topProcesses(processMap)
	totalUsed := float64(used)
	if totalUsed > 0 {
		for i := range processInfos {
			processInfos[i].ValuePercent = (processInfos[i].Value / totalUsed) * 100
		}
	}

	var usedRelative float64
	if memInfo.total > 0 {
		usedRelative = float64(used) / float64(memInfo.total) * 100
	}
	return &internal.MemoryMetrics{
		Total:        memInfo.total,
		Available:    memInfo.available,
		Used:         used,
		UsedRelative: usedRelative,
		Processes:    processInfos,
	}, nil
}

// GatherIO returns storage I/O per process group since the previous call.
func (c *Collector) GatherIO(processes []Process) (*internal.IoMetrics, error) {
	var totalRead, totalWrite uint64
	var totalReadOps, totalWriteOps uint64

	c.ioMu.Lock()
	defer c.ioMu.Unlock()

	c.ioSamples++
	if c.ioSamples >= processCleanupInterval {
		c.ioSamples = 0
		forgetExited(c.prevIOStats, processes)
	}

	processMap := make(map[string]*internal.ProcessInfo)
	for _, proc := range processes {
		io, err := getProcessIO(proc.PID)
		if err != nil {
			continue
		}
		io.startTime = proc.cpu.startTime

		prev, exists := c.prevIOStats[proc.PID]
		c.prevIOStats[proc.PID] = *io
		// Skip new processes, reused PIDs and counters that went backwards.
		if !exists || prev.startTime != io.startTime ||
			io.readBytes < prev.readBytes || io.writeBytes < prev.writeBytes ||
			io.readOps < prev.readOps || io.writeOps < prev.writeOps {
			continue
		}

		readDelta := io.readBytes - prev.readBytes
		writeDelta := io.writeBytes - prev.writeBytes
		readOpsDelta := io.readOps - prev.readOps
		writeOpsDelta := io.writeOps - prev.writeOps

		totalRead += readDelta
		totalWrite += writeDelta
		totalReadOps += readOpsDelta
		totalWriteOps += writeOpsDelta

		info := groupFor(processMap, proc.Name)
		info.Value += float64(readDelta + writeDelta)
		info.ValueBytes += readDelta + writeDelta
		info.ReadBytes += readDelta
		info.WriteBytes += writeDelta
		info.ReadOps += readOpsDelta
		info.WriteOps += writeOpsDelta
		info.Count++
	}

	processInfos := topProcesses(processMap)
	totalIOBytes := totalRead + totalWrite
	if totalIOBytes > 0 {
		for i := range processInfos {
			processInfos[i].ValuePercent = (float64(processInfos[i].ValueBytes) / float64(totalIOBytes)) * 100
		}
	}

	return &internal.IoMetrics{
		TotalRead:  totalRead,
		TotalWrite: totalWrite,
		ReadOps:    totalReadOps,
		WriteOps:   totalWriteOps,
		Processes:  processInfos,
	}, nil
}

// groupFor returns the entry for name, creating it on first use.
func groupFor(groups map[string]*internal.ProcessInfo, name string) *internal.ProcessInfo {
	info, ok := groups[name]
	if !ok {
		info = &internal.ProcessInfo{Name: name}
		groups[name] = info
	}
	return info
}

// topProcesses returns the largest groups by Value.
func topProcesses(groups map[string]*internal.ProcessInfo) []internal.ProcessInfo {
	list := make([]internal.ProcessInfo, 0, len(groups))
	for _, info := range groups {
		list = append(list, *info)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Value > list[j].Value })
	if len(list) > maxTopProcesses {
		list = list[:maxTopProcesses]
	}
	return list
}

// ReadProcesses takes one snapshot shared by all collectors in this sample.
func ReadProcesses() ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	var processes []Process
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		proc, err := readProcess(pid)
		if err != nil {
			continue
		}

		processes = append(processes, proc)
	}

	return processes, nil
}
