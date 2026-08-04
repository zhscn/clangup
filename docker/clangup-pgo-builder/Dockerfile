# syntax=docker/dockerfile:1

ARG BASE_IMAGE=ghcr.io/zhscn/clangup-builder:1

FROM ${BASE_IMAGE}

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# BOLT LBR collection uses perf against the self-hosted runner's kernel.
RUN set -eux; \
    package_manager="$(command -v dnf || command -v yum)"; \
    "${package_manager}" install -y perf; \
    "${package_manager}" clean all; \
    rm -rf /var/cache/dnf /var/cache/yum
