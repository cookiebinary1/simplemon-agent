package main

import (
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"time"
)

var conn *websocket.Conn

func connectWebSocket(url string) error {
	var dialer *websocket.Dialer
	var err error
	conn, _, err = dialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("websocket dial: %v", err)
	}
	return nil
}

type SystemMetrics struct {
	DiskUsage    float64   `json:"disk_usage"`
	CPUUsage     float64   `json:"cpu_usage"`
	CPUCoreUsage []float64 `json:"cpu_core_usage"`
	MemoryUsage  float64   `json:"memory_usage"`
}

type DiskMetric struct {
	Path        string  `json:"path"`
	Type        string  `json:"type"`
	Fstype      string  `json:"fstype"`
	Total       uint64  `json:"total"`
	Free        uint64  `json:"free"`
	Used        uint64  `json:"used"`
	UsedPercent float64 `json:"used_percent"`
}

func isPhysical(partition disk.PartitionStat) bool {
	physicalTypes := []string{"ext4", "ext3", "ntfs", "fat32", "xfs", "apfs", "btrfs", "zfs"}
	for _, t := range physicalTypes {
		if partition.Fstype == t {
			return true
		}
	}
	return false
}

func gatherDiskMetrics() ([]DiskMetric, error) {
	var metrics []DiskMetric

	partitions, err := disk.Partitions(false)

	fmt.Printf("Partitions: %v\n", partitions)
	if err != nil {
		return nil, err
	}

	for _, partition := range partitions {
		usageStat, err := disk.Usage(partition.Mountpoint)
		if err != nil {
			continue
		}
		metric := DiskMetric{
			Path:        partition.Mountpoint,
			Type:        "Virtual", // Predvolený typ
			Total:       usageStat.Total,
			Free:        usageStat.Free,
			Used:        usageStat.Used,
			UsedPercent: usageStat.UsedPercent,
			Fstype:      partition.Fstype,
		}
		if isPhysical(partition) {
			metric.Type = "Physical"
		}
		metrics = append(metrics, metric)
	}

	return metrics, nil
}

func main() {

	websocketURL := "ws://localhost:8080" // Zmeň na tvoju WebSocket URL
	for {

		systemMetrics, err := gatherMetrics()
		if err != nil {
			fmt.Println("Error gathering system metrics:", err)
			return
		}

		diskMetrics, err := gatherDiskMetrics()
		if err != nil {
			fmt.Println("Error gathering disk metrics:", err)
			return
		}

		allMetrics := struct {
			SystemMetrics *SystemMetrics `json:"system"`
			DiskMetrics   []DiskMetric   `json:"disks"`
		}{
			SystemMetrics: systemMetrics,
			DiskMetrics:   diskMetrics,
		}

		err = sendMetrics(websocketURL, allMetrics)
		if err != nil {
			fmt.Println("Error sending metrics:", err)
			time.Sleep(1 * time.Second)
			continue
		}

		fmt.Println("Metrics were sent")

		time.Sleep(1 * time.Second)
	}
}

func gatherMetrics() (*SystemMetrics, error) {
	// Disk usage
	d, err := disk.Usage("/")
	if err != nil {
		return nil, err
	}

	// Total CPU usage (average)
	cpuPercent, err := cpu.Percent(1*time.Second, false)
	if err != nil {
		return nil, err
	}

	// Use of individual CPU cores
	cpuCorePercent, err := cpu.Percent(1*time.Second, true)
	if err != nil {
		return nil, err
	}

	// Memory usage
	m, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	metrics := &SystemMetrics{
		DiskUsage:    d.UsedPercent,
		CPUUsage:     cpuPercent[0],
		CPUCoreUsage: cpuCorePercent,
		MemoryUsage:  m.UsedPercent,
	}

	return metrics, nil
}

func sendMetrics(url string, metrics interface{}) error {
	if conn == nil {
		if err := connectWebSocket(url); err != nil {
			return err
		}
	}

	jsonData, err := json.Marshal(metrics)
	if err != nil {
		return err
	}

	err = conn.WriteMessage(websocket.TextMessage, jsonData)
	if err != nil {
		_ = conn.Close()
		conn = nil
		return fmt.Errorf("websocket write: %v, trying to reconnect...", err)
	}

	return nil
}
