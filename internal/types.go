// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Package internal contains shared types for the simplemon agent.

package internal

// DiskMetric represents the metrics for a single disk or partition.
type DiskMetric struct {
	Path         string  `json:"path"`
	Type         string  `json:"type"`
	Fstype       string  `json:"fstype"`
	Total        uint64  `json:"total"`
	Free         uint64  `json:"free"`
	Used         uint64  `json:"used"`
	UsedRelative float64 `json:"used_relative,omitempty"`
}

// MemoryMetrics represents the metrics for system memory.
type MemoryMetrics struct {
	Total        uint64        `json:"total"`
	Available    uint64        `json:"available"`
	Used         uint64        `json:"used"`
	UsedRelative float64       `json:"used_relative,omitempty"`
	Processes    []ProcessInfo `json:"processes"`
}

// CPUMetrics represents the metrics for CPU usage.
type CPUMetrics struct {
	OverallUsage   float64       `json:"overall_usage"`
	OverallPercent float64       `json:"overall_percent,omitempty"`
	CoreUsage      []float64     `json:"core_usage"`
	Processes      []ProcessInfo `json:"processes"`
}

// ProcessInfo represents the information about a process.
type ProcessInfo struct {
	Name         string  `json:"name"`
	Value        float64 `json:"value"` // This can be either CPU or Mem depending on context
	Count        int     `json:"count"` // Number of processes combined
	ReadBytes    uint64  `json:"read_bytes,omitempty"`
	WriteBytes   uint64  `json:"write_bytes,omitempty"`
	ReadOps      uint64  `json:"read_ops,omitempty"`
	WriteOps     uint64  `json:"write_ops,omitempty"`
	ValuePercent float64 `json:"value_percent,omitempty"`
	ValueBytes   uint64  `json:"value_bytes,omitempty"`
}

// IoMetrics represents the metrics for I/O usage.
type IoMetrics struct {
	TotalRead  uint64        `json:"total_read"`
	TotalWrite uint64        `json:"total_write"`
	ReadOps    uint64        `json:"read_ops"`
	WriteOps   uint64        `json:"write_ops"`
	Processes  []ProcessInfo `json:"processes"`
}

// NetworkInterface holds traffic counted since the previous sample for one interface.
type NetworkInterface struct {
	Name      string `json:"name"`
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	TxPackets uint64 `json:"tx_packets"`
	Errors    uint64 `json:"errors"`
	Dropped   uint64 `json:"dropped"`
}

// NetworkMetrics holds traffic since the previous sample summed over physical interfaces.
type NetworkMetrics struct {
	RxBytes    uint64             `json:"rx_bytes"`
	TxBytes    uint64             `json:"tx_bytes"`
	RxPackets  uint64             `json:"rx_packets"`
	TxPackets  uint64             `json:"tx_packets"`
	Errors     uint64             `json:"errors"`
	Dropped    uint64             `json:"dropped"`
	Interfaces []NetworkInterface `json:"interfaces"`
}

// SystemMetrics holds load average, uptime and TCP socket count.
type SystemMetrics struct {
	Load1          float64 `json:"load1"`
	Load5          float64 `json:"load5"`
	Load15         float64 `json:"load15"`
	UptimeSeconds  uint64  `json:"uptime_seconds"`
	TCPConnections uint64  `json:"tcp_connections"`
}

// SystemInfo describes the host (neofetch-like). Sent at start and then hourly.
type SystemInfo struct {
	Hostname       string             `json:"hostname"`
	OS             string             `json:"os"`
	OSID           string             `json:"os_id,omitempty"`
	Kernel         string             `json:"kernel"`
	Arch           string             `json:"arch"`
	Timezone       string             `json:"timezone,omitempty"`
	BootTime       int64              `json:"boot_time,omitempty"`
	CPUModel       string             `json:"cpu_model,omitempty"`
	CPUCores       int                `json:"cpu_cores"`
	CPUThreads     int                `json:"cpu_threads"`
	MemoryTotal    uint64             `json:"memory_total"`
	SwapTotal      uint64             `json:"swap_total"`
	Vendor         string             `json:"vendor,omitempty"`
	Product        string             `json:"product,omitempty"`
	Virtualization string             `json:"virtualization"`
	Interfaces     []NetworkAddresses `json:"interfaces"`
}

// NetworkAddresses lists the addresses of one network interface.
type NetworkAddresses struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac,omitempty"`
	Addresses []string `json:"addresses"`
	SpeedMbps int      `json:"speed_mbps,omitempty"` // Link speed; 0 when unknown (common on VMs)
}
