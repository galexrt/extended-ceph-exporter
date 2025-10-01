# syntax=docker/dockerfile:1

# Golang Builder
FROM docker.io/library/debian:trixie-slim AS gobuilder

WORKDIR /go/src/github.com/galexrt/extended-ceph-exporter/
COPY . ./
RUN apt-get update && \
    apt-get install -y curl git make golang \
        libcephfs-dev librbd-dev librados-dev gcc pkg-config && \
    git config --global --add safe.directory /go/src/github.com/galexrt/extended-ceph-exporter
RUN make build

# Final Image
FROM docker.io/library/debian:trixie-slim

ARG BUILD_DATE="N/A"
ARG REVISION="N/A"

LABEL org.opencontainers.image.authors="Alexander Trost <me@galexrt.moe>" \
    org.opencontainers.image.created="${BUILD_DATE}" \
    org.opencontainers.image.title="galexrt/extended-ceph-exporter" \
    org.opencontainers.image.description="A Prometheus exporter to provide \"extended\" metrics about a Ceph cluster's running components (e.g., RGW)." \
    org.opencontainers.image.documentation="https://github.com/galexrt/extended-ceph-exporter/blob/main/README.md" \
    org.opencontainers.image.url="https://github.com/galexrt/extended-ceph-exporter" \
    org.opencontainers.image.source="https://github.com/galexrt/extended-ceph-exporter" \
    org.opencontainers.image.revision="${REVISION}" \
    org.opencontainers.image.vendor="galexrt" \
    org.opencontainers.image.version="N/A"

VOLUME /config
VOLUME /realms

RUN apt-get update && \
    apt-get install -y libcephfs-dev librbd-dev librados-dev \
        ca-certificates

COPY --from=gobuilder /go/src/github.com/galexrt/extended-ceph-exporter/extended-ceph-exporter /bin/extended-ceph-exporter
# Copy default configs
COPY --from=gobuilder /go/src/github.com/galexrt/extended-ceph-exporter/config.example.yaml /config/config.yaml
COPY --from=gobuilder /go/src/github.com/galexrt/extended-ceph-exporter/realms.example.yaml /realms/realms.yaml

ENTRYPOINT ["/bin/extended-ceph-exporter"]
