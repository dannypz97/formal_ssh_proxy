FROM alpine:3.21

RUN apk add --no-cache openssh-server && \
    ssh-keygen -A && \
    mkdir -p /run/sshd

# Test user with a known password. The proxy's public key is installed for it
# at startup by entrypoint.sh (see deploy/generate-keys.sh).
RUN adduser -D -s /bin/ash testuser && \
    echo "testuser:testpass" | chpasswd

RUN sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config && \
    sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin no/' /etc/ssh/sshd_config

COPY entrypoint.sh /entrypoint.sh

EXPOSE 22
ENTRYPOINT ["/entrypoint.sh"]
