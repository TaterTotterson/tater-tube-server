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
- Tater Tube Stream catalog endpoints under `/api/tater/usenet/*`.
- NNTP provider configuration.
- Newznab indexer configuration for Tater Tube players.
- Queue/import processing used to prepare streams.
- Direct media stream URLs through `/api/files/stream`.
- Tater-styled web UI with mascot artwork.

## Backend Direction

The original AltMount project is a full virtual filesystem application. Tater
Tube Server is intended to become a normal media backend instead: Tater Tube now
asks the server for a Newznab-backed Stream catalog, then tells the server which
release to prepare and play.

## Basic Flow

1. Start the server.
2. Open the web UI.
3. Add at least one NNTP provider under `Configuration -> NNTP Providers`.
4. Add your indexer URL and API key under `Configuration -> Newznab Stream`.
5. In Tater Tube, open the Usenet module and enter the server URL plus the
   server download key.
6. Optional: enable Stremio under `Configuration -> Stremio Integration` if you
   also want the Stremio addon.

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

Run it with one persistent config folder:

```bash
docker run -d \
  --name tater-tube-server \
  -p 8080:8080 \
  -v /path/to/config:/config \
  --restart unless-stopped \
  ghcr.io/tatertotterson/tater-tube-server:latest
```

The included `docker-compose.yml` uses the same image and only mounts
`./config:/config`. Login is disabled by default. Configure Usenet providers,
Newznab Stream, and optional Stremio from the web UI at `http://SERVER-IP:8080`.

Streaming does not download full media files to disk by default. It caches
decoded Usenet segments under `/config/segment-cache` so repeated reads can
avoid re-downloading articles. Size and expiry are configurable in the web UI
under `Configuration -> Streaming`.

Optional FFmpeg transcoding is available under `Configuration -> Streaming`.
Profiles are included for CRT 480p, HDMI 1080p, and HDMI 4K playback. The Docker
image includes FFmpeg; hardware acceleration such as VAAPI, QSV, NVENC,
VideoToolbox, or V4L2 M2M requires the matching host encoder and device access
inside the container.

## Credits

This project is based on [javi11/altmount](https://github.com/javi11/altmount).
The streaming internals, queue/import pipeline, and Stremio endpoint foundation
come from that project.

## License

This project keeps the upstream license terms in [LICENSE](LICENSE).
