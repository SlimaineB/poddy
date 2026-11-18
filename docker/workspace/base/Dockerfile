FROM docker.io/library/golang:1.26.11@sha256:4165ee6bf3b7811fd7b40373290405c83d0167dcb87ee5f29c427cdcef45ff8f AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /poddy-agent ./cmd/poddy-agent \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /poddy ./cmd/poddy

FROM docker.io/library/debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc

# hadolint ignore=DL3008
RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-server sudo git curl ca-certificates \
 && rm -rf /var/lib/apt/lists/* /etc/ssh/ssh_host_* \
 && useradd -m -s /bin/bash dev \
 && echo 'dev ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/dev \
 && mkdir -p /run/sshd /home/dev/.ssh /workspace \
 && chown -R dev:dev /home/dev /workspace \
 && chmod 700 /home/dev/.ssh \
 && printf 'PasswordAuthentication no\nPermitRootLogin no\nUsePAM no\nAuthorizedKeysFile .ssh/authorized_keys\n' >> /etc/ssh/sshd_config

COPY --from=build /poddy-agent /usr/local/bin/poddy-agent
COPY --from=build /poddy /usr/local/bin/poddy
COPY scripts/poddy-ssh-start /usr/local/bin/poddy-ssh-start
COPY scripts/poddy-git-askpass /usr/local/bin/poddy-git-askpass
COPY scripts/poddy-external-auth /usr/local/bin/poddy-external-auth
COPY scripts/poddy-dotfiles /usr/local/bin/poddy-dotfiles
RUN chmod 0755 /usr/local/bin/poddy /usr/local/bin/poddy-agent /usr/local/bin/poddy-ssh-start /usr/local/bin/poddy-git-askpass /usr/local/bin/poddy-external-auth /usr/local/bin/poddy-dotfiles

WORKDIR /home/dev
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
