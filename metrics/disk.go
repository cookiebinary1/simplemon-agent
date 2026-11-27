// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Disk metrics collection for Linux systems.

package metrics

import (
	"os"
	"strings"
	"syscall"

	"github.com/cookiebinary1/simplemon-agent/internal"
)

// GatherDiskMetrics returns usage of every mounted disk filesystem.
func GatherDiskMetrics() ([]internal.DiskMetric, error) {
	return gatherDisks("/proc/mounts")
}

func gatherDisks(mountsPath string) ([]internal.DiskMetric, error) {
	mounts, err := os.ReadFile(mountsPath)
	if err != nil {
		return nil, err
	}

	var diskMetrics []internal.DiskMetric
	// A filesystem mounted more than once (bind mounts) has one device ID.
	// It is reported once, under its first mount point in /proc/mounts.
	seen := map[uint64]bool{}
	lines := strings.Split(string(mounts), "\n")

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		mountPoint := mountUnescaper.Replace(fields[1])
		fsType := fields[2]

		if !isAllowedFS(fsType) {
			continue
		}

		info, err := os.Stat(mountPoint)
		if err != nil || !info.IsDir() {
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			if seen[uint64(st.Dev)] {
				continue
			}
			seen[uint64(st.Dev)] = true
		}

		var stat syscall.Statfs_t
		err = syscall.Statfs(mountPoint, &stat)
		if err != nil {
			continue
		}

		// Match df: free space is what unprivileged users can still write.
		// Blocks reserved for root (5 % on ext4 by default) count as neither,
		// so the volume reads full when writes start failing.
		used := (stat.Blocks - stat.Bfree) * uint64(stat.Bsize)
		free := stat.Bavail * uint64(stat.Bsize)
		total := used + free

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

// /proc/mounts escapes space, tab, newline and backslash in paths as octal.
var mountUnescaper = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

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
