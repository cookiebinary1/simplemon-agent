// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Collector provides a stateful metrics collector that avoids global state.
// This enables better testing and multiple collector instances.

package metrics

import (
	"os"
	"runtime"
	"simplemon-agent/internal"
	"sort"
	"strconv"
	"sync"
	"syscall"
)

// Collector manages state for metrics collection.
// It is safe for concurrent use within a single instance.
type Collector struct {
	// CPU state
	prevCPUStat      *cpuStat
	prevCoreStats    map[int]*cpuStat
	prevProcessStats map[int]processCPUStat
	cpuMu            sync.RWMutex
	cpuCleanupCount  int

	// I/O state
	prevIOStats    map[int]processIO
	ioMu           sync.RWMutex
	ioCleanupCount int

	// Network state
	prevNet map[string]netCounters
	netMu   sync.Mutex

	// Configuration
	metricInterval int64
	numCPUCores    int
	pageSize       uint64
}

// NewCollector creates a new metrics Collector.
// metricIntervalMs is the metric collection interval in milliseconds.
func NewCollector(metricIntervalMs int64) *Collector {
	return &Collector{
		prevCoreStats:    make(map[int]*cpuStat),
		prevProcessStats: make(map[int]processCPUStat),
		prevIOStats:      make(map[int]processIO),
		metricInterval:   metricIntervalMs,
		numCPUCores:      runtime.NumCPU(),
		pageSize:         uint64(syscall.Getpagesize()),
	}
}

// GatherCPU collects CPU metrics using the Collector's state.
func (c *Collector) GatherCPU(processes []Process) (*internal.CPUMetrics, error) {
	// Periodic cleanup of stale process stats
	c.cpuMu.Lock()
	c.cpuCleanupCount++
	needsCleanup := c.cpuCleanupCount >= processCleanupInterval
	if needsCleanup {
		c.cpuCleanupCount = 0
	}
	c.cpuMu.Unlock()

	if needsCleanup {
		c.cleanupStaleCPUStats(processes)
	}

	// Read all CPU stats in a single pass
	allStats, err := readAllCPUStats()
	if err != nil {
		return nil, err
	}

	// Calculate overall CPU usage
	var overallUsage float64
	coreUsage := make([]float64, c.numCPUCores)

	c.cpuMu.Lock()
	if c.prevCPUStat != nil {
		overallUsage = calculateCPUUsage(c.prevCPUStat, allStats.Overall)
	}
	c.prevCPUStat = allStats.Overall

	// Calculate per-core usage
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
	c.cpuMu.Unlock()

	// Get process information
	// Pre-allocate map with estimated size
	processMap := make(map[string]*internal.ProcessInfo, len(processes)/processMapInitDivisor)
	for _, proc := range processes {
		cpuUsage := c.getProcessCPUUsage(proc)

		baseName := proc.Name
		if info, exists := processMap[baseName]; exists {
			info.Value += cpuUsage
			info.Count++
		} else {
			processMap[baseName] = &internal.ProcessInfo{
				Name:  baseName,
				Value: cpuUsage,
				Count: 1,
			}
		}
	}

	// Convert map to slice and sort
	processInfos := make([]internal.ProcessInfo, 0, len(processMap))
	for _, info := range processMap {
		info.ValuePercent = info.Value * 100
		processInfos = append(processInfos, *info)
	}

	sort.Slice(processInfos, func(i, j int) bool {
		return processInfos[i].Value > processInfos[j].Value
	})

	// Limit to top processes
	if len(processInfos) > maxTopProcesses {
		processInfos = processInfos[:maxTopProcesses]
	}

	return &internal.CPUMetrics{
		OverallUsage:   overallUsage,
		OverallPercent: overallUsage * 100,
		CoreUsage:      coreUsage,
		Processes:      processInfos,
	}, nil
}

// getProcessCPUUsage calculates CPU usage for a specific process.
func (c *Collector) getProcessCPUUsage(proc Process) float64 {
	pid, current := proc.PID, proc.cpu
	c.cpuMu.Lock()
	defer c.cpuMu.Unlock()

	prevStat, exists := c.prevProcessStats[pid]
	c.prevProcessStats[pid] = current
	if !exists || prevStat.startTime != current.startTime || current.utime < prevStat.utime || current.stime < prevStat.stime {
		return 0
	}

	// Calculate deltas
	utimeDelta := current.utime - prevStat.utime
	stimeDelta := current.stime - prevStat.stime
	totalDelta := utimeDelta + stimeDelta

	// Convert to ratio (0-1)
	// USER_HZ is typically 100, so we need to divide by 100 to get seconds
	secondsElapsed := current.measuredAt.Sub(prevStat.measuredAt).Seconds()
	cpuTime := float64(totalDelta) / 100.0 // Convert from USER_HZ to seconds

	// Calculate ratio and normalize by number of CPU cores
	if secondsElapsed > 0 {
		return (cpuTime / secondsElapsed) / float64(c.numCPUCores)
	}
	return 0
}

// cleanupStaleCPUStats removes entries for processes that no longer exist.
func (c *Collector) cleanupStaleCPUStats(processes []Process) {
	// Build set of active PIDs
	activePIDs := make(map[int]struct{}, len(processes))
	for _, p := range processes {
		activePIDs[p.PID] = struct{}{}
	}

	c.cpuMu.Lock()
	defer c.cpuMu.Unlock()

	// Remove stale entries
	for pid := range c.prevProcessStats {
		if _, exists := activePIDs[pid]; !exists {
			delete(c.prevProcessStats, pid)
		}
	}
}

// GatherMemory collects memory metrics.
func (c *Collector) GatherMemory(processes []Process) (*internal.MemoryMetrics, error) {
	memInfo, err := readMemInfo()
	if err != nil {
		return nil, err
	}

	// Calculate used memory
	available := memInfo.available
	if available > memInfo.total {
		available = memInfo.total
	}
	used := memInfo.total - available

	// Get process information
	// Pre-allocate map with estimated size
	// Summing RSS counts shared pages once per process: 70 Postgres backends
	// sharing 1 GB of buffers showed up as 70 GB. A group is its private
	// memory plus the largest shared set, counted once.
	processMap := make(map[string]*internal.ProcessInfo, len(processes)/processMapInitDivisor)
	maxShared := make(map[string]uint64, len(processes)/processMapInitDivisor)
	for _, proc := range processes {
		shared := proc.sharedPages
		if shared > proc.rssPages {
			shared = proc.rssPages
		}
		private := (proc.rssPages - shared) * c.pageSize
		shared *= c.pageSize

		baseName := proc.Name
		info, exists := processMap[baseName]
		if !exists {
			info = &internal.ProcessInfo{Name: baseName}
			processMap[baseName] = info
		}
		info.ValueBytes += private
		info.Count++
		if shared > maxShared[baseName] {
			maxShared[baseName] = shared
		}
	}
	for name, info := range processMap {
		info.ValueBytes += maxShared[name]
		info.Value = float64(info.ValueBytes)
	}

	// Convert map to slice and sort
	processInfos := make([]internal.ProcessInfo, 0, len(processMap))
	for _, info := range processMap {
		processInfos = append(processInfos, *info)
	}

	sort.Slice(processInfos, func(i, j int) bool {
		return processInfos[i].Value > processInfos[j].Value
	})

	// Limit to top processes
	if len(processInfos) > maxTopProcesses {
		processInfos = processInfos[:maxTopProcesses]
	}

	totalUsed := float64(used)
	if totalUsed > 0 {
		for i := range processInfos {
			processInfos[i].ValuePercent = (processInfos[i].Value / totalUsed) * 100
		}
	}

	return &internal.MemoryMetrics{
		Total:     memInfo.total,
		Available: memInfo.available,
		Used:      used,
		UsedRelative: func() float64 {
			if memInfo.total > 0 {
				return (float64(used) / float64(memInfo.total)) * 100
			}
			return 0
		}(),
		Processes: processInfos,
	}, nil
}

// GatherDisk collects disk metrics.
func (c *Collector) GatherDisk() ([]internal.DiskMetric, error) {
	return GatherDiskMetrics()
}

// GatherIO collects I/O metrics using the Collector's state.
func (c *Collector) GatherIO(processes []Process) (*internal.IoMetrics, error) {
	var totalRead, totalWrite uint64
	var totalReadOps, totalWriteOps uint64

	c.ioMu.Lock()
	defer c.ioMu.Unlock()

	// Periodic cleanup of stale IO stats
	c.ioCleanupCount++
	if c.ioCleanupCount >= processCleanupInterval {
		c.cleanupStaleIOStatsLocked(processes)
		c.ioCleanupCount = 0
	}

	// Pre-allocate map with estimated size
	processMap := make(map[string]*internal.ProcessInfo, len(processes)/processMapInitDivisor)

	for _, proc := range processes {
		io, err := getProcessIO(proc.PID)
		if err != nil {
			continue
		}

		prevIO, exists := c.prevIOStats[proc.PID]
		if !exists {
			c.prevIOStats[proc.PID] = *io
			continue
		}

		// Reset baselines when counters go backwards (including reused PIDs).
		if io.readBytes < prevIO.readBytes || io.writeBytes < prevIO.writeBytes || io.readOps < prevIO.readOps || io.writeOps < prevIO.writeOps {
			c.prevIOStats[proc.PID] = *io
			continue
		}
		// Calculate deltas
		readDelta := io.readBytes - prevIO.readBytes
		writeDelta := io.writeBytes - prevIO.writeBytes
		readOpsDelta := io.readOps - prevIO.readOps
		writeOpsDelta := io.writeOps - prevIO.writeOps

		totalRead += readDelta
		totalWrite += writeDelta
		totalReadOps += readOpsDelta
		totalWriteOps += writeOpsDelta

		deltaBytes := readDelta + writeDelta
		baseName := proc.Name

		if info, exists := processMap[baseName]; exists {
			info.Value += float64(deltaBytes)
			info.Count++
			info.ValueBytes += deltaBytes
			info.ReadBytes += readDelta
			info.WriteBytes += writeDelta
			info.ReadOps += readOpsDelta
			info.WriteOps += writeOpsDelta
		} else {
			processMap[baseName] = &internal.ProcessInfo{
				Name:       baseName,
				Value:      float64(deltaBytes),
				ValueBytes: deltaBytes,
				ReadBytes:  readDelta,
				WriteBytes: writeDelta,
				ReadOps:    readOpsDelta,
				WriteOps:   writeOpsDelta,
				Count:      1,
			}
		}

		c.prevIOStats[proc.PID] = *io
	}

	// Convert map to slice and sort
	processInfos := make([]internal.ProcessInfo, 0, len(processMap))
	for _, info := range processMap {
		processInfos = append(processInfos, *info)
	}

	sort.Slice(processInfos, func(i, j int) bool {
		return processInfos[i].Value > processInfos[j].Value
	})

	// Limit to top processes
	if len(processInfos) > maxTopProcesses {
		processInfos = processInfos[:maxTopProcesses]
	}

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

// cleanupStaleIOStatsLocked removes entries for processes that no longer exist.
// Must be called with ioMu held.
func (c *Collector) cleanupStaleIOStatsLocked(processes []Process) {
	// Build set of active PIDs
	activePIDs := make(map[int]struct{}, len(processes))
	for _, p := range processes {
		activePIDs[p.PID] = struct{}{}
	}

	// Remove stale entries
	for pid := range c.prevIOStats {
		if _, exists := activePIDs[pid]; !exists {
			delete(c.prevIOStats, pid)
		}
	}
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

// SetMetricInterval updates the metric interval used for CPU calculations.
func (c *Collector) SetMetricInterval(intervalMs int64) {
	c.cpuMu.Lock()
	c.metricInterval = intervalMs
	c.cpuMu.Unlock()
}
