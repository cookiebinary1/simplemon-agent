package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatherDisksSkipsBindMountsAndUnescapes(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "with space")
	second := filepath.Join(dir, "bind")
	for _, d := range []string{first, second} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	escaped := strings.ReplaceAll(first, " ", `\040`)
	mounts := "/dev/sdb " + escaped + " ext4 rw 0 0\n" +
		"/dev/sdb " + second + " ext4 rw 0 0\n" +
		"proc /proc proc rw 0 0\n"
	path := filepath.Join(dir, "mounts")
	if err := os.WriteFile(path, []byte(mounts), 0o600); err != nil {
		t.Fatal(err)
	}

	disks, err := gatherDisks(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 1 || disks[0].Path != first {
		t.Fatalf("disks = %+v, want only %q", disks, first)
	}
	if disks[0].Total != disks[0].Used+disks[0].Free {
		t.Errorf("total %d != used %d + free %d", disks[0].Total, disks[0].Used, disks[0].Free)
	}
}
