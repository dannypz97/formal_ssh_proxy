FROM alpine:3.21

RUN apk add --no-cache openssh-server && \
    ssh-keygen -A && \
    mkdir -p /run/sshd

# Create a test user with a known password.
RUN adduser -D -s /bin/ash testuser && \
    echo "testuser:testpass" | chpasswd

# Allow password auth for testing.
RUN sed -i 's/^#PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config && \
    sed -i 's/^#PermitRootLogin.*/PermitRootLogin no/' /etc/ssh/sshd_config

EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
