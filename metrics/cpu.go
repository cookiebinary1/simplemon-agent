// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// CPU metrics collection for Linux systems.

package metrics

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type cpuStat struct {
	user, nice, system, idle, iowait, irq, softirq, steal, guest, guestnice uint64
}

type processCPUStat struct {
	utime      uint64
	stime      uint64
	startTime  uint64
	measuredAt time.Time
}

// AllCPUStats holds both overall and per-core CPU statistics
type AllCPUStats struct {
	Overall *cpuStat
	Cores   map[int]*cpuStat
}

// readAllCPUStats reads overall and per-core CPU stats in a single pass
func readAllCPUStats() (*AllCPUStats, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := &AllCPUStats{
		Cores: make(map[int]*cpuStat, runtime.NumCPU()),
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu") {
			break // CPU lines are at the top, stop when we hit other stats
		}

		fields := strings.Fields(line)
		if len(fields) < 11 {
			continue
		}

		stat := parseCPUFields(fields)

		if fields[0] == "cpu" {
			// Overall CPU stats
			result.Overall = stat
		} else {
			// Per-core stats (cpu0, cpu1, etc.)
			coreIndexStr := fields[0][3:] // Skip "cpu" prefix
			if coreIndex, err := strconv.Atoi(coreIndexStr); err == nil {
				result.Cores[coreIndex] = stat
			}
		}
	}

	if result.Overall == nil {
		return nil, fmt.Errorf("failed to read CPU stats")
	}

	return result, scanner.Err()
}

// parseCPUFields parses CPU stat fields into a cpuStat struct
func parseCPUFields(fields []string) *cpuStat {
	stat := &cpuStat{}
	if len(fields) > 1 {
		stat.user, _ = strconv.ParseUint(fields[1], 10, 64)
	}
	if len(fields) > 2 {
		stat.nice, _ = strconv.ParseUint(fields[2], 10, 64)
	}
	if len(fields) > 3 {
		stat.system, _ = strconv.ParseUint(fields[3], 10, 64)
	}
	if len(fields) > 4 {
		stat.idle, _ = strconv.ParseUint(fields[4], 10, 64)
	}
	if len(fields) > 5 {
		stat.iowait, _ = strconv.ParseUint(fields[5], 10, 64)
	}
	if len(fields) > 6 {
		stat.irq, _ = strconv.ParseUint(fields[6], 10, 64)
	}
	if len(fields) > 7 {
		stat.softirq, _ = strconv.ParseUint(fields[7], 10, 64)
	}
	if len(fields) > 8 {
		stat.steal, _ = strconv.ParseUint(fields[8], 10, 64)
	}
	if len(fields) > 9 {
		stat.guest, _ = strconv.ParseUint(fields[9], 10, 64)
	}
	if len(fields) > 10 {
		stat.guestnice, _ = strconv.ParseUint(fields[10], 10, 64)
	}
	return stat
}

func calculateCPUUsage(prev, curr *cpuStat) float64 {
	prevTotal := prev.user + prev.nice + prev.system + prev.idle + prev.iowait + prev.irq + prev.softirq + prev.steal
	currTotal := curr.user + curr.nice + curr.system + curr.idle + curr.iowait + curr.irq + curr.softirq + curr.steal

	prevIdle := prev.idle + prev.iowait
	currIdle := curr.idle + curr.iowait

	if currTotal < prevTotal || currIdle < prevIdle {
		return 0
	}
	totalDelta := currTotal - prevTotal
	idleDelta := currIdle - prevIdle

	if totalDelta == 0 || currIdle-prevIdle > totalDelta {
		return 0
	}

	return float64(totalDelta-idleDelta) / float64(totalDelta)
}
