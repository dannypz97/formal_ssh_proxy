#!/bin/sh
# Install keys from the optional /keys mount, then start sshd.
set -e

if [ -f /keys/upstream_host_key ]; then
    install -m 600 /keys/upstream_host_key /etc/ssh/ssh_host_ed25519_key
    install -m 644 /keys/upstream_host_key.pub /etc/ssh/ssh_host_ed25519_key.pub
fi

if [ -f /keys/upstream_client_key.pub ]; then
    install -d -m 700 -o testuser -g testuser /home/testuser/.ssh
    install -m 600 -o testuser -g testuser /keys/upstream_client_key.pub /home/testuser/.ssh/authorized_keys
fi

exec /usr/sbin/sshd -D -e
