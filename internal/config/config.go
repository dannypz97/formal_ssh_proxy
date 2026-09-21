package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level proxy configuration.
type Config struct {
	Listen   string         `yaml:"listen"`
	HostKey  string         `yaml:"host_key"`
	Upstream UpstreamConfig `yaml:"upstream"`
	Users    []UserConfig   `yaml:"users"`
	LogDir   string         `yaml:"log_dir"`
	LLM      LLMConfig     `yaml:"llm"`
}

// UpstreamConfig describes how to connect to the upstream SSH server.
type UpstreamConfig struct {
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	PrivateKey string `yaml:"private_key"` // path to PEM file
}

// UserConfig describes a user allowed to connect to the proxy.
// If PublicKey is set, public-key auth is tried first.
// If Password is set, password auth is used as a fallback.
type UserConfig struct {
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	PublicKey string `yaml:"public_key"` // path to authorized public key file (OpenSSH format)
}

// LLMConfig controls the optional session-summary feature.
// If APIKey is empty the feature is disabled.
type LLMConfig struct {
	Provider string `yaml:"provider"` // "anthropic" (default) | "openai"; empty ⇒ disabled
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`    // optional; per-provider default used when empty
}

// Load reads and validates a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{
		// Defaults
		Listen: "0.0.0.0:2022",
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

func (c *Config) validate() error {
	var errs []string

	// Host key is required.
	if c.HostKey == "" {
		errs = append(errs, "host_key is required")
	} else if !fileExists(c.HostKey) {
		errs = append(errs, fmt.Sprintf("host_key file not found: %s", c.HostKey))
	}

	// Upstream validation.
	if c.Upstream.Host == "" {
		errs = append(errs, "upstream.host is required")
	}
	if c.Upstream.Port == 0 {
		c.Upstream.Port = 22
	}
	if c.Upstream.Username == "" {
		errs = append(errs, "upstream.username is required")
	}
	if c.Upstream.Password == "" && c.Upstream.PrivateKey == "" {
		errs = append(errs, "upstream: at least one of password or private_key is required")
	}
	if c.Upstream.PrivateKey != "" && !fileExists(c.Upstream.PrivateKey) {
		errs = append(errs, fmt.Sprintf("upstream.private_key file not found: %s", c.Upstream.PrivateKey))
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
		if u.Password == "" && u.PublicKey == "" {
			errs = append(errs, fmt.Sprintf("users[%d] (%s): at least one of password or public_key is required", i, u.Username))
		}
		if u.PublicKey != "" && !fileExists(u.PublicKey) {
			errs = append(errs, fmt.Sprintf("users[%d] (%s): public_key file not found: %s", i, u.Username, u.PublicKey))
		}
	}

	// Log directory: default and create if needed.
	if c.LogDir == "" {
		c.LogDir = "./logs"
	}
	if err := os.MkdirAll(c.LogDir, 0o750); err != nil {
		errs = append(errs, fmt.Sprintf("cannot create log directory %s: %v", c.LogDir, err))
	}

	// LLM validation.
	if c.LLM.APIKey != "" {
		p := strings.ToLower(c.LLM.Provider)
		if p == "" {
			c.LLM.Provider = "anthropic"
			p = "anthropic"
		}
		switch p {
		case "anthropic":
			if c.LLM.Model == "" {
				c.LLM.Model = "claude-sonnet-4-6"
			}
		case "openai":
			if c.LLM.Model == "" {
				c.LLM.Model = "gpt-4o"
			}
		default:
			errs = append(errs, fmt.Sprintf("llm.provider: unsupported provider %q (use \"anthropic\" or \"openai\")", c.LLM.Provider))
		}
		c.LLM.Provider = p
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// Addr returns the upstream address as host:port.
func (u *UpstreamConfig) Addr() string {
	return fmt.Sprintf("%s:%d", u.Host, u.Port)
}

// LLMEnabled reports whether a valid LLM configuration is present.
func (c *Config) LLMEnabled() bool {
	return c.LLM.APIKey != ""
}

// ResolveHostKeyPath returns the absolute path of the host key, resolved
// relative to the config file directory if not already absolute.
func ResolveRelativeTo(base, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
