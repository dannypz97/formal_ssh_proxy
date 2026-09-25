# ssh-proxy

A transparent SSH proxy in Go that forwards client connections to a single upstream SSH server while logging all client stdin to per-session files. Optionally produces async LLM-powered security assessments of each session.

## Features

- **Transparent forwarding** — proxies channels, requests (`pty-req`, `shell`, `exec`, `window-change`, `env`, `signal`), and data bidirectionally so the client experience is indistinguishable from a direct connection.
- **Session logging** — writes every byte of client stdin to `<username>_<UTC timestamp>.log` files.
- **LLM security summaries** — after a session ends, an async worker sends the cleaned transcript to Anthropic (Claude) or OpenAI for a security risk assessment, written to a `.summary.txt` file alongside the log. Control characters like Ctrl+C and Ctrl+Z are annotated for context.
- **YAML configuration** — upstream server, proxy users (pubkey + password auth), log directory, host key, and LLM settings in one file.
- **Docker Compose deployment** — proxy on `localhost:2022`, test upstream on `localhost:2222`, ready to go with one command.

## Quick start

### Prerequisites

- Go 1.26+ (for building from source)
- Docker and Docker Compose (for containerised deployment)
- `ssh-keygen` (for generating the host key)

### Docker Compose (recommended)

```bash
# Generate the proxy host key
./deploy/generate-keys.sh

# Start the proxy and a test upstream server
docker compose up --build
```

Connect as one of the preconfigured test users:

```bash
ssh -p 2022 alice@localhost   # password: alice123
ssh -p 2022 bob@localhost     # password: bob123
```

Session logs appear in `./logs/`.

### Build from source

```bash
go build -o ssh-proxy ./cmd/ssh-proxy/

# Create a config (copy and edit the example)
cp config.example.yaml config.yaml

# Generate a host key
ssh-keygen -t ed25519 -f host_key -N ""

# Run
./ssh-proxy -config config.yaml
```

## Configuration reference

See [`config.example.yaml`](config.example.yaml) for a fully commented example. All fields below are YAML keys.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `listen` | string | `0.0.0.0:2022` | Address the proxy listens on. |
| `host_key` | string | *(required)* | Path to the proxy's SSH host key (PEM). |
| `log_dir` | string | `./logs` | Directory for session log files. Created at startup if missing. |

**`upstream`** — the single upstream SSH server:

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `host` | string | *(required)* | Hostname or IP. |
| `port` | int | `22` | SSH port. |
| `username` | string | *(required)* | User to authenticate as. |
| `password` | string | | Password for upstream auth. |
| `private_key` | string | | Path to a private key for upstream auth (tried before password). |

**`users`** — list of allowed proxy users:

| Key | Type | Description |
|-----|------|-------------|
| `username` | string | Login name. |
| `password` | string | Password (fallback if pubkey auth fails). |
| `public_key` | string | Path to an OpenSSH authorized public key file (tried first). |

**`llm`** — optional LLM security summarizer:

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `provider` | string | `anthropic` | `"anthropic"` or `"openai"`. |
| `api_key` | string | | API key. Leave empty to disable summarization. |
| `model` | string | `claude-sonnet-4-6` / `gpt-4o` | Model to use (provider-specific default). |

## Design

### Channel-level proxying

The proxy operates at the SSH channel level, not TCP. After authenticating the client, it opens a matching channel on the upstream server for each client channel and copies data and requests bidirectionally. This preserves terminal behaviour — PTY allocation, window resizing, signals — transparently.

```
Client  ──SSH──▶  Proxy  ──SSH──▶  Upstream
                   │
                   ├──▶ session log (.log)
                   └──▶ LLM summary  (.summary.txt)
```

### Stdin logging with io.MultiWriter

Client-to-upstream data is copied through an `io.MultiWriter` that tees to both the upstream channel and the session log file. No extra goroutine or buffer is needed — the tee is inline with the copy.

### Async LLM summarization

When a session ends, its log path is submitted to a bounded worker pool (goroutines gated by a semaphore). The worker reads the log, cleans the transcript (strips ANSI escapes, annotates control characters like `[Ctrl+C]`, handles backspace), truncates if needed, and sends it to the configured LLM for a security risk assessment. The summary is written to `<session>.summary.txt`. The proxy never blocks on LLM calls — sessions close immediately.

### Authentication

The proxy authenticates clients independently of the upstream. Public key auth is tried first if a `public_key` path is configured for the user; password auth is the fallback. The upstream connection uses its own credentials (key or password) configured under `upstream`.

## Project structure

```
cmd/ssh-proxy/          Entry point
internal/
  config/               YAML loader, validation, defaults
  proxy/                SSH server, channel proxying, session logging
  summarizer/           LLM interface, Anthropic/OpenAI providers, worker pool
deploy/
  config.yaml           Docker test config
  generate-keys.sh      Host key generator
  upstream/Dockerfile   Test sshd container
Dockerfile              Multi-stage build for the proxy
docker-compose.yml      Proxy + upstream stack
config.example.yaml     Documented config template
```

## Testing

```bash
go test ./...
```

Tests cover config loading/validation, filename sanitisation, session logging, transcript cleaning, prompt formatting, and the summarizer worker pool.
