#!/bin/sh
# Generate host key and test config for the proxy container.
# Run once before first `docker compose up`.

set -e

DIR="$(cd "$(dirname "$0")" && pwd)"
KEYS_DIR="$DIR/keys"

mkdir -p "$KEYS_DIR"

if [ ! -f "$KEYS_DIR/host_key" ]; then
    echo "Generating proxy host key..."
    ssh-keygen -t ed25519 -f "$KEYS_DIR/host_key" -N "" -q
    echo "Created $KEYS_DIR/host_key"
else
    echo "Host key already exists, skipping."
fi

echo "Done. You can now run: docker compose up"
