# Tater Tube Server for macOS

The native macOS app installs Tater Tube Server without Docker or Terminal. It
runs as a menu bar app, starts the bundled server, opens the dashboard, and
keeps server data separate from the app so updates do not replace it.

## What the App Includes

- The Tater Tube Server backend and web dashboard.
- A private FFmpeg and ffprobe runtime with Apple VideoToolbox support.
- A native template-style menu bar icon with a compact active-stream count.
- State-aware start, stop, restart, dashboard, data-folder, and log controls.
- An optional **Start at Login** setting.
- Signed ZIP updates with SHA-256 verification and in-app installation.

Server data lives in:

```text
~/Library/Application Support/Tater Tube Server
```

The app supports macOS 14 or newer. Release builds use the architecture of the
self-hosted Mac runner; the recommended release runner is an Apple Silicon Mac.

## Local Build

Install Xcode Command Line Tools, Go 1.26, and FFmpeg, then build the app:

```bash
brew install ffmpeg go
macos/TaterTubeServer/scripts/build_app.sh
```

The local build uses ad-hoc signing by default. To build a local DMG:

```bash
macos/TaterTubeServer/scripts/build_dmg.sh
```

Release builds set `TATER_CODESIGN_IDENTITY`, `TATER_NOTARIZE=1`, and
`TATER_NOTARY_PROFILE`. The tag version and GitHub Actions run number are
injected through `TATER_TUBE_VERSION` and `TATER_TUBE_BUILD`.

See [RUNNER_SETUP.md](./RUNNER_SETUP.md) for the dedicated Mac mini runner.
