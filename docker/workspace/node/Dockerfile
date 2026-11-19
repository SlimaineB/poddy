# syntax=docker/dockerfile:1

ARG BASE_IMAGE=ghcr.io/slimaineb/poddy-workspace-base:latest
FROM ${BASE_IMAGE}

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG NODE_MAJOR=22
ARG NPM_VERSION=12.2.3
ARG PNPM_VERSION=12.21.0
ARG YARN_VERSION=1.22.22
ARG TYPESCRIPT_VERSION=5.9.3
ARG TSX_VERSION=4.23.15

# hadolint ignore=DL3008
RUN curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && npm install -g "npm@${NPM_VERSION}" "pnpm@${PNPM_VERSION}" "yarn@${YARN_VERSION}" "typescript@${TYPESCRIPT_VERSION}" "tsx@${TSX_VERSION}" \
 && npm cache clean --force \
 && rm -rf /var/lib/apt/lists/* /root/.npm

RUN printf "export PATH=\$PATH:/home/dev/.npm-global/bin\n" > /etc/profile.d/node.sh \
 && printf "export PATH=\$PATH:/home/dev/.npm-global/bin\n" >> /etc/skel/.bashrc

ENV NPM_CONFIG_PREFIX=/home/dev/.npm-global

WORKDIR /home/dev
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
