ARG BASE_IMAGE=ghcr.io/slimaineb/poddy-workspace-base:latest
FROM ${BASE_IMAGE}

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      python3 python3-pip python3-venv python3-dev build-essential \
 && rm -rf /var/lib/apt/lists/* \
 && curl -LsSf https://astral.sh/uv/install.sh | env UV_INSTALL_DIR=/usr/local/bin sh

RUN printf 'export PATH=$PATH:/home/dev/.local/bin\n' > /etc/profile.d/python.sh \
 && printf 'export PATH=$PATH:/home/dev/.local/bin\n' >> /etc/skel/.bashrc

WORKDIR /home/dev
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
