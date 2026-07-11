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
  -p 8080:8080 \
  -v /path/to/config:/config \
  --restart unless-stopped \
  ghcr.io/tatertotterson/tater-tube-server:latest
```

The server UI is available at `http://localhost:8080`.
Configure NNTP providers and the Newznab Stream catalog from the web UI, then
enter the server URL and download key in Tater Tube's Usenet module.

## Volume

`/config` stores `config.yaml`, the database, logs, metadata, imports, and
segment cache data. Login is disabled by default; enable it from the web UI only
if you need it.

Streaming does not persist full media downloads by default. The persistent
stream cache stores decoded Usenet segments in `/config/segment-cache`; adjust
its size, expiry, or path from the web UI under `Configuration -> Streaming`.

FFmpeg is installed in the image for optional playback transcoding. Software
x264 works without extra Docker flags. For VAAPI/QSV on Linux, pass `/dev/dri`
through as a device:

```bash
docker run ... --device /dev/dri:/dev/dri ...
```

Then enable transcoding and select the hardware mode in
`Configuration -> Hardware Transcoding`. Intel QSV uses FFmpeg's runtime device
selection; the hardware device field is mainly for VAAPI systems that need a
specific render node.

## Build

```bash
docker build -f docker/Dockerfile -t tater-tube-server:dev .
```
