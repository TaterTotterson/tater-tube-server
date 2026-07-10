# Tater Tube Server Docker

This directory contains container builds for the Tater Tube Server backend.

## Files

- `Dockerfile` builds the frontend and backend in one image.
- `Dockerfile.ci` expects `frontend/dist` to already exist.
- `root/` contains the s6-overlay service definition.

## Basic Run

```bash
docker run -d \
  --name tater-tube-server \
  -e PUID=1000 \
  -e PGID=1000 \
  -e PORT=8080 \
  -p 8080:8080 \
  -v /path/to/config:/config \
  -v /path/to/metadata:/metadata \
  ghcr.io/tatertotterson/tater-tube-server:latest
```

The server UI is available at `http://localhost:8080`.

## Volumes

- `/config` stores `config.yaml`, the database, and logs.
- `/metadata` stores prepared stream metadata.

## Build

```bash
docker build -f docker/Dockerfile -t tater-tube-server:dev .
```
