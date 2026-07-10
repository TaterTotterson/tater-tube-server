# Tater Tube Server

<p align="center">
  <img src="./frontend/public/tater-tube-logo.png" alt="Tater Tube" width="520" />
</p>

Tater Tube Server is the backend foundation for Tater Tube. The first supported
server role is Usenet/NZB streaming: take an NZB, prepare it, and return
streamable playback URLs that Tater Tube or Stremio-compatible clients can play.

This project keeps the Stremio addon and direct NZB stream endpoint from
AltMount, then trims the app down around streaming setup, provider
configuration, queue status, logs, and system settings. WebDAV, FUSE, and
rclone mount workflows have been removed from the app.

## What It Keeps

- Stremio addon manifest and stream endpoints.
- Direct NZB-to-stream endpoint at `POST /api/nzb/streams`.
- NNTP provider configuration.
- Queue/import processing used to prepare streams.
- Direct media stream URLs through `/api/files/stream`.
- Tater-styled web UI with mascot artwork.

## Backend Direction

The original AltMount project is a full virtual filesystem application. Tater
Tube Server is intended to become a normal media backend instead: Tater Tube can
ask it what Usenet-backed titles are playable, then launch stream URLs from that
server. For now, the working surface is the stream endpoint and Stremio addon.

## Basic Flow

1. Start the server.
2. Open the web UI.
3. Add at least one NNTP provider under `Configuration -> NNTP Providers`.
4. Enable Stremio under `Configuration -> Stremio Integration`.
5. Copy the addon URL from the dashboard or Stremio settings.
6. Install that URL in Stremio.

## Local Development

```bash
go run ./cmd/tater-tube-server serve --config ./config.yaml
```

Frontend development:

```bash
cd frontend
npm install
npm run dev
```

The frontend dev server proxies API requests to `http://localhost:8080`.

## Docker

The release workflow publishes one multi-architecture image to GitHub Container
Registry, tagged `latest`:

```bash
docker pull ghcr.io/tatertotterson/tater-tube-server:latest
```

The included `docker-compose.yml` uses the same image by default.

## Credits

This project is based on [javi11/altmount](https://github.com/javi11/altmount).
The streaming internals, queue/import pipeline, and Stremio endpoint foundation
come from that project.

## License

This project keeps the upstream license terms in [LICENSE](LICENSE).
