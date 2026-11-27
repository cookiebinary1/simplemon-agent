// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// I/O metrics collection for Linux systems.

package metrics

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type processIO struct {
	readBytes  uint64
	writeBytes uint64
	readOps    uint64
	writeOps   uint64
	startTime  uint64 // tells a reused PID apart from the old process
}

func getProcessIO(pid int) (*processIO, error) {
	ioPath := filepath.Join("/proc", strconv.Itoa(pid), "io")
	file, err := os.Open(ioPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	io := &processIO{}
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		key, raw, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			continue
		}

		// read_bytes/write_bytes count storage traffic only. rchar/wchar
		// would also count sockets, pipes and page cache hits.
		// syscr/syscw count read and write calls of any kind.
		switch key {
		case "read_bytes":
			io.readBytes = value
		case "write_bytes":
			io.writeBytes = value
		case "syscr":
			io.readOps = value
		case "syscw":
			io.writeOps = value
		}
	}

	return io, scanner.Err()
}
