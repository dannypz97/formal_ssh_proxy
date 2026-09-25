package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Defaults for configuration values not specified in the YAML file.
const (
	DefaultListenAddr     = "0.0.0.0:2022"
	DefaultUpstreamPort   = 22
	DefaultLogDir         = "./logs"
	DefaultLLMProvider    = "anthropic"
	DefaultAnthropicModel = "claude-opus-5"
	DefaultOpenAIModel    = "gpt-4o"
)

// Config is the top-level proxy configuration.
type Config struct {
	Listen      string         `yaml:"listen"`
	HostKeyPath string         `yaml:"host_key"`
	Upstream    UpstreamConfig `yaml:"upstream"`
	Users       []UserConfig   `yaml:"users"`
	LogDir      string         `yaml:"log_dir"`
	LLM         LLMConfig      `yaml:"llm"`
}

// UpstreamConfig describes how to connect to the upstream SSH server.
type UpstreamConfig struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	Username       string `yaml:"username"`
	Password       string `yaml:"password"`
	PrivateKeyPath string `yaml:"private_key"` // path to PEM file
	KnownHostsPath string `yaml:"known_hosts"` // optional; verifies the upstream host key
}

// UserConfig describes a user allowed to connect to the proxy.
// If PublicKeyPath is set, public-key auth is tried first.
// If Password is set, password auth is used as a fallback.
type UserConfig struct {
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
	PublicKeyPath string `yaml:"public_key"` // path to authorized public key file (OpenSSH format)
}

// LLMConfig controls the optional session-summary feature.
// If APIKey is empty the feature is disabled.
type LLMConfig struct {
	Provider           string `yaml:"provider"`             // "anthropic" (default) | "openai"
	APIKey             string `yaml:"api_key"`              // empty ⇒ feature disabled
	Model              string `yaml:"model"`                // optional; per-provider default used when empty
	MaxTranscriptChars int    `yaml:"max_transcript_chars"` // optional; 0 ⇒ summarizer default
}

// Load reads and validates a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{
		Listen: DefaultListenAddr,
	}
	// Reject unknown keys so that typos (e.g. "public_keys") fail loudly
	// instead of silently disabling an auth method.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// validate checks that the configuration is complete and consistent.
// It applies defaults for optional fields but does not create directories
// or perform other side effects.
func (c *Config) validate() error {
	var errs []string

	// Host key is required.
	if c.HostKeyPath == "" {
		errs = append(errs, "host_key is required")
	} else if !fileExists(c.HostKeyPath) {
		errs = append(errs, fmt.Sprintf("host_key file not found: %s", c.HostKeyPath))
	}

	// Upstream validation.
	if c.Upstream.Host == "" {
		errs = append(errs, "upstream.host is required")
	}
	if c.Upstream.Port == 0 {
		c.Upstream.Port = DefaultUpstreamPort
	} else if c.Upstream.Port < 0 || c.Upstream.Port > 65535 {
		errs = append(errs, fmt.Sprintf("upstream.port out of range: %d", c.Upstream.Port))
	}
	if c.Upstream.Username == "" {
		errs = append(errs, "upstream.username is required")
	}
	if c.Upstream.Password == "" && c.Upstream.PrivateKeyPath == "" {
		errs = append(errs, "upstream: at least one of password or private_key is required")
	}
	if c.Upstream.PrivateKeyPath != "" && !fileExists(c.Upstream.PrivateKeyPath) {
		errs = append(errs, fmt.Sprintf("upstream.private_key file not found: %s", c.Upstream.PrivateKeyPath))
	}
	if c.Upstream.KnownHostsPath != "" && !fileExists(c.Upstream.KnownHostsPath) {
		errs = append(errs, fmt.Sprintf("upstream.known_hosts file not found: %s", c.Upstream.KnownHostsPath))
	}

	// Users validation.
	if len(c.Users) == 0 {
		errs = append(errs, "at least one user must be configured")
	}
	seen := make(map[string]bool)
	for i, u := range c.Users {
		if u.Username == "" {
			errs = append(errs, fmt.Sprintf("users[%d]: username is required", i))
			continue
		}
		if seen[u.Username] {
			errs = append(errs, fmt.Sprintf("users[%d]: duplicate username %q", i, u.Username))
		}
		seen[u.Username] = true
		if u.Password == "" && u.PublicKeyPath == "" {
			errs = append(errs, fmt.Sprintf("users[%d] (%s): at least one of password or public_key is required", i, u.Username))
		}
		if u.PublicKeyPath != "" && !fileExists(u.PublicKeyPath) {
			errs = append(errs, fmt.Sprintf("users[%d] (%s): public_key file not found: %s", i, u.Username, u.PublicKeyPath))
		}
	}

	// Log directory default.
	if c.LogDir == "" {
		c.LogDir = DefaultLogDir
	}

	// LLM validation.
	if c.LLM.APIKey != "" {
		p := strings.ToLower(c.LLM.Provider)
		if p == "" {
			c.LLM.Provider = DefaultLLMProvider
			p = DefaultLLMProvider
		}
		switch p {
		case "anthropic":
			if c.LLM.Model == "" {
				c.LLM.Model = DefaultAnthropicModel
			}
		case "openai":
			if c.LLM.Model == "" {
				c.LLM.Model = DefaultOpenAIModel
			}
		default:
			errs = append(errs, fmt.Sprintf("llm.provider: unsupported provider %q (use \"anthropic\" or \"openai\")", c.LLM.Provider))
		}
		c.LLM.Provider = p
	}
	if c.LLM.MaxTranscriptChars < 0 {
		errs = append(errs, "llm.max_transcript_chars must not be negative")
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// EnsureLogDir creates the log directory if it does not already exist.
// Call this at startup, after Load.
func (c *Config) EnsureLogDir() error {
	return os.MkdirAll(c.LogDir, 0o750)
}

// Addr returns the upstream address as host:port.
func (u *UpstreamConfig) Addr() string {
	return fmt.Sprintf("%s:%d", u.Host, u.Port)
}

// LLMEnabled reports whether an LLM API key is configured. The key itself is
// verified against the provider when the server starts.
func (c *Config) LLMEnabled() bool {
	return c.LLM.APIKey != ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
