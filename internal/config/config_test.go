package config

import (
	"os"
	"path/filepath"
	"testing"
)

// helper to write a temp file with the given content.
// Returns the path with forward slashes so it's safe to embed in YAML.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(path)
}

// yamlPath converts a path to forward slashes for safe YAML embedding.
func yamlPath(p string) string {
	return filepath.ToSlash(p)
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()

	// Create dummy host key and public key files.
	hostKey := writeTempFile(t, dir, "host_key", "fake-key")
	pubKey := writeTempFile(t, dir, "alice.pub", "ssh-ed25519 AAAA...")

	cfgYAML := `
listen: "0.0.0.0:3333"
host_key: "` + hostKey + `"
upstream:
  host: "10.0.0.1"
  port: 22
  username: "root"
  password: "secret"
users:
  - username: "alice"
    password: "pw"
    public_key: "` + pubKey + `"
  - username: "bob"
    password: "bobpw"
log_dir: "` + yamlPath(filepath.Join(dir, "logs")) + `"
`

	cfgPath := writeTempFile(t, dir, "config.yaml", cfgYAML)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Listen != "0.0.0.0:3333" {
		t.Errorf("listen = %q, want 0.0.0.0:3333", cfg.Listen)
	}
	if cfg.Upstream.Host != "10.0.0.1" {
		t.Errorf("upstream.host = %q, want 10.0.0.1", cfg.Upstream.Host)
	}
	if cfg.Upstream.Addr() != "10.0.0.1:22" {
		t.Errorf("upstream.Addr() = %q, want 10.0.0.1:22", cfg.Upstream.Addr())
	}
	if len(cfg.Users) != 2 {
		t.Errorf("len(users) = %d, want 2", len(cfg.Users))
	}
	if cfg.LLMEnabled() {
		t.Error("LLMEnabled() = true, want false (no key)")
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	hostKey := writeTempFile(t, dir, "host_key", "fake-key")

	cfgYAML := `
host_key: "` + hostKey + `"
upstream:
  host: "upstream"
  username: "root"
  password: "pw"
users:
  - username: "alice"
    password: "pw"
`
	cfgPath := writeTempFile(t, dir, "config.yaml", cfgYAML)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Listen != "0.0.0.0:2022" {
		t.Errorf("listen = %q, want default 0.0.0.0:2022", cfg.Listen)
	}
	if cfg.Upstream.Port != 22 {
		t.Errorf("upstream.port = %d, want default 22", cfg.Upstream.Port)
	}
}

func TestLoadLLMDefaults(t *testing.T) {
	dir := t.TempDir()
	hostKey := writeTempFile(t, dir, "host_key", "fake-key")

	cfgYAML := `
host_key: "` + hostKey + `"
upstream:
  host: "upstream"
  username: "root"
  password: "pw"
users:
  - username: "alice"
    password: "pw"
llm:
  api_key: "sk-ant-test-key"
`
	cfgPath := writeTempFile(t, dir, "config.yaml", cfgYAML)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cfg.LLMEnabled() {
		t.Error("LLMEnabled() = false, want true")
	}
	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("llm.provider = %q, want anthropic (default)", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "claude-sonnet-4-6" {
		t.Errorf("llm.model = %q, want claude-sonnet-4-6 (default)", cfg.LLM.Model)
	}
}

func TestLoadLLMOpenAI(t *testing.T) {
	dir := t.TempDir()
	hostKey := writeTempFile(t, dir, "host_key", "fake-key")

	cfgYAML := `
host_key: "` + hostKey + `"
upstream:
  host: "upstream"
  username: "root"
  password: "pw"
users:
  - username: "alice"
    password: "pw"
llm:
  provider: "openai"
  api_key: "sk-openai-test"
`
	cfgPath := writeTempFile(t, dir, "config.yaml", cfgYAML)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LLM.Provider != "openai" {
		t.Errorf("llm.provider = %q, want openai", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "gpt-4o" {
		t.Errorf("llm.model = %q, want gpt-4o (default)", cfg.LLM.Model)
	}
}

func TestLoadMissingFields(t *testing.T) {
	dir := t.TempDir()

	// No host_key, no upstream, no users.
	cfgYAML := `listen: ":2022"`
	cfgPath := writeTempFile(t, dir, "config.yaml", cfgYAML)

	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing required fields")
	}

	for _, want := range []string{"host_key", "upstream.host", "upstream.username", "at least one user"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestLoadDuplicateUser(t *testing.T) {
	dir := t.TempDir()
	hostKey := writeTempFile(t, dir, "host_key", "fake-key")

	cfgYAML := `
host_key: "` + hostKey + `"
upstream:
  host: "upstream"
  username: "root"
  password: "pw"
users:
  - username: "alice"
    password: "pw"
  - username: "alice"
    password: "pw2"
`
	cfgPath := writeTempFile(t, dir, "config.yaml", cfgYAML)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for duplicate username")
	}
	if !contains(err.Error(), "duplicate") {
		t.Errorf("error %q should mention 'duplicate'", err)
	}
}

func TestLoadBadProvider(t *testing.T) {
	dir := t.TempDir()
	hostKey := writeTempFile(t, dir, "host_key", "fake-key")

	cfgYAML := `
host_key: "` + hostKey + `"
upstream:
  host: "upstream"
  username: "root"
  password: "pw"
users:
  - username: "alice"
    password: "pw"
llm:
  provider: "gemini"
  api_key: "some-key"
`
	cfgPath := writeTempFile(t, dir, "config.yaml", cfgYAML)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if !contains(err.Error(), "unsupported provider") {
		t.Errorf("error %q should mention 'unsupported provider'", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
