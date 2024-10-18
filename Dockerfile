FROM docker.io/library/debian:bookworm-slim

ARG BUILD_DATE="N/A"
ARG REVISION="N/A"

LABEL org.opencontainers.image.authors="Alexander Trost <alexander@galexrt>" \
    org.opencontainers.image.created="${BUILD_DATE}" \
    org.opencontainers.image.title="galexrt/extended-ceph-exporter" \
    org.opencontainers.image.description="A Prometheus exporter to provide \"extended\" metrics about a Ceph cluster's running components (e.g., RGW)." \
    org.opencontainers.image.documentation="https://github.com/galexrt/extended-ceph-exporter/blob/main/README.md" \
    org.opencontainers.image.url="https://github.com/galexrt/extended-ceph-exporter" \
    org.opencontainers.image.source="https://github.com/galexrt/extended-ceph-exporter" \
    org.opencontainers.image.revision="${REVISION}" \
    org.opencontainers.image.vendor="galexrt" \
    org.opencontainers.image.version="N/A"

RUN apt-get update && \
    apt-get install -y librbd-dev librados-dev

COPY .build/linux-amd64/extended-ceph-exporter /bin/extended-ceph-exporter

ENTRYPOINT ["/bin/extended-ceph-exporter"]
