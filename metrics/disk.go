// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Disk metrics collection for Linux systems.

package metrics

import (
	"os"
	"simplemon-agent/internal"
	"strings"
	"syscall"
)

// GatherDiskMetrics collects and returns disk usage metrics for all physical disks.
func GatherDiskMetrics() ([]internal.DiskMetric, error) {
	// Get list of mount points
	mounts, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, err
	}

	var diskMetrics []internal.DiskMetric
	lines := strings.Split(string(mounts), "\n")

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		mountPoint := fields[1]
		fsType := fields[2]

		// Skip virtual/unsupported filesystems
		if !isAllowedFS(fsType) {
			continue
		}

		info, err := os.Stat(mountPoint)
		if err != nil || !info.IsDir() {
			continue
		}

		var stat syscall.Statfs_t
		err = syscall.Statfs(mountPoint, &stat)
		if err != nil {
			continue
		}

		// Calculate sizes in bytes
		total := stat.Blocks * uint64(stat.Bsize)
		free := stat.Bfree * uint64(stat.Bsize)
		used := total - free

		diskMetrics = append(diskMetrics, internal.DiskMetric{
			Path:   mountPoint,
			Type:   "Physical",
			Fstype: fsType,
			Total:  total,
			Free:   free,
			Used:   used,
			UsedRelative: func() float64 {
				if total == 0 {
					return 0
				}
				return (float64(used) / float64(total)) * 100
			}(),
		})
	}

	return diskMetrics, nil
}

// allowedTypes is a set of filesystem types to collect metrics for
var allowedTypes = map[string]struct{}{
	"ext4":       {},
	"ext3":       {},
	"ext2":       {},
	"xfs":        {},
	"btrfs":      {},
	"zfs":        {},
	"vfat":       {},
	"ntfs":       {},
	"hfsplus":    {},
	"apfs":       {},
	"fuse.sshfs": {},
	"nfs":        {},
	"nfs4":       {},
	"cifs":       {},
}

// isAllowedFS checks if a filesystem type should be monitored.
func isAllowedFS(fsType string) bool {
	_, ok := allowedTypes[fsType]
	return ok
}
