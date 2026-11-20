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

	var buffer [256]byte
	scanner.Buffer(buffer[:], bufio.MaxScanTokenSize)
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

		switch key {
		case "rchar":
			// rchar = all bytes read (including from cache)
			io.readBytes = value
		case "wchar":
			// wchar = all bytes written (including to cache)
			io.writeBytes = value
		case "syscr":
			io.readOps = value
		case "syscw":
			io.writeOps = value
		}
	}

	return io, scanner.Err()
}
