// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Memory metrics collection for Linux systems.

package metrics

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type memInfo struct {
	total, free, available, buffers, cached, swapTotal, swapFree uint64
}

func readMemInfo() (*memInfo, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info := &memInfo{}
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}

		// Convert KB to bytes
		value *= 1024

		switch fields[0] {
		case "MemTotal:":
			info.total = value
		case "MemFree:":
			info.free = value
		case "MemAvailable:":
			info.available = value
		case "Buffers:":
			info.buffers = value
		case "Cached:":
			info.cached = value
		case "SwapTotal:":
			info.swapTotal = value
		case "SwapFree:":
			info.swapFree = value
		}
	}

	return info, scanner.Err()
}
