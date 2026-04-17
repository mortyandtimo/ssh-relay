Windows Packaging Handoff

Scope:
- 驻阡陌 / Publisher: `apps/desktop-console`
- 证书管家 / CertKeeper: `apps/cert-keeper-desktop`
- 驻阡陌用户端 / User Console: `apps/user-console`

Current packaging decision:
- Both products use NSIS setup exe as the main Windows installer deliverable.
- Portable zip remains a fallback artifact.
- MSI/WiX settings may still exist in Tauri config, but the maintained delivery path in this repo is the custom NSIS chain.

What is already implemented:
- Publisher installer already supports custom install directory, overwrite prompts, running-app quit, desktop shortcut, start menu shortcut, auto-start, launch-now, and uninstall cleanup.
- CertKeeper installer is now aligned to the same finish-page model and shortcut/autostart behavior.
- Shared NSIS execution helper: `scripts/build_windows_nsis_installer.sh`
- Publisher repack flow: `scripts/repack_windows_publisher.sh`
- Frontend dependency reuse helper: `scripts/ensure_npm_dependencies.sh`

Key build entrypoints:
- Build all: `scripts/build_windows_artifacts.sh all`
- Build publisher only: `scripts/build_windows_artifacts.sh publisher`
- Build cert-keeper only: `scripts/build_windows_artifacts.sh cert-keeper`
- Build user console only: `scripts/build_windows_artifacts.sh user`
- Clean rebuild caches: `scripts/clean_windows_packaging_cache.sh all`
- Print Gitee sync workflow: `scripts/prepare_gitee_sync.sh`
- Force fresh npm deps on packager when lockfiles change unexpectedly: `FORCE_NPM_INSTALL=1 ./scripts/packager_build_and_upload.sh all`

Large caches safe to delete:
- `apps/desktop-console/node_modules`
- `apps/desktop-console/src-tauri/target`
- `apps/cert-keeper-desktop/node_modules`
- `apps/cert-keeper-desktop/src-tauri/target`
- `apps/user-console/node_modules`
- `apps/user-console/src-tauri/target`
- `outputs/windows-publisher`
- `outputs/windows-cert-keeper`
- `outputs/windows-user`

External tools required for rebuilds:
- npm / Node.js
- cargo / rustup
- Rust Windows target(s)
- zip
- NSIS `makensis`
- Go for Publisher because it rebuilds `client-agent.exe`
- curl and unzip when the packager auto-prepares EasyTier for User Console from a release zip

Gitee collaboration boundary:
- Current repo remote is already Gitee.
- This cloud machine keeps building and deploying Linux/server-side services locally.
- This cloud machine syncs source code, scripts, README files, and handoff docs to Gitee for the Windows packaging handoff.
- Another Windows build machine performs the actual packaging build and keeps the heavy caches local.
- That build machine now uploads installer artifacts directly through `scripts/upload_windows_artifacts.sh` or `scripts/packager_build_and_upload.sh`.
- `https://manage.020309.top/` now reads the latest uploaded release metadata automatically; no more hand-editing download links after each upload.
- Repo should not store bulky caches or installer binaries.
- The User Console build now auto-stages `deploy/windows/user-console/runtime/easytier-core.exe` on the packager through `scripts/prepare_easytier_runtime.sh`.
- EasyTier source overrides on the packager:
  `EASYTIER_VERSION=vX.Y.Z ./scripts/packager_build_and_upload.sh user`
  `EASYTIER_DOWNLOAD_URL=https://...zip ./scripts/packager_build_and_upload.sh user`
  `EASYTIER_CORE_SOURCE=/path/to/easytier-windows-x86_64-vX.Y.Z.zip ./scripts/packager_build_and_upload.sh user`
  `FORCE_EASYTIER_PREPARE=1 ./scripts/packager_build_and_upload.sh user`

Recommended split:
- Cloud machine: edit code, run server-side tests, build/deploy server-side locally, commit, push to Gitee.
- Packager machine: `git pull --ff-only`, run `./scripts/packager_build_and_upload.sh all`, verify the returned download URLs.

Files to inspect first when resuming work:
- `deploy/windows/publisher/installer.nsi`
- `deploy/windows/cert-keeper/installer.nsi`
- `deploy/windows/user-console/installer.nsi`
- `deploy/windows/user-console/runtime/README.txt`
- `deploy/windows/publisher/build-windows-artifacts.sh`
- `deploy/windows/cert-keeper/build-windows-artifacts.sh`
- `deploy/windows/user-console/build-windows-artifacts.sh`
- `scripts/build_windows_artifacts.sh`
- `scripts/clean_windows_packaging_cache.sh`
- `scripts/prepare_gitee_sync.sh`
- `deploy/windows/publisher/README.txt`
- `deploy/windows/cert-keeper/README.txt`
- `deploy/windows/user-console/README.txt`
