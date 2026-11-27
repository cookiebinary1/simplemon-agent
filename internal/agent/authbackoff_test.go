package agent

import (
	"testing"
	"time"
)

func TestAuthBackoff(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i, w := range want {
		if got := authBackoff(int32(i + 1)); got != w {
			t.Fatalf("failures=%d: got %v, want %v", i+1, got, w)
		}
	}
	if got := authBackoff(1000); got != authBackoffMax {
		t.Fatalf("large count: got %v", got)
	}
}
