CertKeeper Windows Artifacts

Product summary:
- 证书管家 / CertKeeper 是 SSL 证书管理桌面应用。
- 它负责证书上传、自动签发、DNS 校验与 nginx 相关管理。
- Windows 正式安装链路以 NSIS setup exe 为主，portable zip 为备用交付物。

Expected outputs:
- CertKeeper-x64-portable.zip
- CertKeeperSetup-x64.exe (only when NSIS actually runs)

Payload contents:
- CertKeeper.exe: Tauri desktop shell
- WebView2Loader.dll: WebView runtime loader when bundled by Tauri
- README.txt: local packaging and handoff notes

Installer behavior:
- supports custom install directory
- supports overwrite prompts for existing installs
- requests the running app to quit via `--quit-for-install`
- supports desktop shortcut, start menu shortcut, auto-start, and launch-now choices on finish page
- removes shortcuts, auto-start registry value, and uninstall registry entry during uninstall

Build dependencies:
- npm / Node.js
- cargo / rustup
- Rust Windows target `x86_64-pc-windows-gnu`
- zip
- NSIS `makensis` for setup exe generation

Authoritative build entrypoints:
- single product rebuild: `deploy/windows/cert-keeper/build-windows-artifacts.sh`
- shared build wrapper: `scripts/build_windows_artifacts.sh cert-keeper`
- cache cleanup: `scripts/clean_windows_packaging_cache.sh cert-keeper`
- Gitee sync guidance: `scripts/prepare_gitee_sync.sh`

Build notes:
- `deploy/windows/cert-keeper/build-windows-artifacts.sh` reuses existing `node_modules` by default and only reinstalls when `package.json` / `package-lock.json` fingerprint changes, then rebuilds frontend assets, runs Tauri without bundle, and repacks portable + NSIS outputs.
- `scripts/build_windows_nsis_installer.sh` skips setup generation when `makensis` is missing; portable zip remains available.
- `TAURI_TARGET` can override the default build target. Default is `x86_64-pc-windows-gnu`.
- If you need a full dependency refresh on the packager, run with `FORCE_NPM_INSTALL=1`.

Final artifact locations:
- `outputs/windows-cert-keeper/final/CertKeeper-x64-portable.zip`
- `outputs/windows-cert-keeper/final/CertKeeperSetup-x64.exe` (only if NSIS succeeds)

Large cache directories safe to delete and rebuild:
- `apps/cert-keeper-desktop/node_modules`
- `apps/cert-keeper-desktop/src-tauri/target`
- `outputs/windows-cert-keeper`

Gitee collaboration flow:
- This cloud machine keeps the Linux/server-side build local and only hands the Windows packaging chain to Gitee + packager.
- Another machine pulls from Gitee and runs the build scripts to produce installers.
- Built installers should be uploaded from that build machine with `scripts/upload_windows_artifacts.sh cert-keeper` or the one-shot `scripts/packager_build_and_upload.sh cert-keeper`.
- After upload, `manage.020309.top` downloads switch to the newest uploaded CertKeeper artifact automatically.
- Do not commit large build caches or installer binaries to the repo.

Files future AI should read first:
- `deploy/windows/cert-keeper/installer.nsi`
- `deploy/windows/cert-keeper/build-windows-artifacts.sh`
- `scripts/build_windows_artifacts.sh`
- `scripts/clean_windows_packaging_cache.sh`
- `scripts/prepare_gitee_sync.sh`
