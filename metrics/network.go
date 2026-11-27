// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Network and system metrics collection for Linux systems.

package metrics

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/cookiebinary1/simplemon-agent/internal"
)

type netCounters struct {
	rxBytes, rxPackets, rxErrors, rxDropped uint64
	txBytes, txPackets, txErrors, txDropped uint64
}

// Virtual interfaces would double count traffic that already passes a physical NIC.
var virtualPrefixes = []string{"lo", "veth", "docker", "br-", "virbr", "cni", "flannel", "cali", "vxlan", "tun", "tap", "kube", "lxc", "vnet"}

func isVirtualInterface(name string) bool {
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func readNetDev(path string) (map[string]netCounters, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := make(map[string]netCounters)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, rest, found := strings.Cut(scanner.Text(), ":")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		fields := strings.Fields(rest)
		if len(fields) < 16 || isVirtualInterface(name) {
			continue
		}
		values := make([]uint64, 16)
		for i := range values {
			values[i], _ = strconv.ParseUint(fields[i], 10, 64)
		}
		result[name] = netCounters{
			rxBytes: values[0], rxPackets: values[1], rxErrors: values[2], rxDropped: values[3],
			txBytes: values[8], txPackets: values[9], txErrors: values[10], txDropped: values[11],
		}
	}
	return result, scanner.Err()
}

func delta(curr, prev uint64) uint64 {
	if curr < prev {
		return 0
	}
	return curr - prev
}

// GatherNetwork returns traffic since the previous call. The first call only sets the baseline.
func (c *Collector) GatherNetwork() (*internal.NetworkMetrics, error) {
	current, err := readNetDev("/proc/net/dev")
	if err != nil {
		return nil, err
	}

	c.netMu.Lock()
	defer c.netMu.Unlock()

	result := &internal.NetworkMetrics{Interfaces: []internal.NetworkInterface{}}
	for name, curr := range current {
		prev, ok := c.prevNet[name]
		if !ok {
			continue
		}
		item := internal.NetworkInterface{
			Name:      name,
			RxBytes:   delta(curr.rxBytes, prev.rxBytes),
			TxBytes:   delta(curr.txBytes, prev.txBytes),
			RxPackets: delta(curr.rxPackets, prev.rxPackets),
			TxPackets: delta(curr.txPackets, prev.txPackets),
			Errors:    delta(curr.rxErrors, prev.rxErrors) + delta(curr.txErrors, prev.txErrors),
			Dropped:   delta(curr.rxDropped, prev.rxDropped) + delta(curr.txDropped, prev.txDropped),
		}
		result.RxBytes += item.RxBytes
		result.TxBytes += item.TxBytes
		result.RxPackets += item.RxPackets
		result.TxPackets += item.TxPackets
		result.Errors += item.Errors
		result.Dropped += item.Dropped
		// Idle interfaces (tunnels, unused NICs) would only add empty rows every second.
		if item.RxBytes+item.TxBytes+item.Errors+item.Dropped > 0 {
			result.Interfaces = append(result.Interfaces, item)
		}
	}
	c.prevNet = current

	sort.Slice(result.Interfaces, func(i, j int) bool {
		return result.Interfaces[i].RxBytes+result.Interfaces[i].TxBytes > result.Interfaces[j].RxBytes+result.Interfaces[j].TxBytes
	})
	return result, nil
}

// GatherSystem reads load average, uptime and the number of TCP sockets in use.
func GatherSystem() (*internal.SystemMetrics, error) {
	result := &internal.SystemMetrics{}

	load, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(load))
	if len(fields) < 3 {
		return nil, fmt.Errorf("invalid loadavg format")
	}
	result.Load1, _ = strconv.ParseFloat(fields[0], 64)
	result.Load5, _ = strconv.ParseFloat(fields[1], 64)
	result.Load15, _ = strconv.ParseFloat(fields[2], 64)

	if uptime, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(uptime)); len(fields) > 0 {
			seconds, _ := strconv.ParseFloat(fields[0], 64)
			result.UptimeSeconds = uint64(seconds)
		}
	}

	result.TCPConnections = readTCPInUse("/proc/net/sockstat") + readTCPInUse("/proc/net/sockstat6")
	return result, nil
}

func readTCPInUse(path string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "TCP:") && !strings.HasPrefix(line, "TCP6:") {
			continue
		}
		fields := strings.Fields(line)
		for i := 1; i+1 < len(fields); i += 2 {
			if fields[i] == "inuse" {
				value, _ := strconv.ParseUint(fields[i+1], 10, 64)
				return value
			}
		}
	}
	return 0
}
