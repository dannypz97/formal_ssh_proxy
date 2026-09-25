# ssh-proxy

A transparent SSH proxy in Go. It authenticates clients against its own user list, forwards their sessions to a single pre-configured upstream SSH server, and records each session's raw stdin to a per-session log file. Optionally, each finished session is sent to an LLM (Anthropic or OpenAI) for an asynchronous security assessment.

```
Client ──SSH──▶ Proxy ──SSH──▶ Upstream
                  │
                  ├──▶ <user>_<timestamp>.log          raw client stdin
                  └──▶ <user>_<timestamp>.summary.txt  LLM security assessment (optional)
```

## Features

- **Transparent sessions**: interactive shells, `exec` commands, `scp`/`sftp`, PTYs, **terminal resizing** (`window-change`), signals, environment variables, stdout/stderr, and exit codes are all passed through.
- **Session logging**: every byte of client stdin is written to `<username>_<UTC start time>.log`, one file per session.
- **Client authentication**: per-user public key and/or password.
- **Upstream authentication**: private key and/or password, with optional host-key verification against a `known_hosts` file.
- **LLM security summaries**: generated asynchronously after each session and written next to the log. The proxy log announces when each one is ready.
- **Docker Compose test environment**: the proxy on `localhost:2022` and an OpenSSH upstream on `localhost:2222`.

## Quick start (Docker Compose)

Requires Docker with Compose, and `ssh-keygen` on the host.

```bash
# 1. Generate the keys used by the test environment (into deploy/keys/)
./deploy/generate-keys.sh

# 2. Start the proxy and the test upstream
docker compose up --build
```

Connect through the proxy:

```bash
ssh -p 2022 -i deploy/keys/alice_key alice@localhost   # public key (password alice123 also works)
ssh -p 2022 bob@localhost                              # password: bob123
```

Session logs (and summaries, if enabled) appear in `./logs/` on the host. The upstream can also be reached directly with `ssh -p 2222 testuser@localhost` (password `testpass`).

What the compose setup mounts:

| Host path | Container path | Purpose |
|-----------|----------------|---------|
| `deploy/config.yaml` | `/etc/ssh-proxy/config.yaml` (read-only) | Proxy configuration |
| `deploy/keys/` | `/etc/ssh-proxy/keys/` (read-only) | Proxy host key, client/upstream keys, `known_hosts` |
| `logs/` | `/var/log/ssh-proxy/` | Session logs and summaries |

The test environment exercises every auth path. `alice` uses a public key with a password fallback, and `bob` uses a password only. The proxy logs in to the upstream with a key and verifies the upstream's host key. To enable LLM summaries, add an `llm` section with an API key to `deploy/config.yaml` and restart.

## Build and run from source

Requires Go 1.26+.

```bash
go build -o ssh-proxy ./cmd/ssh-proxy/

cp config.example.yaml config.yaml         # edit as needed
ssh-keygen -t ed25519 -f host_key -N ""     # proxy host key

./ssh-proxy -config config.yaml
```

`config.example.yaml` points at the Compose test upstream (`localhost:2222`). Start just that container with `docker compose up upstream` to try the proxy locally. Stop the Compose `proxy` service first, because both listen on 2022.

The only command-line flag is `-config <path>`, which is required. The proxy logs to stderr. On `SIGINT`/`SIGTERM` it stops accepting connections and waits up to 30s for pending LLM summaries.

## Configuration reference

The configuration is a YAML file; [`config.example.yaml`](config.example.yaml) is a commented template. Relative paths are resolved against the proxy's working directory. Unknown keys are rejected, so a typo such as `public_keys:` fails at startup instead of silently disabling an auth method.

### Top level

| Key | Default | Description |
|-----|---------|-------------|
| `listen` | `0.0.0.0:2022` | Address the proxy listens on. |
| `host_key` | *(required)* | The proxy's SSH host key (unencrypted private key, e.g. from `ssh-keygen`). |
| `log_dir` | `./logs` | Directory for session logs and summaries. Created at startup if missing. |

### `upstream`

| Key | Default | Description |
|-----|---------|-------------|
| `host` | *(required)* | Upstream hostname or IP. |
| `port` | `22` | Upstream SSH port. |
| `username` | *(required)* | Account used on the upstream for all proxied sessions. |
| `password` | | Password for upstream auth. |
| `private_key` | | Unencrypted private key for upstream auth, tried before the password. |
| `known_hosts` | | OpenSSH `known_hosts` file used to verify the upstream host key (hashed entries are supported). **Strongly recommended**: without it the host key is not checked and a warning is logged. |

At least one of `password` or `private_key` is required.

### `users`

A list of users allowed to connect to the proxy:

| Key | Description |
|-----|-------------|
| `username` | Login name (must be unique). |
| `public_key` | An OpenSSH `authorized_keys`-format file: a `.pub` file, or several keys, one per line. |
| `password` | Password. |

Each user needs a `public_key`, a `password`, or both. Public-key authentication is the default. Password authentication is the fallback, used when the client presents no accepted key or when the user has no key configured. Clients such as OpenSSH try keys first and only prompt for the password afterwards. To enforce key-only access, leave `password` unset.

### `llm` (optional)

| Key | Default | Description |
|-----|---------|-------------|
| `provider` | `anthropic` | `anthropic` or `openai`. |
| `api_key` | | API key. Empty disables the feature. |
| `model` | `claude-opus-5` / `gpt-4o` | Model to use. |
| `max_transcript_chars` | `100000` | Transcripts longer than this (in bytes) are truncated before sending. |

At startup the proxy checks the key and model with the provider. If the provider rejects them (HTTP 401/403/404), summaries are disabled and a message is logged. If the provider can't be reached, the feature stays enabled and a warning is logged.

## Session logs

- One file per SSH session channel, named `<username>_<YYYY-MM-DDTHH-MM-SS.mmmZ>.log` (UTC start time). If two sessions for the same user start in the same millisecond, a `-1`, `-2`, … suffix is added, so sessions never share a file.
- Content is the **raw** client stdin: keystrokes including control characters and escape sequences for interactive sessions, and file contents for `scp`/`sftp` uploads.
- Files are created `0640` in a `0750` directory. Treat them as sensitive: anything the user types is recorded, including passwords typed at prompts such as `sudo`, since those are sent over stdin.
- If the log file cannot be created, the session is refused rather than run unaudited.

## LLM security summaries

When a session ends, its log path goes to a worker pool with at most 4 concurrent LLM requests. For each session the worker:

1. Cleans the transcript. It strips ANSI/terminal escape sequences, applies backspaces, and annotates notable control characters such as `[Ctrl+C]` and `[Ctrl+D]`.
2. Truncates the transcript if needed.
3. Asks the model for a list of risky actions with a severity for each, plus an overall assessment.

The result is written to `<session>.summary.txt` next to the log, and the proxy logs `summarizer: security evaluation ready for session …`. Summarization never blocks the proxy: sessions close immediately and new connections are accepted while summaries are in flight.

The transcript is fully user-controlled, so it is sent as data in the user message. The analysis instructions go in the system prompt, and they tell the model to ignore instructions found in the transcript and flag them as suspicious. Offensive-security commands can trip model safety classifiers. On Anthropic models that support it, the request enables server-side refusal fallbacks, and a refusal is logged as a failed summary.

Session transcripts are sent to a third-party API. Only enable this feature where that is acceptable.

## Design

### Channel-level proxying

The proxy terminates SSH on both sides rather than forwarding TCP. That is what makes it possible to see (and log) the client's stdin. For each client connection it:

1. Authenticates the client against the configured users.
2. Opens one upstream SSH connection. It authenticates as `upstream.username` and verifies the host key if `known_hosts` is set.
3. For every `session` channel the client opens, opens a matching upstream channel.
4. Copies data and channel requests in both directions:
   - client stdin → upstream, teed to the session log via `io.MultiWriter`
   - upstream stdout and stderr → client, as separate streams
   - channel requests (`pty-req`, `shell`, `exec`, `subsystem`, `window-change`, `env`, `signal`, `exit-status`, …) forwarded as-is. This is what makes the proxy responsive to terminal resizes: the client's `window-change` requests reach the upstream PTY unchanged.

Close handling mirrors a direct connection:
- When the upstream closes a channel (after sending `exit-status`), the proxy flushes the remaining output and closes the client channel, so the client's `ssh` exits with the remote exit code.
- When the client goes away, the upstream channel is closed.
- If the whole upstream connection drops, the client connection is closed too.

### Security choices

- **Only `session` channels are allowed.** Port forwarding (`ssh -L`/`-R`, `direct-tcpip`, `tcpip-forward`) is refused. It would let users tunnel arbitrary traffic through the upstream host without it appearing in the audit log.
- **Upstream host-key verification** through `known_hosts`. The proxy offers only the host-key algorithms listed there, so an upstream with several host keys (RSA/ECDSA/Ed25519) still verifies against a known_hosts that contains just one of them.
- **Passwords** are compared in constant time. Users without a configured password can never log in by password, including with an empty one.
- **Timeouts**: unauthenticated clients get 30s to finish the handshake, and upstream connection attempts time out after 10s.
- **Fail fast**: all keys are loaded and parsed at startup, so a bad key file is reported immediately rather than on the first login.

### Known limitations

- Every proxied session uses the same upstream account. Per-user upstream identities are not supported.
- User passwords are stored in plaintext in the config file. Protect it accordingly, or prefer public keys.
- Agent forwarding and X11 forwarding are not proxied.
- There is no rate limiting of failed logins beyond the per-connection attempt limit of `golang.org/x/crypto/ssh`.
- The proxy container runs as root so that it can read key files mounted from the host with `0600` permissions.

## Project structure

```
cmd/ssh-proxy/            Entry point: flag parsing, startup, signal handling
internal/
  config/                 YAML loading, validation, defaults
  proxy/                  SSH server, authentication, channel proxying, session logs
  summarizer/             LLM interface, Anthropic/OpenAI providers, worker pool
deploy/
  config.yaml             Config used by docker compose
  generate-keys.sh        Generates all keys for the test environment
  upstream/               Test upstream: Alpine + OpenSSH
Dockerfile                Multi-stage build for the proxy
docker-compose.yml        Proxy + upstream test environment
config.example.yaml       Commented config template
```

## Testing

```bash
go test -race ./...
```

- **Proxy tests** (`internal/proxy`) run end to end against an in-process fake SSH upstream. They cover authentication (including the empty-password case), exit codes and stderr, stdin forwarding and logging, window-change forwarding, and rejection of port-forwarding channels.
- **Config tests** cover validation, defaults, and rejection of unknown keys.
- **Summarizer tests** cover transcript cleaning, prompt formatting and truncation, and the worker pool's concurrency limit.
