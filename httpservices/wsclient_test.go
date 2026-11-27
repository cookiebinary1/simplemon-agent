package httpservices

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A server that stops reading must not block writers or Close forever.
func TestWriteToStalledServerTimesOut(t *testing.T) {
	old := wsWriteTimeout
	wsWriteTimeout = 200 * time.Millisecond
	defer func() { wsWriteTimeout = old }()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		<-release // never read, so the client's send buffer fills up
	}))
	defer srv.Close()
	defer close(release)

	c, err := NewWSClient("ws"+strings.TrimPrefix(srv.URL, "http"), "", "token", "test")
	if err != nil {
		t.Fatal(err)
	}

	payload := strings.Repeat("x", 1<<20)
	done := make(chan error, 1)
	go func() {
		for {
			if err := c.SendMessage(payload); err != nil {
				done <- err
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("write to a stalled connection did not time out")
	}

	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked")
	}
}
