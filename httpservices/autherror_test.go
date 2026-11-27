package httpservices

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnauthorizedReturnsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Invalid Server-Token"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	var authErr *AuthError
	_, err := NewWSClient("ws"+strings.TrimPrefix(srv.URL, "http")+"/incoming", "key", "token", "1.0.0")
	if !errors.As(err, &authErr) {
		t.Fatalf("websocket: expected AuthError, got %v", err)
	}

	client := NewClient(srv.URL, "key", "token", "host", "1.0.0")
	if err := client.SendMetrics([]int{1}); !errors.As(err, &authErr) {
		t.Fatalf("batch: expected AuthError, got %v", err)
	}
}
