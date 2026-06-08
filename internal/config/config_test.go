package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefault_ReasonableValues(t *testing.T) {
	cfg := Default()
	if cfg.ArgusBaseURL != "http://127.0.0.1:7743" {
		t.Fatalf("ArgusBaseURL = %s", cfg.ArgusBaseURL)
	}
	if cfg.ListenAddr != "127.0.0.1:7744" {
		t.Fatalf("ListenAddr = %s", cfg.ListenAddr)
	}
	if cfg.NotifyDeadlineMs != 300000 {
		t.Fatalf("NotifyDeadlineMs = %d, want 300000", cfg.NotifyDeadlineMs)
	}
	if cfg.MCPHeartbeat.Minutes() != 5 {
		t.Fatalf("MCPHeartbeat = %v", cfg.MCPHeartbeat)
	}
	if cfg.StateDir == "" {
		t.Fatalf("StateDir empty")
	}
	if cfg.AutoInjectEnabled != true {
		t.Fatalf("AutoInjectEnabled = %v, want true", cfg.AutoInjectEnabled)
	}
	if cfg.ReconcileInterval.Seconds() != 60 {
		t.Fatalf("ReconcileInterval = %v, want 60s", cfg.ReconcileInterval)
	}
}

func TestLoadToken_FileMissing(t *testing.T) {
	tmp := t.TempDir()
	cfg := &Config{StateDir: tmp}
	_, err := cfg.LoadToken()
	if err == nil {
		t.Fatalf("expected error for missing token")
	}
	if !strings.Contains(err.Error(), "argus token mint") {
		t.Fatalf("error doesn't include the suggested fix: %v", err)
	}
}

func TestLoadToken_FileEmpty(t *testing.T) {
	tmp := t.TempDir()
	cfg := &Config{StateDir: tmp}
	path := cfg.TokenPath()
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := cfg.LoadToken()
	if err == nil {
		t.Fatalf("expected error for empty token")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error wording: %v", err)
	}
}

func TestLoadToken_FileValid(t *testing.T) {
	tmp := t.TempDir()
	cfg := &Config{StateDir: tmp}
	path := cfg.TokenPath()
	if err := os.WriteFile(path, []byte("secret-token-value\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tok, err := cfg.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if tok != "secret-token-value" {
		t.Fatalf("token = %q (should strip whitespace)", tok)
	}
}

func TestEnsureStateDir(t *testing.T) {
	tmp := t.TempDir()
	cfg := &Config{StateDir: filepath.Join(tmp, "nested", "dir")}
	if err := cfg.EnsureStateDir(); err != nil {
		t.Fatalf("EnsureStateDir: %v", err)
	}
	info, err := os.Stat(cfg.StateDir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("not a dir")
	}
}
