# Tater Tube Server

<p align="center">
  <img src="./frontend/public/tater-tube-logo.png" alt="Tater Tube" width="520" />
</p>

<p align="center">
  <a href="https://tatertube.tv">tatertube.tv</a>
</p>

Tater Tube Server is the backend foundation for Tater Tube. The first supported
server role is Usenet/NZB streaming: Tater Tube browses a Newznab-backed Stream
catalog on the server, selects a release, and receives a playable stream URL.

This project keeps the streaming pipeline from AltMount, then trims the app
down around Tater Tube player setup, provider configuration, queue status, logs,
and system settings. WebDAV, FUSE, and rclone mount workflows have been removed
from the app.

## What It Keeps

- Tater Tube Stream catalog endpoints under `/api/tater/usenet/*`.
- Tater Tube player pairing with short-lived PINs.
- NNTP provider configuration.
- Newznab indexer configuration for Tater Tube players.
- Local Media categories that expose server or container folders to The Tube.
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
5. Open `Configuration -> Tater Tube Players` and create a pairing PIN.
6. In Tater Tube, open The Tube and enter the server URL plus the PIN.

Local Media is optional. In Docker, mount any media folders into the container,
then add the container paths under `Configuration -> Local Media`. Each local
category can scan as Movies, TV Shows, or Folders. Movies are flattened into a
clean title list, TV Shows browse as Show -> Season -> Episode, and Folders keeps
the original directory structure.

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
Newznab Stream, and paired Tater Tube players from the web UI at
`http://SERVER-IP:8080`.

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
The streaming internals and queue/import pipeline come from that project.

## License

This project keeps the upstream license terms in [LICENSE](LICENSE).
