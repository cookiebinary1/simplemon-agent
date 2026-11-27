package config

import (
	"os"
	"path/filepath"
	"testing"
)

func load(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "simplemon.conf")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSecureDefaultsToTrue(t *testing.T) {
	if cfg := load(t, `{"serviceHost":"h","serverToken":"t"}`); !cfg.Secure {
		t.Fatal("missing secure must mean true")
	}
	if cfg := load(t, `{"serviceHost":"h","serverToken":"t","secure":false}`); cfg.Secure {
		t.Fatal("explicit secure=false was ignored")
	}
}

func TestDefaultsAreValid(t *testing.T) {
	cfg := load(t, `{"serviceHost":"h","serverToken":"t"}`)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
