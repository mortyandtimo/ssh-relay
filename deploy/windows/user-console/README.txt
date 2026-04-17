Cloud Relay User Console Windows Artifacts

Product summary:
- 驻阡陌用户端 / Cloud Relay User Console 是给用户操作机器使用的独立桌面壳。
- 它负责网盘访问、图床批量上传入口、P2P 本地策略与用户端登录体验。
- Windows 正式安装链路以 NSIS setup exe 为主，portable zip 为备用交付物。

Expected outputs:
- CloudRelayUser-x64-portable.zip
- CloudRelayUserSetup-x64.exe (only when NSIS actually runs)

Payload contents:
- CloudRelayUser.exe: Tauri desktop shell
- runtime/easytier-core.exe: optional EasyTier runtime binary bundled by the packager when present
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
- single product rebuild: `deploy/windows/user-console/build-windows-artifacts.sh`
- shared build wrapper: `scripts/build_windows_artifacts.sh user`
- cache cleanup: `scripts/clean_windows_packaging_cache.sh user`
- Gitee sync guidance: `scripts/prepare_gitee_sync.sh`

Build notes:
- `deploy/windows/user-console/build-windows-artifacts.sh` reuses existing `node_modules` by default and only reinstalls when `package.json` / `package-lock.json` fingerprint changes, then rebuilds frontend assets, runs Tauri without bundle, and repacks portable + NSIS outputs.
- If `deploy/windows/user-console/runtime/` exists on the packager, its contents are copied verbatim into the final package under `runtime/`.
- To make P2P actually runnable, place `easytier-core.exe` in `deploy/windows/user-console/runtime/` before building on the packager.
- `scripts/build_windows_nsis_installer.sh` skips setup generation when `makensis` is missing; portable zip remains available.
- `TAURI_TARGET` can override the default build target. Default is `x86_64-pc-windows-gnu`.
- If you need a full dependency refresh on the packager, run with `FORCE_NPM_INSTALL=1`.

Final artifact locations:
- `outputs/windows-user/final/CloudRelayUser-x64-portable.zip`
- `outputs/windows-user/final/CloudRelayUserSetup-x64.exe` (only if NSIS succeeds)

Large cache directories safe to delete and rebuild:
- `apps/user-console/node_modules`
- `apps/user-console/src-tauri/target`
- `outputs/windows-user`

Gitee collaboration flow:
- This cloud machine keeps the Linux/server-side build local and only hands the Windows packaging chain to Gitee + packager.
- Another machine pulls from Gitee and runs the build scripts to produce installers.
- Built installers should be uploaded from that build machine with `scripts/upload_windows_artifacts.sh user` or the one-shot `scripts/packager_build_and_upload.sh user`.
- After upload, `manage.020309.top` downloads switch to the newest uploaded user-console artifact automatically.
- Do not commit large build caches or installer binaries to the repo.

Files future AI should read first:
- `deploy/windows/user-console/installer.nsi`
- `deploy/windows/user-console/build-windows-artifacts.sh`
- `scripts/build_windows_artifacts.sh`
- `scripts/clean_windows_packaging_cache.sh`
- `scripts/prepare_gitee_sync.sh`
