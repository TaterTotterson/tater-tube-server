# macOS Release Runner

Use a dedicated Apple Silicon Mac mini for the `Build macOS App` release job.
The job only runs on a self-hosted runner carrying both the standard `macOS`
label and the custom `tater-tube-server` label, so it will not take work meant
for the existing Tater app runner.

## 1. Prepare the Mac

Install Xcode and open it once to accept its license, then install the build
dependencies:

```bash
xcode-select --install
brew install ffmpeg-full molten-vk go
```

The runner needs:

- macOS 14 or newer.
- Xcode/Swift and the Xcode Command Line Tools.
- Go 1.26 or newer.
- Homebrew `ffmpeg-full`, ffprobe, and `molten-vk`. The release build requires
  VideoToolbox, zscale, and libplacebo support and bundles MoltenVK so FSRCNNX
  works without Homebrew on the installed Mac.
- The **Developer ID Application** certificate and private key in the runner
  user's login keychain.

## 2. Register a Separate GitHub Runner

In the repository, open **Settings -> Actions -> Runners -> New self-hosted
runner**, choose macOS ARM64, and follow GitHub's download instructions. Use a
separate folder and work directory from the Tater app runner. During
configuration, use values similar to:

```bash
./config.sh \
  --url https://github.com/TaterTotterson/tater-tube-server \
  --token TOKEN_FROM_GITHUB \
  --name tater-tube-server-mac-mini \
  --labels tater-tube-server \
  --work _work-tater-tube-server

./svc.sh install
./svc.sh start
./svc.sh status
```

GitHub automatically adds the `self-hosted`, `macOS`, and architecture labels.
The workflow adds `tater-tube-server` as the final selector.

## 3. Configure Signing and Notarization

Confirm the signing identity is visible to the runner user:

```bash
security find-identity -v -p codesigning
```

Store notarization credentials in that user's keychain:

```bash
xcrun notarytool store-credentials tater-notary \
  --apple-id APPLE_ID \
  --team-id 8BHV7Y2D9D \
  --password APP_SPECIFIC_PASSWORD
```

Repository Actions variables used by the job:

| Variable | Recommended value |
| --- | --- |
| `TATER_CODESIGN_IDENTITY` | `Developer ID Application: Tater Totterson AI LLC (8BHV7Y2D9D)` |
| `TATER_NOTARIZE` | `1` |
| `TATER_NOTARY_PROFILE` | `tater-notary` |

The service runs as the configured macOS user. Keep that user's login keychain
available to the runner so `codesign` and `notarytool` can access the identity
and stored profile.

## 4. Release Output

Every server tag release builds and notarizes:

- `Tater-Tube-Server-vX.Y.Z.dmg` for installation.
- `Tater-Tube-Server-vX.Y.Z.zip` for in-app updates.
- `update-manifest.json` containing the version, build, URL, and SHA-256.

The workflow uploads all three files to the existing GitHub Release and updates
the manifest on `main` when the tag points at the current `main` commit.
