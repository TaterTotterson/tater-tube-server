# Tater Tube Server

<p align="center">
  <img src="./frontend/public/tater-tube-logo.png" alt="Tater Tube" width="520" />
</p>

<p align="center">
  <a href="https://tatertube.tv">tatertube.tv</a>
</p>

Tater Tube Server is the backend for Tater Tube players. It provides The Tube
catalog, Newznab-backed streaming, local media libraries, player pairing,
optional FFmpeg transcoding, queue status, logs, and a mobile-friendly setup UI.

## Tater Tube Player

This server is designed to pair with the Tater Tube player image. The player
repo has the Raspberry Pi images, module setup, built-in updater details, and
device-specific notes for CRT composite and HDMI builds.

- Player GitHub: [TaterTotterson/Tater-Tube](https://github.com/TaterTotterson/Tater-Tube)
- Project website: [tatertube.tv](https://tatertube.tv)

## Quick Start

1. Start the server with Docker.
2. Open `http://SERVER-IP:8080`.
3. Add at least one NNTP provider under `Configuration -> NNTP Providers`.
4. Add your Newznab URL and API key under `Configuration -> Newznab Stream`.
5. Create a player PIN under `Configuration -> Tater Tube Players`.
6. On the Tater Tube player, open The Tube and enter the server URL plus PIN.

Login is disabled by default. Configure users later from the web UI if you want
to lock down the server dashboard.

## Docker

The release image is published to GitHub Container Registry:

```bash
docker pull ghcr.io/tatertotterson/tater-tube-server:latest
```

Minimal run command:

```bash
docker run -d \
  --name tater-tube-server \
  -p 8080:8080 \
  -v /path/to/tater-tube-server/config:/config \
  --restart unless-stopped \
  ghcr.io/tatertotterson/tater-tube-server:latest
```

Docker Compose:

```yaml
services:
  tater-tube-server:
    image: ghcr.io/tatertotterson/tater-tube-server:latest
    container_name: tater-tube-server
    ports:
      - "8080:8080"
    volumes:
      - /path/to/tater-tube-server/config:/config
    restart: unless-stopped
```

The only required volume is `/config`. It stores server settings, player pairing
tokens, stream metadata, and the segment cache.

Unraid template icon URL:

```text
https://raw.githubusercontent.com/TaterTotterson/tater-tube-server/main/frontend/public/unraid-icon.png
```

## Local Media Mapping

To use Local Media, mount host media folders into the container, then add the
container paths in `Configuration -> Local Media`.

Example:

```yaml
services:
  tater-tube-server:
    image: ghcr.io/tatertotterson/tater-tube-server:latest
    container_name: tater-tube-server
    ports:
      - "8080:8080"
    volumes:
      - /mnt/user/appdata/tater-tube-server/config:/config
      - /mnt/user/media/movies:/media/movies:ro
      - /mnt/user/media/tv:/media/tv:ro
      - /mnt/user/media/music:/media/music:ro
      - /mnt/user/media/home-videos:/media/home-videos:ro
    restart: unless-stopped
```

Then add categories like:

| Category | Library Type | Folder Path |
| --- | --- | --- |
| Movies | Movies | `/media/movies` |
| TV Shows | TV Shows | `/media/tv` |
| Music | Music | `/media/music` |
| Home Videos | Folders | `/media/home-videos` |

Library types:

- `Movies` shows a clean movie title list.
- `TV Shows` browses as show, season, then episode.
- `Music` scans album folders for Tape Deck when Tater Tube Server is selected as its provider.
- `Folders` keeps the original directory structure.

Use container paths, not host paths, inside the Tater Tube Server web UI.

## Hardware Transcoding

Transcoding is optional and lives under `Configuration -> Hardware Transcoding`.
The Docker image includes a bundled FFmpeg build with Intel QSV/VAAPI driver
support. Profiles are included for CRT 480p, HDMI 1080p, and HDMI 4K playback.

For Intel, AMD, Raspberry Pi, and other `/dev/dri` hardware encoders, pass the
device into the container:

```yaml
services:
  tater-tube-server:
    image: ghcr.io/tatertotterson/tater-tube-server:latest
    devices:
      - /dev/dri:/dev/dri
    volumes:
      - /path/to/tater-tube-server/config:/config
```

For NVIDIA, install the NVIDIA Container Toolkit on the host, then run with GPU
access:

```bash
docker run -d \
  --name tater-tube-server \
  --gpus all \
  -p 8080:8080 \
  -v /path/to/tater-tube-server/config:/config \
  ghcr.io/tatertotterson/tater-tube-server:latest
```

The dashboard shows detected hardware and active playback cards show whether a
stream is direct play, software transcode, or hardware transcode.

## Updates

For Docker installs:

```bash
docker pull ghcr.io/tatertotterson/tater-tube-server:latest
docker restart tater-tube-server
```

The built-in updater can also update Docker installs when the container has
access to the Docker socket and Docker CLI.

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

## Credits

This project is based on [javi11/altmount](https://github.com/javi11/altmount).
The streaming internals and queue/import pipeline come from that project.

## License

Tater Tube Server is licensed under the [GNU Affero General Public License v3.0](LICENSE).
Portions derived from AltMount retain the upstream MIT notice in [NOTICE](NOTICE).
