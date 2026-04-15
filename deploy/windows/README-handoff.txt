Windows Packaging Handoff

Scope:
- 驻阡陌 / Publisher: `apps/desktop-console`
- 证书管家 / CertKeeper: `apps/cert-keeper-desktop`

Current packaging decision:
- Both products use NSIS setup exe as the main Windows installer deliverable.
- Portable zip remains a fallback artifact.
- MSI/WiX settings may still exist in Tauri config, but the maintained delivery path in this repo is the custom NSIS chain.

What is already implemented:
- Publisher installer already supports custom install directory, overwrite prompts, running-app quit, desktop shortcut, start menu shortcut, auto-start, launch-now, and uninstall cleanup.
- CertKeeper installer is now aligned to the same finish-page model and shortcut/autostart behavior.
- Shared NSIS execution helper: `scripts/build_windows_nsis_installer.sh`
- Publisher repack flow: `scripts/repack_windows_publisher.sh`

Key build entrypoints:
- Build all: `scripts/build_windows_artifacts.sh all`
- Build publisher only: `scripts/build_windows_artifacts.sh publisher`
- Build cert-keeper only: `scripts/build_windows_artifacts.sh cert-keeper`
- Clean rebuild caches: `scripts/clean_windows_packaging_cache.sh all`
- Print Gitee sync workflow: `scripts/prepare_gitee_sync.sh`

Large caches safe to delete:
- `apps/desktop-console/node_modules`
- `apps/desktop-console/src-tauri/target`
- `apps/cert-keeper-desktop/node_modules`
- `apps/cert-keeper-desktop/src-tauri/target`
- `outputs/windows-publisher`
- `outputs/windows-cert-keeper`

External tools required for rebuilds:
- npm / Node.js
- cargo / rustup
- Rust Windows target(s)
- zip
- NSIS `makensis`
- Go for Publisher because it rebuilds `client-agent.exe`

Gitee collaboration boundary:
- Current repo remote is already Gitee.
- This machine syncs source code, scripts, README files, and handoff docs.
- Another machine performs the actual Windows packaging build.
- That build machine uploads installer artifacts manually.
- This machine later places approved installers into the download center.
- Repo should not store bulky caches or installer binaries.

Files to inspect first when resuming work:
- `deploy/windows/publisher/installer.nsi`
- `deploy/windows/cert-keeper/installer.nsi`
- `deploy/windows/publisher/build-windows-artifacts.sh`
- `deploy/windows/cert-keeper/build-windows-artifacts.sh`
- `scripts/build_windows_artifacts.sh`
- `scripts/clean_windows_packaging_cache.sh`
- `scripts/prepare_gitee_sync.sh`
- `deploy/windows/publisher/README.txt`
- `deploy/windows/cert-keeper/README.txt`
