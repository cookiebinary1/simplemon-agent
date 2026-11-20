// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Common utilities for metrics collection.

package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Process represents a running process with its basic information
type Process struct {
	PID      int
	Name     string
	cpu      processCPUStat
	rssPages uint64
	// sharedPages is resident memory backed by files or shared memory
	// (e.g. Postgres shared buffers). It is the same pages in every process.
	sharedPages uint64
}

func readProcess(pid int) (Process, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return Process{}, err
	}
	proc, err := parseProcessStat(pid, string(data))
	if err != nil {
		return proc, err
	}
	// statm is world readable, unlike smaps_rollup; missing it is not fatal.
	if statm, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "statm")); err == nil {
		proc.sharedPages = parseStatmShared(string(statm))
	}
	return proc, nil
}

// parseStatmShared returns the third statm field: resident shared pages.
func parseStatmShared(statm string) uint64 {
	fields := strings.Fields(statm)
	if len(fields) < 3 {
		return 0
	}
	shared, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return 0
	}
	return shared
}

func parseProcessStat(pid int, stat string) (Process, error) {
	// comm may contain spaces and closing parentheses. RSS is field 24.
	start, end := strings.IndexByte(stat, '('), strings.LastIndexByte(stat, ')')
	if start < 0 || end <= start {
		return Process{}, fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 22 {
		return Process{}, fmt.Errorf("incomplete process stat")
	}
	proc := Process{PID: pid, Name: GetBaseProcessName(strings.TrimSpace(stat[start+1 : end]))}
	for _, field := range []struct {
		index int
		dst   *uint64
	}{
		{11, &proc.cpu.utime}, {12, &proc.cpu.stime}, {19, &proc.cpu.startTime}, {21, &proc.rssPages},
	} {
		value, err := strconv.ParseUint(fields[field.index], 10, 64)
		if err != nil {
			return Process{}, err
		}
		*field.dst = value
	}
	proc.cpu.measuredAt = time.Now()
	return proc, nil
}

// GetBaseProcessName returns the process name without command line arguments
func GetBaseProcessName(fullName string) string {
	// Split by space and take the first part
	parts := strings.Fields(fullName)
	if len(parts) == 0 {
		return fullName
	}

	// Get just the base name without path
	baseName := filepath.Base(parts[0])
	return baseName
}
