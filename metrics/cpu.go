// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// CPU metrics collection for Linux systems.

package metrics

import (
	"bufio"
	"fmt"
	"os"
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

type allCPUStats struct {
	Overall *cpuStat
	Cores   map[int]*cpuStat
}

// readAllCPUStats reads the total and per-core lines of /proc/stat.
func readAllCPUStats() (*allCPUStats, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := &allCPUStats{Cores: make(map[int]*cpuStat)}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			break // the cpu lines come first
		}
		if len(fields) < 11 {
			continue
		}
		stat := parseCPUFields(fields)
		if fields[0] == "cpu" {
			result.Overall = stat
		} else if core, err := strconv.Atoi(fields[0][3:]); err == nil {
			result.Cores[core] = stat
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if result.Overall == nil {
		return nil, fmt.Errorf("no cpu line in /proc/stat")
	}
	return result, nil
}

// parseCPUFields parses a /proc/stat cpu line. Missing fields stay zero.
func parseCPUFields(fields []string) *cpuStat {
	stat := &cpuStat{}
	dst := []*uint64{&stat.user, &stat.nice, &stat.system, &stat.idle, &stat.iowait,
		&stat.irq, &stat.softirq, &stat.steal, &stat.guest, &stat.guestnice}
	for i, d := range dst {
		if i+1 < len(fields) {
			*d, _ = strconv.ParseUint(fields[i+1], 10, 64)
		}
	}
	return stat
}

// calculateCPUUsage returns the busy share between two samples, 0..1.
// guest time is already part of user time, so it is not added again.
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

	if totalDelta == 0 || idleDelta > totalDelta {
		return 0
	}

	return float64(totalDelta-idleDelta) / float64(totalDelta)
}
