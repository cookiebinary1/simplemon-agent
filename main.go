package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"net/http"
	"time"
)

type SystemMetrics struct {
	DiskUsage    float64   `json:"disk_usage"`
	CPUUsage     float64   `json:"cpu_usage"`      // Celkové priemerné využitie CPU
	CPUCoreUsage []float64 `json:"cpu_core_usage"` // Využitie jednotlivých jadier
	MemoryUsage  float64   `json:"memory_usage"`
}

// Struktúra pre uchovávanie metrík o disku
type DiskMetric struct {
	Path        string  `json:"path"`
	Type        string  `json:"type"` // Pridané pre určenie typu disku
	Total       uint64  `json:"total"`
	Free        uint64  `json:"free"`
	Used        uint64  `json:"used"`
	UsedPercent float64 `json:"used_percent"`
}

func isPhysical(partition disk.PartitionStat) bool {
	// Toto je veľmi základná heuristika a možno ju bude treba prispôsobiť
	// podľa konkrétnych potrieb alebo konfigurácie systému
	physicalTypes := []string{"ext4", "ext3", "ntfs", "fat32", "xfs"}
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
		}
		if isPhysical(partition) {
			metric.Type = "Physical"
		}
		metrics = append(metrics, metric)
	}

	return metrics, nil
}

func main() {
	for {
		// Získanie systémových metrík
		systemMetrics, err := gatherMetrics()
		if err != nil {
			fmt.Println("Error gathering system metrics:", err)
			return
		}

		// Získanie metrík disku
		diskMetrics, err := gatherDiskMetrics()
		if err != nil {
			fmt.Println("Error gathering disk metrics:", err)
			return
		}

		// Kombinovanie systémových metrík a metrík disku do jedného objektu
		allMetrics := struct {
			SystemMetrics *SystemMetrics `json:"system"`
			DiskMetrics   []DiskMetric   `json:"disks"`
		}{
			SystemMetrics: systemMetrics,
			DiskMetrics:   diskMetrics,
		}

		// Odoslanie kombinovaných metrík
		err = sendMetrics("http://simplemon-server.test/api/metrics", allMetrics)
		if err != nil {
			fmt.Println("Error sending metrics:", err)
			return
		}

		// Čakanie pred ďalšou iteráciou
		time.Sleep(1 * time.Second) // Zmenené na 60 sekúnd pre ilustráciu
	}
}

func gatherMetrics() (*SystemMetrics, error) {
	// Disk usage
	d, err := disk.Usage("/")
	if err != nil {
		return nil, err
	}

	// Celkové CPU usage (priemer)
	cpuPercent, err := cpu.Percent(1*time.Second, false) // False znamená celkový priemer
	if err != nil {
		return nil, err
	}

	// Využitie jednotlivých jadier CPU
	cpuCorePercent, err := cpu.Percent(1*time.Second, true) // True znamená využitie jednotlivých jadier
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
		CPUUsage:     cpuPercent[0],  // Priemerné využitie (môže byť zavádzajúce, zvyčajne sa používa ako celkový priemer)
		CPUCoreUsage: cpuCorePercent, // Využitie jednotlivých jadier
		MemoryUsage:  m.UsedPercent,
	}

	return metrics, nil
}

func sendMetrics(url string, metrics interface{}) error {

	jsonData, err := json.Marshal(metrics)
	if err != nil {
		return err
	}

	fmt.Println(url)                     // Môžeš zakomentovať po dokončení testovania
	fmt.Printf("%s\n", string(jsonData)) // Pre výpis JSON reprezentácie odosielaných údajov

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to send metrics, server responded with status code: %d", resp.StatusCode)
	}

	return nil
}
