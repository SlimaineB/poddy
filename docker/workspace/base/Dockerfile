# syntax=docker/dockerfile:1

ARG GOLANG_IMAGE=docker.io/library/golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf
ARG DEBIAN_IMAGE=docker.io/library/debian:bookworm-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc

FROM ${GOLANG_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/poddy-agent ./cmd/poddy-agent \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/poddy ./cmd/poddy

FROM ${DEBIAN_IMAGE}

ARG DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8 \
    LC_ALL=C.UTF-8

# hadolint ignore=DL3008
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      openssh-server sudo git curl ca-certificates \
 && rm -rf /var/lib/apt/lists/* /etc/ssh/ssh_host_* \
 && useradd -m -s /bin/bash dev \
 && usermod -p '*' dev \
 && printf 'dev ALL=(ALL) NOPASSWD:ALL\n' > /etc/sudoers.d/dev \
 && chmod 0440 /etc/sudoers.d/dev \
 && mkdir -p /run/sshd /home/dev/.ssh /workspace \
 && chown -R dev:dev /home/dev /workspace \
 && chmod 700 /home/dev/.ssh

COPY --chmod=0644 docker/workspace/base/sshd_config /etc/ssh/sshd_config.d/00-poddy.conf
RUN ssh-keygen -q -t ed25519 -N '' -f /tmp/poddy-sshd-test-key \
 && /usr/sbin/sshd -t -h /tmp/poddy-sshd-test-key \
 && rm -f /tmp/poddy-sshd-test-key /tmp/poddy-sshd-test-key.pub

COPY --from=build --chmod=0755 /out/poddy-agent /usr/local/bin/poddy-agent
COPY --from=build --chmod=0755 /out/poddy /usr/local/bin/poddy
COPY --chmod=0755 scripts/poddy-ssh-start /usr/local/bin/poddy-ssh-start
COPY --chmod=0755 scripts/poddy-git-askpass /usr/local/bin/poddy-git-askpass
COPY --chmod=0755 scripts/poddy-external-auth /usr/local/bin/poddy-external-auth
COPY --chmod=0755 scripts/poddy-dotfiles /usr/local/bin/poddy-dotfiles

WORKDIR /home/dev
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
