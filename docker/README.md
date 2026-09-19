# Tater Tube Server Docker Image

This directory contains the files used to build the Tater Tube Server container.
For installation, Unraid, local-media, and troubleshooting instructions, see
the [main README](../README.md).

## Image Files

- `Dockerfile` builds the frontend and backend from source.
- `Dockerfile.ci` uses frontend assets produced by the release workflow.
- `root/` contains the s6-overlay service definition.

## Basic Run

```bash
docker run -d \
  --name tater-tube-server \
  -p 8080:8080 \
  -v /path/to/config:/config \
  --restart unless-stopped \
  ghcr.io/tatertotterson/tater-tube-server:latest
```

Open `http://SERVER-IP:8080`. The `/config` volume stores configuration, the
database, logs, metadata, pairing information, imports, and the segment cache.

## GPU Access

The image includes the supported FFmpeg build. Software transcoding needs no
extra container options.

For NVIDIA NVENC:

```bash
docker run ... \
  --gpus all \
  -e NVIDIA_DRIVER_CAPABILITIES=all \
  ghcr.io/tatertotterson/tater-tube-server:latest
```

For AMD VAAPI or Intel Quick Sync/VAAPI:

```bash
docker run ... \
  --device /dev/dri:/dev/dri \
  ghcr.io/tatertotterson/tater-tube-server:latest
```

After the container starts, open `Configuration -> Hardware Transcoding`, run
**Auto Detect**, confirm the encoder is **Ready**, and select **Save**.

## Date and Time

The container uses the Docker host's clock. Keep time synchronization enabled
on the host. The optional `TZ` environment variable changes the displayed time
zone but cannot correct an inaccurate host clock.

## Build Locally

From the repository root:

```bash
docker build -f docker/Dockerfile -t tater-tube-server:dev .
```
