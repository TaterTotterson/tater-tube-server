# Tater Tube Server

<p align="center">
  <img src="./frontend/public/tater-tube-logo-leaning-transparent.png" alt="Tater Tube" width="520" />
</p>

<p align="center">
  <a href="https://tatertube.tv">
    <img alt="Visit Tater Tube" src="https://img.shields.io/badge/Tater%20Tube-Visit%20Website-F28C28?style=for-the-badge&logo=googlechrome&logoColor=white" />
  </a>
  <a href="https://github.com/TaterTotterson/Tater-Tube-Player">
    <img alt="Tater Tube Player" src="https://img.shields.io/badge/Tater%20Tube-Player-2D333B?style=for-the-badge&logo=github&logoColor=white" />
  </a>
  <a href="https://discord.gg/w52namKyXT">
    <img alt="Join the community" src="https://img.shields.io/badge/Discord-Join%20the%20Community-5865F2?style=for-the-badge&logo=discord&logoColor=white" />
  </a>
</p>

Tater Tube Server is the self-hosted backend for Tater Tube players. It brings
your local libraries and configured streaming sources together, serves paired
players, builds Tube TV channels, and provides automatic video transcoding for
playback compatibility.

## Features

- Local movies, series, music, and folder-based libraries.
- Newznab-powered discovery using your configured NNTP providers.
- Shared Tube TV channels with schedules, commercials, bumpers, and logos.
- Player pairing, playback activity, and watch-progress tracking.
- Direct play plus software, NVIDIA, AMD, Intel, and Apple transcoding options.
- Browser-based setup, stream monitoring, queues, logs, and updates.

## Quick Start

Choose the installation that matches the server computer.

### macOS App

On an Apple Silicon Mac running macOS 14 or newer, download the latest
`Tater-Tube-Server-vX.Y.Z.dmg` from
[GitHub Releases](https://github.com/TaterTotterson/tater-tube-server/releases),
drag **Tater Tube Server** to Applications, and open it. The server starts from
its own menu bar icon and opens the dashboard automatically the first time.

The app includes FFmpeg with Apple VideoToolbox support. Use the menu bar icon
to open the dashboard, restart the server, view logs, open its data folder,
enable **Start at Login**, or check for updates. Configuration, libraries,
metadata, logs, and playback data remain in:

```text
~/Library/Application Support/Tater Tube Server
```

### Docker or Unraid

Docker Compose is the recommended installation for Linux servers and NAS
systems:

```yaml
services:
  tater-tube-server:
    image: ghcr.io/tatertotterson/tater-tube-server:latest
    container_name: tater-tube-server
    ports:
      - "8080:8080"
    volumes:
      - /path/to/tater-tube-server/config:/config
      # Add any local libraries you want Tater Tube to serve:
      - /path/to/movies:/media/movies:ro
      - /path/to/tv:/media/tv:ro
    restart: unless-stopped
```

Before starting the container, add the appropriate GPU access from
[Hardware Transcoding](#hardware-transcoding). This is strongly recommended for
Tube TV and for players that cannot direct-play a source format or resolution.

```bash
docker compose up -d
```

After the macOS app or container is running:

1. Open `http://SERVER-IP:8080`.
2. Open `Configuration -> Hardware Transcoding`, run **Auto Detect**, confirm the
   encoder says **Ready**, and select **Save**.
3. For a local library, open `Configuration -> Local Media`, select the library
   type, add its container path (such as `/media/movies`), select **Save Local
   Media**, and then select **Scan Libraries**.
4. Optionally configure an NNTP provider and Newznab service to add the Stream
   discovery catalog. These are not required for local-library playback.
5. Open `Configuration -> Tater Tube Players` and create a pairing code.
6. Enter the server address and pairing code in the Tater Tube Player.

The Docker `/config` volume, or the macOS Application Support folder, stores the
configuration, database, logs, metadata, pairing information, and segment
cache. Login is disabled by default; enable authentication before exposing the
dashboard outside your trusted network.

## Hardware Transcoding

Configure hardware transcoding during the initial installation. Direct-play
titles do not use it, but Tube TV and incompatible media need a working
transcoder. Without GPU acceleration, the server falls back to CPU encoding,
which may not keep up with higher resolutions.

| Hardware | Mode | Container access |
| --- | --- | --- |
| NVIDIA | NVENC | GPU access plus NVIDIA video capabilities |
| AMD on Linux | VAAPI | Map `/dev/dri` |
| Intel on Linux | Quick Sync or VAAPI | Map `/dev/dri` |
| Apple native | VideoToolbox | No Docker device mapping |

After changing container settings, recreate the container. Open
`Configuration -> Hardware Transcoding`, select **Auto Detect**, confirm the
encoder says **Ready**, and select **Save**. Auto Detect uses a lightweight
capability check; playback validates the actual requested resolution before it
uses the encoder. New installations default to **Auto**; existing installations
keep their saved selection.

### NVIDIA / NVENC

Install the NVIDIA driver and Container Toolkit, or the NVIDIA Driver plugin on
Unraid. Docker Compose needs:

```yaml
services:
  tater-tube-server:
    gpus: all
    environment:
      NVIDIA_DRIVER_CAPABILITIES: all
```

On Unraid, add:

- **Extra Parameters:** `--gpus all`
- **Variable:** `NVIDIA_DRIVER_CAPABILITIES=all`

Confirm the GPU is visible with:

```bash
docker exec tater-tube-server nvidia-smi
```

If the probe reports an allocation failure, check `nvidia-smi` for other
processes consuming GPU memory. Auto Detect itself does not run a 4K workload.

### AMD / VAAPI

AMD GPUs use VAAPI on Linux. Add the device mapping below, then run Auto Detect
and select **VAAPI**. The GPU must include a hardware video encoder.

```yaml
devices:
  - /dev/dri:/dev/dri
```

On Unraid, add a **Device** with `/dev/dri` as both the host and container path.
Leave **Hardware Device** blank unless you need a particular render node, such
as `/dev/dri/renderD129`. At startup, the image adds its service user to the
numeric groups assigned to the mapped DRM devices.

### Intel / Quick Sync

Intel integrated graphics uses the same `/dev/dri` mapping. Auto Detect may
recommend **Intel Quick Sync** or **VAAPI**, depending on the GPU and driver.

### If It Still Shows Software

- Confirm the expected encoder says **Ready**, then select **Save**.
- NVIDIA needs `NVIDIA_DRIVER_CAPABILITIES=all`, not only `--gpus all`.
- AMD and Intel need a visible `/dev/dri/renderD*` device.
- Direct play does not use the GPU; test with a stream that requires transcoding.
- Check the server logs for the encoder-probe failure reason.

The x86_64 image includes NVENC, VAAPI, zscale, libplacebo, and the bundled
Intel/AMD VAAPI and Vulkan drivers used by the supported processing modes.

## Upscaling

Open `Configuration -> Upscaling` to choose how the server enlarges video for
Tater Tube TV players. Upscaling only runs when the connected TV resolution is
higher than the media resolution; other playback is left unchanged.

- **Auto AI** uses the selected AI model when the server passes its
  compatibility check and automatically steps down through lighter models.
- **AI Upscaling** prioritizes the selected experimental neural model with the
  same safe fallback behavior.
- **Standard** uses Spline36 and remains the default for broad compatibility.
- **Off** sends the original resolution and leaves enlargement to the player or
  display.

AI model choices:

- **FSRCNNX Fast** is the default 8-feature model for responsive general video.
- **FSRCNNX Quality** uses the larger 16-feature model for finer general-video
  detail.
- **ArtCNN Balanced** uses C4F16 for animation, line art, and CG.
- **ArtCNN Quality** uses the heavier C4F32 animation model on faster GPUs.

AI currently targets compatible SDR upscales up to 2×. It needs an FFmpeg build
with libplacebo plus a working Vulkan device. Docker images include the pinned
AI shaders; the macOS app also includes MoltenVK so Apple Silicon can run the
same models through Metal. If a selected model cannot initialize, Tater Tube
tries its lighter fallback chain before continuing with Standard upscaling.

## Unraid

| Setting | Value |
| --- | --- |
| Repository | `ghcr.io/tatertotterson/tater-tube-server:latest` |
| Web UI port | `8080` |
| Config path | Your appdata folder mapped to `/config` |
| Local libraries | Host media folders mapped beneath `/media` |

Template icon:

```text
https://raw.githubusercontent.com/TaterTotterson/tater-tube-server/main/frontend/public/unraid-icon.png
```

GPU-specific Unraid settings are listed in
[Hardware Transcoding](#hardware-transcoding).

## Local Media

Add media folders to the Compose `volumes` list or as Unraid path mappings:

```yaml
volumes:
  - /mnt/user/media/movies:/media/movies:ro
  - /mnt/user/media/tv:/media/tv:ro
  - /mnt/user/media/music:/media/music:ro
  - /mnt/user/media/home-videos:/media/home-videos:ro
```

Use the **container path** in the server dashboard:

| Library type | Example path | Player layout |
| --- | --- | --- |
| Movies | `/media/movies` | Movie titles |
| TV Shows | `/media/tv` | Show, season, and episode |
| Music | `/media/music` | Artist, album, and track |
| Folders | `/media/home-videos` | Original folder structure |

Finish the library setup in the dashboard:

1. Open `Configuration -> Local Media`.
2. Choose **Movies**, **TV Shows**, **Music**, or **Folders**, then select
   **Add Library** for that type.
3. Select **Add Folder** and choose its mapped container path.
4. Select **Save Local Media**, then **Scan Libraries**.

The scanned library will appear on every paired Tater Tube Player.

Read-only (`:ro`) mounts support scanning and playback. Use a writable mount if
you want the server to save artwork or NFO metadata beside the media. Optional
TMDB matching uses the API key you provide.

## Tube TV

Tube TV turns server libraries into shared live-style channels. Paired players
receive the same schedule, channel numbers, commercial breaks, bumpers, and
optional logos. Configure it under `Configuration -> Tube TV`, then view the
result on the **TV Guide** page.

## Updating

```bash
docker compose pull
docker compose up -d
```

On Unraid, check for and apply the container update. Pulling an image and only
restarting the old container does not install the new image. Your data remains
in `/config` when the container is recreated.

## Development

The current toolchain uses Go 1.26 and Node.js 24.

```bash
make build-frontend
go run ./cmd/tater-tube-server serve --config ./config.yaml
```

Run the frontend development server with `cd frontend && npm ci && npm run dev`.

## Credits and License

Tater Tube Server is based on
[javi11/altmount](https://github.com/javi11/altmount), and Tube TV logo browsing
uses [tv-logo/tv-logos](https://github.com/tv-logo/tv-logos).

The server is licensed under the [GNU AGPL v3](LICENSE). Portions derived from
AltMount retain the upstream MIT notice in [NOTICE](NOTICE).
