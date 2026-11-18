ARG BASE_IMAGE=ghcr.io/your-org/poddy-workspace-base:latest
FROM ${BASE_IMAGE}

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# hadolint ignore=DL3008
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /home/dev
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
