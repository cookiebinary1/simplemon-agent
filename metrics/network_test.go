package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestReadNetDevParsesPhysicalInterfaces(t *testing.T) {
	content := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 999 9 0 0 0 0 0 0 999 9 0 0 0 0 0 0
  eth0: 1000 10 1 2 0 0 0 0 2000 20 3 4 0 0 0 0
docker0: 5 1 0 0 0 0 0 0 5 1 0 0 0 0 0 0
  wlan0: 300 3 0 0 0 0 0 0 400 4 0 0 0 0 0 0
broken line
  short: 1 2 3
`

	counters, err := readNetDev(writeTempFile(t, content))
	if err != nil {
		t.Fatalf("readNetDev: %v", err)
	}

	if len(counters) != 2 {
		t.Fatalf("expected 2 physical interfaces, got %d: %#v", len(counters), counters)
	}

	eth0, ok := counters["eth0"]
	if !ok {
		t.Fatal("eth0 missing")
	}
	if eth0.rxBytes != 1000 || eth0.rxPackets != 10 || eth0.rxErrors != 1 || eth0.rxDropped != 2 {
		t.Errorf("unexpected rx counters: %#v", eth0)
	}
	if eth0.txBytes != 2000 || eth0.txPackets != 20 || eth0.txErrors != 3 || eth0.txDropped != 4 {
		t.Errorf("unexpected tx counters: %#v", eth0)
	}

	if _, ok := counters["lo"]; ok {
		t.Error("loopback must be ignored")
	}
	if _, ok := counters["docker0"]; ok {
		t.Error("docker bridge must be ignored")
	}
}

func TestReadNetDevMissingFile(t *testing.T) {
	if _, err := readNetDev(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestDeltaHandlesCounterReset(t *testing.T) {
	if got := delta(120, 100); got != 20 {
		t.Errorf("expected 20, got %d", got)
	}
	if got := delta(5, 100); got != 0 {
		t.Errorf("counter reset should clamp to 0, got %d", got)
	}
}

func TestReadTCPInUse(t *testing.T) {
	path := writeTempFile(t, "sockets: used 10\nTCP: inuse 7 orphan 1 tw 2 alloc 9 mem 3\nUDP: inuse 2\n")

	if got := readTCPInUse(path); got != 7 {
		t.Errorf("expected 7 TCP sockets, got %d", got)
	}
	if got := readTCPInUse(filepath.Join(t.TempDir(), "missing")); got != 0 {
		t.Errorf("missing file should return 0, got %d", got)
	}
	if got := readTCPInUse(writeTempFile(t, "no tcp data\n")); got != 0 {
		t.Errorf("file without TCP data should return 0, got %d", got)
	}
}

func TestVirtualInterfaceDetection(t *testing.T) {
	virtual := []string{"lo", "veth1234", "docker0", "br-abc", "virbr0", "cni0", "flannel.1", "cali123", "vxlan0", "tun0", "tap0", "kube-ipvs0", "lxcbr0", "vnet0"}
	for _, name := range virtual {
		if !isVirtualInterface(name) {
			t.Errorf("%q should be treated as virtual", name)
		}
	}

	for _, name := range []string{"eth0", "enp3s0", "wlan0", "eno1", "bond0"} {
		if isVirtualInterface(name) {
			t.Errorf("%q should be treated as physical", name)
		}
	}
}

func TestIsAllowedFS(t *testing.T) {
	for _, fs := range []string{"ext4", "xfs", "btrfs", "zfs", "apfs", "nfs4", "cifs"} {
		if !isAllowedFS(fs) {
			t.Errorf("%q should be allowed", fs)
		}
	}
	for _, fs := range []string{"tmpfs", "proc", "sysfs", "overlay", "devtmpfs"} {
		if isAllowedFS(fs) {
			t.Errorf("%q should be ignored", fs)
		}
	}
}
