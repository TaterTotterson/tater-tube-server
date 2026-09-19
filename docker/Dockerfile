# Multi-stage build for Tater Tube Server - Multi-platform optimized
# This is the self-contained development/local build Dockerfile
# For CI builds, use Dockerfile.ci which expects pre-built frontend assets
# Use buildkit syntax for advanced features
# syntax=docker/dockerfile:1.11

# Custom base stage with pre-installed packages
FROM lscr.io/linuxserver/baseimage-ubuntu:jammy AS custom-base

ARG DEBIAN_FRONTEND="noninteractive"

# Install mime-support and clean up in single layer
RUN apt-get update && \
    apt-get install --reinstall mime-support -y && \
    rm -rf /var/lib/apt/lists/*

# Frontend build stage
FROM node:24-alpine AS frontend-builder

ARG VERSION=dev
ARG COMMIT=unknown

# Set working directory for frontend
WORKDIR /app/frontend

# Copy only dependency manifests first to leverage caching for npm install
COPY frontend/package.json frontend/package-lock.json ./

# Install dependencies with enhanced cache mounting
RUN --mount=type=cache,target=/root/.npm,sharing=locked \
    npm ci

# Copy the rest of the frontend sources and build with cache
COPY frontend/ ./
RUN --mount=type=cache,target=/tmp/npm-build-cache,sharing=locked \
    APP_VERSION=${VERSION} GIT_COMMIT=${COMMIT} npm run build

# Backend build stage with native arch support (no cross-compilation)
FROM golang:1.26-alpine AS backend-builder

# Install build dependencies including sqlite with cache mount
RUN --mount=type=cache,target=/var/cache/apk,sharing=locked \
    apk add --no-cache git gcc g++ musl-dev sqlite-dev

# Set working directory
WORKDIR /app

# Copy go module files first for better caching
COPY go.mod go.sum ./

# Download dependencies with enhanced cache mount
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-mod-download,sharing=locked \
    go mod download

# Copy source code in optimal order for caching
COPY ./internal ./internal
COPY ./cmd ./cmd
COPY ./frontend/embed_cli.go ./frontend/embed_cli.go
COPY ./frontend/embed_docker.go ./frontend/embed_docker.go

# Copy built frontend from previous stage
COPY --from=frontend-builder /app/frontend/dist ./frontend/dist

# Build web binary with enhanced cache mount for native architecture
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIMESTAMP=unknown
RUN --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    --mount=type=cache,target=/tmp/go-build-cache,sharing=locked \
    CGO_ENABLED=1 GOOS=linux \
    go build -a \
    -ldflags "-linkmode external -extldflags \"-static\" -X 'github.com/TaterTotterson/tater-tube-server/internal/version.Version=${VERSION}' -X 'github.com/TaterTotterson/tater-tube-server/internal/version.GitCommit=${COMMIT}' -X 'github.com/TaterTotterson/tater-tube-server/internal/version.Timestamp=${BUILD_TIMESTAMP}'" \
    -o tater-tube-server cmd/tater-tube-server/main.go

# Final stage - use custom base image with pre-installed packages
ARG TARGETARCH
FROM custom-base

ARG TARGETARCH
ARG DEBIAN_FRONTEND="noninteractive"
ARG BUILD_DATE
ARG VERSION
ARG PUID=1000
ARG PGID=1000

# Set up environment variables for PUID and PGID
ENV PUID=${PUID}
ENV PGID=${PGID}
ENV LIBVA_DRIVERS_PATH=/usr/lib/jellyfin-ffmpeg/lib/dri
ENV XDG_DATA_DIRS=/usr/lib/jellyfin-ffmpeg/share:/usr/local/share:/usr/share

# Set the working directory inside the container
WORKDIR /app

# Copy web binary from builder
COPY --from=backend-builder /app/tater-tube-server /app/tater-tube-server
COPY --from=frontend-builder /app/frontend/dist ./frontend/dist

# Install required packages for runtime (mime-support already in custom-base)
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        gnupg \
        wget && \
    media_ffmpeg_vendor="jelly""fin" && \
    install -d -m 0755 /etc/apt/keyrings && \
    curl -fsSL "https://repo.${media_ffmpeg_vendor}.org/${media_ffmpeg_vendor}_team.gpg.key" | \
        gpg --dearmor --yes --output /etc/apt/keyrings/media-ffmpeg.gpg && \
    chmod 0644 /etc/apt/keyrings/media-ffmpeg.gpg && \
    . /etc/os-release && \
    arch="$(dpkg --print-architecture)" && \
    printf 'Types: deb\nURIs: https://repo.%s.org/%s\nSuites: %s\nComponents: main\nArchitectures: %s\nSigned-By: /etc/apt/keyrings/media-ffmpeg.gpg\n' "$media_ffmpeg_vendor" "$ID" "$VERSION_CODENAME" "$arch" \
        > /etc/apt/sources.list.d/media-ffmpeg.sources && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        "${media_ffmpeg_vendor}-ffmpeg7" \
        v4l-utils \
        vainfo && \
    ln -sf "/usr/lib/${media_ffmpeg_vendor}-ffmpeg/ffmpeg" /usr/local/bin/tater-ffmpeg && \
    ln -sf "/usr/lib/${media_ffmpeg_vendor}-ffmpeg/ffprobe" /usr/local/bin/tater-ffprobe && \
    ln -sf "/usr/lib/${media_ffmpeg_vendor}-ffmpeg/ffmpeg" /usr/local/bin/ffmpeg && \
    ln -sf "/usr/lib/${media_ffmpeg_vendor}-ffmpeg/ffprobe" /usr/local/bin/ffprobe && \
    install -d -m 0755 /usr/share/vulkan/icd.d && \
    if [ -d "/usr/lib/${media_ffmpeg_vendor}-ffmpeg/share/vulkan/icd.d" ]; then \
        for vulkan_icd in /usr/lib/${media_ffmpeg_vendor}-ffmpeg/share/vulkan/icd.d/*.json; do \
            [ -f "${vulkan_icd}" ] || continue; \
            ln -sf "${vulkan_icd}" "/usr/share/vulkan/icd.d/$(basename "${vulkan_icd}")"; \
        done; \
    fi && \
    install -d -m 0755 /usr/share/tater-tube-server/ai-upscaling && \
    curl -fsSL --retry 3 \
        https://github.com/igv/FSRCNN-TensorFlow/releases/download/1.1/FSRCNNX_x2_8-0-4-1.glsl \
        -o /tmp/FSRCNNX_x2_8-0-4-1.glsl && \
    echo "e800dbc5c1c95185cc82216c597724533ff5f2880179f256eef600f03e8dc2ae  /tmp/FSRCNNX_x2_8-0-4-1.glsl" | sha256sum -c - && \
    install -m 0644 /tmp/FSRCNNX_x2_8-0-4-1.glsl /usr/share/tater-tube-server/ai-upscaling/FSRCNNX_x2_8-0-4-1.glsl && \
    curl -fsSL --retry 3 \
        https://github.com/igv/FSRCNN-TensorFlow/releases/download/1.1/FSRCNNX_x2_16-0-4-1.glsl \
        -o /tmp/FSRCNNX_x2_16-0-4-1.glsl && \
    echo "d5a24a271e5d9a3f7f7a053b150c460a44c25b3cf7f770857d57cc3a2e1c9965  /tmp/FSRCNNX_x2_16-0-4-1.glsl" | sha256sum -c - && \
    install -m 0644 /tmp/FSRCNNX_x2_16-0-4-1.glsl /usr/share/tater-tube-server/ai-upscaling/FSRCNNX_x2_16-0-4-1.glsl && \
    curl -fsSL --retry 3 \
        https://github.com/Artoriuz/ArtCNN/releases/download/v1.6.2/ArtCNN_C4F16.glsl \
        -o /tmp/ArtCNN_C4F16.glsl && \
    echo "03d0b3d31cb82c898a94a46663021a3e8f02c5a21d69c5cfdf0208de4bfd453e  /tmp/ArtCNN_C4F16.glsl" | sha256sum -c - && \
    install -m 0644 /tmp/ArtCNN_C4F16.glsl /usr/share/tater-tube-server/ai-upscaling/ArtCNN_C4F16.glsl && \
    curl -fsSL --retry 3 \
        https://github.com/Artoriuz/ArtCNN/releases/download/v1.6.2/ArtCNN_C4F32.glsl \
        -o /tmp/ArtCNN_C4F32.glsl && \
    echo "f773bce6cf5fe7e5e5d599a695edd40df5cd7a20c3d08c4d164d07591d5bead3  /tmp/ArtCNN_C4F32.glsl" | sha256sum -c - && \
    install -m 0644 /tmp/ArtCNN_C4F32.glsl /usr/share/tater-tube-server/ai-upscaling/ArtCNN_C4F32.glsl && \
    rm -f /tmp/FSRCNNX_x2_8-0-4-1.glsl /tmp/FSRCNNX_x2_16-0-4-1.glsl /tmp/ArtCNN_C4F16.glsl /tmp/ArtCNN_C4F32.glsl && \
    rm -rf /var/lib/apt/lists/*

# Fail image builds if the bundled FFmpeg loses a required portable filter or,
# on x86_64, one of the advertised hardware backends.
RUN ffmpeg -hide_banner -filters 2>&1 | grep -Eq '[[:space:]]zscale[[:space:]]' && \
    if [ "${TARGETARCH}" = "amd64" ]; then \
        ffmpeg -hide_banner -filters 2>&1 | grep -Eq '[[:space:]]libplacebo[[:space:]]' && \
        ffmpeg -hide_banner -encoders 2>&1 | grep -Eq '[[:space:]]h264_nvenc[[:space:]]' && \
        ffmpeg -hide_banner -encoders 2>&1 | grep -Eq '[[:space:]]h264_vaapi[[:space:]]'; \
    fi

# Make binary executable
RUN chmod +x /app/tater-tube-server

# Copy s6-overlay service configuration
COPY docker/root/ /

# Expose port for web interface
EXPOSE 8080

# Volume
VOLUME ["/config"]

# Health check
HEALTHCHECK --interval=5s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/live || exit 1

# Labels
LABEL org.opencontainers.image.title="Tater Tube Server"
LABEL build_version="version: Build-date:- ${VERSION}  ${BUILD_DATE}"
LABEL maintainer="Tater Tube"
