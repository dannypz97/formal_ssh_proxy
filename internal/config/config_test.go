package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempFile writes content to dir/name and returns the path with forward
// slashes so it's safe to embed in YAML on Windows.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(path)
}

// baseConfigYAML returns a minimal valid YAML config string. It creates a
// dummy host key in dir and wires up the required fields. Callers can append
// extra YAML sections (llm, extra users, etc.) to the returned string.
func baseConfigYAML(t *testing.T, dir string) string {
	t.Helper()
	hostKey := writeTempFile(t, dir, "host_key", "fake-key")
	return `
host_key: "` + hostKey + `"
upstream:
  host: "upstream"
  username: "root"
  password: "pw"
users:
  - username: "alice"
    password: "pw"
`
}

// loadFromYAML is a shorthand: writes cfgYAML to a temp file and calls Load.
func loadFromYAML(t *testing.T, dir, cfgYAML string) (*Config, error) {
	t.Helper()
	cfgPath := writeTempFile(t, dir, "config.yaml", cfgYAML)
	return Load(cfgPath)
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
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
log_dir: "` + filepath.ToSlash(filepath.Join(dir, "logs")) + `"
`

	cfg, err := loadFromYAML(t, dir, cfgYAML)
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
	cfg, err := loadFromYAML(t, dir, baseConfigYAML(t, dir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Listen != DefaultListenAddr {
		t.Errorf("listen = %q, want default %q", cfg.Listen, DefaultListenAddr)
	}
	if cfg.Upstream.Port != DefaultUpstreamPort {
		t.Errorf("upstream.port = %d, want default %d", cfg.Upstream.Port, DefaultUpstreamPort)
	}
	if cfg.LogDir != DefaultLogDir {
		t.Errorf("log_dir = %q, want default %q", cfg.LogDir, DefaultLogDir)
	}
}

func TestLoadLLMDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgYAML := baseConfigYAML(t, dir) + `
llm:
  api_key: "sk-ant-test-key"
`
	cfg, err := loadFromYAML(t, dir, cfgYAML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cfg.LLMEnabled() {
		t.Error("LLMEnabled() = false, want true")
	}
	if cfg.LLM.Provider != DefaultLLMProvider {
		t.Errorf("llm.provider = %q, want default %q", cfg.LLM.Provider, DefaultLLMProvider)
	}
	if cfg.LLM.Model != DefaultAnthropicModel {
		t.Errorf("llm.model = %q, want default %q", cfg.LLM.Model, DefaultAnthropicModel)
	}
}

func TestLoadLLMOpenAI(t *testing.T) {
	dir := t.TempDir()
	cfgYAML := baseConfigYAML(t, dir) + `
llm:
  provider: "openai"
  api_key: "sk-openai-test"
`
	cfg, err := loadFromYAML(t, dir, cfgYAML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.LLM.Provider != "openai" {
		t.Errorf("llm.provider = %q, want openai", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != DefaultOpenAIModel {
		t.Errorf("llm.model = %q, want default %q", cfg.LLM.Model, DefaultOpenAIModel)
	}
}

func TestLoadMissingFields(t *testing.T) {
	dir := t.TempDir()

	// No host_key, no upstream, no users.
	_, err := loadFromYAML(t, dir, `listen: ":2022"`)
	if err == nil {
		t.Fatal("expected error for missing required fields")
	}

	for _, want := range []string{"host_key", "upstream.host", "upstream.username", "at least one user"} {
		if !strings.Contains(err.Error(), want) {
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
	_, err := loadFromYAML(t, dir, cfgYAML)
	if err == nil {
		t.Fatal("expected error for duplicate username")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error %q should mention 'duplicate'", err)
	}
}

func TestLoadBadProvider(t *testing.T) {
	dir := t.TempDir()
	cfgYAML := baseConfigYAML(t, dir) + `
llm:
  provider: "gemini"
  api_key: "some-key"
`
	_, err := loadFromYAML(t, dir, cfgYAML)
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Errorf("error %q should mention 'unsupported provider'", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	appendYAML := func(extra string) func(string) string {
		return func(base string) string { return base + extra }
	}
	tests := []struct {
		name    string
		edit    func(base string) string
		wantErr string
	}{
		{"unknown key", appendYAML("log_directory: /tmp\n"), "log_directory"},
		{"misspelled nested key", appendYAML("llm:\n  apikey: x\n"), "apikey"},
		{"negative transcript limit", appendYAML("llm:\n  max_transcript_chars: -1\n"), "max_transcript_chars"},
		{
			"missing known_hosts file",
			func(base string) string {
				return strings.Replace(base, `  host: "upstream"`, `  host: "upstream"
  known_hosts: "/nonexistent/known_hosts"`, 1)
			},
			"known_hosts file not found",
		},
		{
			"upstream port out of range",
			func(base string) string {
				return strings.Replace(base, `  host: "upstream"`, `  host: "upstream"
  port: 70000`, 1)
			},
			"upstream.port",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			_, err := loadFromYAML(t, dir, tt.edit(baseConfigYAML(t, dir)))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadEmptyFile(t *testing.T) {
	_, err := loadFromYAML(t, t.TempDir(), "# nothing here\n")
	if err == nil || !strings.Contains(err.Error(), "host_key is required") {
		t.Errorf("err = %v, want validation errors", err)
	}
}
