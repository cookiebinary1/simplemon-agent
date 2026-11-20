// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT

package agent

import (
	"simplemon-agent/internal"
	"sort"
)

// maxStoredProcesses limits process lists in one stored sample.
const maxStoredProcesses = 10

// aggregateSamples merges per-second samples into one stored sample.
// Gauges are averaged, counters (I/O, network) are summed over the window,
// and slow values (disks, system, info) come from the newest sample.
// Process lists are kept only when withProcesses is true.
func aggregateSamples(samples []MetricEntry, withProcesses bool) MetricEntry {
	last := samples[len(samples)-1]
	n := float64(len(samples))

	out := MetricEntry{
		Timestamp: last.Timestamp,
		Disks:     last.Disks,
		System:    last.System,
	}

	var (
		cpu     internal.CPUMetrics
		mem     internal.MemoryMetrics
		io      internal.IoMetrics
		net     *internal.NetworkMetrics
		netIdx  = map[string]int{}
		cpuN    float64
		memN    float64
		memAvl  float64
		memUsed float64
	)

	for _, s := range samples {
		out.DeltaTime += s.DeltaTime
		if out.Info == nil && s.Info != nil {
			out.Info = s.Info
		}

		if s.CPU != nil {
			cpuN++
			cpu.OverallUsage += s.CPU.OverallUsage
			cpu.OverallPercent += s.CPU.OverallPercent
			for i, v := range s.CPU.CoreUsage {
				if i >= len(cpu.CoreUsage) {
					cpu.CoreUsage = append(cpu.CoreUsage, 0)
				}
				cpu.CoreUsage[i] += v
			}
		}

		if s.Memory != nil {
			memN++
			mem.Total = s.Memory.Total
			memAvl += float64(s.Memory.Available)
			memUsed += float64(s.Memory.Used)
			mem.UsedRelative += s.Memory.UsedRelative
		}

		if s.IO != nil {
			io.TotalRead += s.IO.TotalRead
			io.TotalWrite += s.IO.TotalWrite
			io.ReadOps += s.IO.ReadOps
			io.WriteOps += s.IO.WriteOps
		}

		if s.Network != nil {
			if net == nil {
				net = &internal.NetworkMetrics{}
			}
			net.RxBytes += s.Network.RxBytes
			net.TxBytes += s.Network.TxBytes
			net.RxPackets += s.Network.RxPackets
			net.TxPackets += s.Network.TxPackets
			net.Errors += s.Network.Errors
			net.Dropped += s.Network.Dropped
			for _, iface := range s.Network.Interfaces {
				i, ok := netIdx[iface.Name]
				if !ok {
					i = len(net.Interfaces)
					netIdx[iface.Name] = i
					net.Interfaces = append(net.Interfaces, internal.NetworkInterface{Name: iface.Name})
				}
				sum := &net.Interfaces[i]
				sum.RxBytes += iface.RxBytes
				sum.TxBytes += iface.TxBytes
				sum.RxPackets += iface.RxPackets
				sum.TxPackets += iface.TxPackets
				sum.Errors += iface.Errors
				sum.Dropped += iface.Dropped
			}
		}
	}

	if cpuN > 0 {
		cpu.OverallUsage /= cpuN
		cpu.OverallPercent /= cpuN
		for i := range cpu.CoreUsage {
			cpu.CoreUsage[i] /= cpuN
		}
	}
	if memN > 0 {
		mem.Available = uint64(memAvl / memN)
		mem.Used = uint64(memUsed / memN)
		mem.UsedRelative /= memN
	}

	cpu.Processes = []internal.ProcessInfo{}
	mem.Processes = []internal.ProcessInfo{}
	io.Processes = []internal.ProcessInfo{}
	if withProcesses {
		cpu.Processes = averageProcesses(samples, n, func(s MetricEntry) []internal.ProcessInfo {
			if s.CPU == nil {
				return nil
			}
			return s.CPU.Processes
		})
		mem.Processes = averageProcesses(samples, n, func(s MetricEntry) []internal.ProcessInfo {
			if s.Memory == nil {
				return nil
			}
			return s.Memory.Processes
		})
		io.Processes = averageProcesses(samples, n, func(s MetricEntry) []internal.ProcessInfo {
			if s.IO == nil {
				return nil
			}
			return s.IO.Processes
		})
	}

	out.CPU = &cpu
	out.Memory = &mem
	out.IO = &io
	out.Network = net
	return out
}

// averageProcesses averages per-sample process values over the window.
// A process missing from a sample counts as zero for that sample, so the
// result keeps the same "per sample" meaning as a single measurement.
func averageProcesses(samples []MetricEntry, n float64, pick func(MetricEntry) []internal.ProcessInfo) []internal.ProcessInfo {
	byName := map[string]*internal.ProcessInfo{}
	var order []string

	for _, s := range samples {
		for _, p := range pick(s) {
			sum, ok := byName[p.Name]
			if !ok {
				sum = &internal.ProcessInfo{Name: p.Name}
				byName[p.Name] = sum
				order = append(order, p.Name)
			}
			sum.Value += p.Value
			sum.ValuePercent += p.ValuePercent
			sum.ValueBytes += p.ValueBytes
			sum.ReadBytes += p.ReadBytes
			sum.WriteBytes += p.WriteBytes
			sum.ReadOps += p.ReadOps
			sum.WriteOps += p.WriteOps
			if p.Count > sum.Count {
				sum.Count = p.Count
			}
		}
	}

	out := make([]internal.ProcessInfo, 0, len(order))
	for _, name := range order {
		p := byName[name]
		p.Value /= n
		p.ValuePercent /= n
		p.ValueBytes = uint64(float64(p.ValueBytes) / n)
		p.ReadBytes = uint64(float64(p.ReadBytes) / n)
		p.WriteBytes = uint64(float64(p.WriteBytes) / n)
		p.ReadOps = uint64(float64(p.ReadOps) / n)
		p.WriteOps = uint64(float64(p.WriteOps) / n)
		out = append(out, *p)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Value > out[j].Value })
	if len(out) > maxStoredProcesses {
		out = out[:maxStoredProcesses]
	}
	return out
}
