// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT

package metrics

import (
	"testing"
)

func TestParseCPUFields(t *testing.T) {
	tests := []struct {
		name   string
		fields []string
		want   *cpuStat
	}{
		{
			name:   "valid cpu line",
			fields: []string{"cpu", "100", "200", "300", "400", "50", "60", "70", "80", "90", "10"},
			want: &cpuStat{
				user: 100, nice: 200, system: 300, idle: 400,
				iowait: 50, irq: 60, softirq: 70, steal: 80,
				guest: 90, guestnice: 10,
			},
		},
		{
			name:   "minimal fields",
			fields: []string{"cpu", "100"},
			want:   &cpuStat{user: 100},
		},
		{
			name:   "empty fields",
			fields: []string{"cpu"},
			want:   &cpuStat{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCPUFields(tt.fields)
			if got.user != tt.want.user || got.nice != tt.want.nice ||
				got.system != tt.want.system || got.idle != tt.want.idle {
				t.Errorf("parseCPUFields() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCalculateCPUUsage(t *testing.T) {
	tests := []struct {
		name string
		prev *cpuStat
		curr *cpuStat
		want float64
	}{
		{
			name: "50% usage",
			prev: &cpuStat{user: 100, system: 100, idle: 200},
			curr: &cpuStat{user: 200, system: 200, idle: 400},
			want: 0.5,
		},
		{
			name: "0% usage (all idle)",
			prev: &cpuStat{idle: 100},
			curr: &cpuStat{idle: 200},
			want: 0.0,
		},
		{
			name: "100% usage (no idle)",
			prev: &cpuStat{user: 100, idle: 100},
			curr: &cpuStat{user: 200, idle: 100},
			want: 1.0,
		},
		{
			name: "no change",
			prev: &cpuStat{user: 100, idle: 100},
			curr: &cpuStat{user: 100, idle: 100},
			want: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateCPUUsage(tt.prev, tt.curr)
			if got != tt.want {
				t.Errorf("calculateCPUUsage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProcessGroupName(t *testing.T) {
	tests := []struct{ input, want string }{
		{"python3", "python3"},
		{"tmux: server", "tmux: server"},
		{"kworker/u8:2-events", "kworker"},
		{"ksoftirqd/0", "ksoftirqd"},
		{"/odd", "/odd"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := processGroupName(tt.input); got != tt.want {
			t.Errorf("processGroupName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestProcessStatSnapshot(t *testing.T) {
	proc, err := parseProcessStat(123, "123 (name) with spaces) S 1 2 3 4 5 6 7 8 9 10 111 222 13 14 15 16 17 18 333 20 444 22")
	if err != nil || proc.Name != "name) with spaces" || proc.cpu.utime != 111 || proc.cpu.stime != 222 || proc.cpu.startTime != 333 || proc.rssPages != 444 {
		t.Fatalf("unexpected snapshot %+v, error %v", proc, err)
	}
	if _, err := parseProcessStat(123, "123 (gone) S 1"); err == nil {
		t.Fatal("accepted truncated stat")
	}
}

func TestParseStatmShared(t *testing.T) {
	if got := parseStatmShared("1000 400 300 10 0 50 0\n"); got != 300 {
		t.Fatalf("shared = %d, want 300", got)
	}
	if got := parseStatmShared("bad"); got != 0 {
		t.Fatalf("shared = %d, want 0", got)
	}
}
