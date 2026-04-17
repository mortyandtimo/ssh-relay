Cloud Relay Publisher Windows Artifacts

Product summary:
- 驻阡陌 / Cloud Relay Publisher 是 Windows 10/11 本机服务发布器。
- 它负责管理本机服务、绑定云端入口、验证访问结果，并在后台托管 `client-agent.exe`。
- 当前打包链会把 EasyTier runtime 一并揉进 `runtime/`，用于后续服务端 P2P 能力接入。
- Windows 正式安装链路以 NSIS setup exe 为主，portable zip 为备用交付物。

Expected outputs:
- CloudRelayPublisher-x64-portable.zip
- CloudRelayPublisherSetup-x64.exe (only when NSIS actually runs)

Payload contents:
- CloudRelayPublisher.exe: Tauri desktop shell
- runtime/client-agent.exe: embedded reverse tunnel runtime
- runtime/easytier-core.exe / easytier-cli.exe / 相关 sidecar: bundled EasyTier runtime payload
- desktop-config.json: sample config for local relay endpoint selection

Installer behavior:
- supports custom install directory
- supports overwrite prompts for existing installs
- requests the running app to quit via `--quit-for-install`
- supports desktop shortcut, start menu shortcut, auto-start, and launch-now choices on finish page
- removes shortcuts, auto-start registry value, and uninstall registry entry during uninstall

Build dependencies:
- npm / Node.js
- cargo / rustup
- one Rust Windows target: `x86_64-pc-windows-gnu` or `x86_64-pc-windows-msvc`
- Go (to rebuild `client-agent.exe`)
- zip
- NSIS `makensis` for setup exe generation

Authoritative build entrypoints:
- single product rebuild: `deploy/windows/publisher/build-windows-artifacts.sh`
- shared build wrapper: `scripts/build_windows_artifacts.sh publisher`
- cache cleanup: `scripts/clean_windows_packaging_cache.sh publisher`
- Gitee sync guidance: `scripts/prepare_gitee_sync.sh`

Build notes:
- `deploy/windows/publisher/build-windows-artifacts.sh` reuses existing `node_modules` by default and only reinstalls when `package.json` / `package-lock.json` fingerprint changes, then rebuilds frontend assets, runs Tauri without bundle, and repacks portable + NSIS outputs.
- `scripts/repack_windows_publisher.sh` deletes stale `final/CloudRelayPublisherSetup-x64.exe` before packaging so old installers are not reused.
- `scripts/build_windows_nsis_installer.sh` skips setup generation when `makensis` is missing; portable zip remains available.
- `TAURI_TARGET` can override the default build target. Default is `x86_64-pc-windows-gnu`.
- If you need a full dependency refresh on the packager, run with `FORCE_NPM_INSTALL=1`.

Final artifact locations:
- `outputs/windows-publisher/final/CloudRelayPublisher.exe`
- `outputs/windows-publisher/final/CloudRelayPublisher-x64-portable.zip`
- `outputs/windows-publisher/final/CloudRelayPublisherSetup-x64.exe` (only if NSIS succeeds)

Large cache directories safe to delete and rebuild:
- `apps/desktop-console/node_modules`
- `apps/desktop-console/src-tauri/target`
- `outputs/windows-publisher`

Gitee collaboration flow:
- This cloud machine keeps the Linux/server-side build local and only hands the Windows packaging chain to Gitee + packager.
- Another machine pulls from Gitee and runs the build scripts to produce installers.
- Built installers should be uploaded from that build machine with `scripts/upload_windows_artifacts.sh publisher` or the one-shot `scripts/packager_build_and_upload.sh publisher`.
- After upload, `manage.020309.top` downloads switch to the newest uploaded publisher artifact automatically.
- Do not commit large build caches or installer binaries to the repo.

Files future AI should read first:
- `deploy/windows/publisher/installer.nsi`
- `deploy/windows/publisher/build-windows-artifacts.sh`
- `scripts/repack_windows_publisher.sh`
- `scripts/build_windows_artifacts.sh`
- `scripts/clean_windows_packaging_cache.sh`
- `scripts/prepare_gitee_sync.sh`
