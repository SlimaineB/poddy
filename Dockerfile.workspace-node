ARG BASE_IMAGE=ghcr.io/slimaineb/poddy-workspace-base:latest
FROM ${BASE_IMAGE}

RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && npm install -g pnpm yarn typescript tsx \
 && npm cache clean --force \
 && rm -rf /var/lib/apt/lists/* /root/.npm

RUN printf 'export PATH=$PATH:/home/dev/.npm-global/bin\n' > /etc/profile.d/node.sh \
 && printf 'export PATH=$PATH:/home/dev/.npm-global/bin\n' >> /etc/skel/.bashrc

ENV NPM_CONFIG_PREFIX=/home/dev/.npm-global

WORKDIR /home/dev
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
