// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Static host information (OS, hardware, virtualization, addresses).

package metrics

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cookiebinary1/simplemon-agent/internal"
)

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func osRelease() (name, id string) {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return runtime.GOOS, ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"'`)
		switch key {
		case "PRETTY_NAME":
			name = value
		case "ID":
			id = value
		}
	}
	if name == "" {
		name = runtime.GOOS
	}
	return name, id
}

func cpuInfo() (model string, cores, threads int) {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", runtime.NumCPU(), runtime.NumCPU()
	}
	defer file.Close()
	physical := map[string]bool{}
	var physicalID string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "processor":
			threads++
		case "model name", "Model", "Hardware":
			if model == "" {
				model = value
			}
		case "physical id":
			physicalID = value
		case "core id":
			physical[physicalID+"/"+value] = true
		}
	}
	cores = len(physical)
	if cores == 0 {
		cores = threads
	}
	if threads == 0 {
		threads = runtime.NumCPU()
		cores = threads
	}
	return model, cores, threads
}

func memoryTotals() (memory, swap uint64) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			memory = value * 1024
		case "SwapTotal:":
			swap = value * 1024
		}
	}
	return memory, swap
}

// virtualization detects containers first, then hypervisors from DMI and CPU flags.
func virtualization(vendor, product string) string {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}
	if env := readTrimmed("/proc/1/environ"); strings.Contains(env, "container=lxc") {
		return "lxc"
	}
	if cgroup := readTrimmed("/proc/1/cgroup"); strings.Contains(cgroup, "docker") || strings.Contains(cgroup, "kubepods") {
		return "container"
	}
	dmi := strings.ToLower(vendor + " " + product + " " + readTrimmed("/sys/class/dmi/id/bios_vendor"))
	for _, known := range []struct{ match, name string }{
		{"kvm", "kvm"}, {"qemu", "kvm"}, {"vmware", "vmware"}, {"virtualbox", "virtualbox"},
		{"microsoft corporation", "hyper-v"}, {"xen", "xen"}, {"amazon ec2", "aws"}, {"google", "gce"},
		{"hetzner", "kvm"}, {"digitalocean", "kvm"}, {"openstack", "kvm"}, {"parallels", "parallels"},
	} {
		if strings.Contains(dmi, known.match) {
			return known.name
		}
	}
	if strings.Contains(readTrimmed("/proc/cpuinfo"), " hypervisor") {
		return "vm"
	}
	return "none"
}

func interfaceAddresses() []internal.NetworkAddresses {
	result := []internal.NetworkAddresses{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	for _, iface := range interfaces {
		if isVirtualInterface(iface.Name) || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		item := internal.NetworkAddresses{Name: iface.Name, MAC: iface.HardwareAddr.String(), Addresses: []string{}}
		if speed, err := strconv.Atoi(readTrimmed("/sys/class/net/" + iface.Name + "/speed")); err == nil && speed > 0 {
			item.SpeedMbps = speed
		}
		for _, addr := range addrs {
			item.Addresses = append(item.Addresses, addr.String())
		}
		if len(item.Addresses) > 0 {
			result = append(result, item)
		}
	}
	return result
}

func timezone() string {
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(link, "zoneinfo/"); ok {
			return zone
		}
	}
	if zone := readTrimmed("/etc/timezone"); zone != "" {
		return zone
	}
	name, _ := time.Now().Zone()
	return name
}

// GatherSystemInfo reads static host details. It only reads local files, no commands.
func GatherSystemInfo() *internal.SystemInfo {
	info := &internal.SystemInfo{Arch: runtime.GOARCH, Timezone: timezone()}
	info.Hostname, _ = os.Hostname()
	info.OS, info.OSID = osRelease()

	info.Kernel = readTrimmed("/proc/sys/kernel/osrelease")
	if uptime := strings.Fields(readTrimmed("/proc/uptime")); len(uptime) > 0 {
		seconds, _ := strconv.ParseFloat(uptime[0], 64)
		info.BootTime = time.Now().Add(-time.Duration(seconds * float64(time.Second))).Unix()
	}
	info.CPUModel, info.CPUCores, info.CPUThreads = cpuInfo()
	info.MemoryTotal, info.SwapTotal = memoryTotals()
	info.Vendor = readTrimmed(filepath.Join("/sys/class/dmi/id", "sys_vendor"))
	info.Product = readTrimmed(filepath.Join("/sys/class/dmi/id", "product_name"))
	info.Virtualization = virtualization(info.Vendor, info.Product)
	info.Interfaces = interfaceAddresses()
	return info
}
