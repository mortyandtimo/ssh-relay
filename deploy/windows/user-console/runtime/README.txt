Place optional Windows runtime companions for the user console here before building on the packager.

Expected file:
- `easytier-core.exe`

Packaging behavior:
- `deploy/windows/user-console/build-windows-artifacts.sh` copies this directory into the final app as `runtime/`.
- The user console looks for `runtime/easytier-core.exe` at startup and reports whether it is bundled.

Notes:
- Do not commit large third-party binaries to the repo unless you intentionally want them versioned here.
- The preferred flow is to have the packager place the exact EasyTier binary in this directory just before running the Windows build.
