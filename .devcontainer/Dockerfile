FROM docker.io/library/golang:1.27-trixie

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      git \
      make \
      pkg-config \
      libcephfs-dev \
      librbd-dev \
      librados-dev && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /workspaces/extended-ceph-exporter
