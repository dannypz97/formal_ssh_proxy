#!/bin/sh
# Prepare the Docker Compose test environment. Run once before the first
# `docker compose up`. Safe to re-run: existing keys and config are kept.
#
# Creates in deploy/:
#   config.yaml              from config.example.yaml (git-ignored; holds your API key)
# and in deploy/keys/:
#   host_key                 proxy's own SSH host key
#   alice_key(.pub)          client key for proxy user "alice" (public-key auth demo)
#   upstream_client_key      key the proxy uses to log in to the upstream server
#   upstream_host_key        upstream sshd's host key
#   known_hosts              lets the proxy verify upstream_host_key

set -e

DIR="$(cd "$(dirname "$0")" && pwd)"
KEYS_DIR="$DIR/keys"

if [ -f "$DIR/config.yaml" ]; then
    echo "config.yaml already exists, skipping."
else
    cp "$DIR/config.example.yaml" "$DIR/config.yaml"
    echo "Created $DIR/config.yaml"
fi

mkdir -p "$KEYS_DIR"

gen() {
    if [ -f "$KEYS_DIR/$1" ]; then
        echo "$1 already exists, skipping."
    else
        ssh-keygen -t ed25519 -f "$KEYS_DIR/$1" -N "" -C "$1" -q
        echo "Created $KEYS_DIR/$1"
    fi
}

gen host_key
gen alice_key
gen upstream_client_key
gen upstream_host_key

# The upstream is reached as "upstream" (port 22) on the Compose network.
echo "upstream $(cut -d' ' -f1,2 "$KEYS_DIR/upstream_host_key.pub")" > "$KEYS_DIR/known_hosts"

echo "Done. You can now run: docker compose up --build"
