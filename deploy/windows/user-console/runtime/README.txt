Place Windows runtime companions for the user console here. The packager now fills this directory automatically when building `user-console`.

Expected primary file:
- `easytier-core.exe`

Packaging behavior:
- `deploy/windows/user-console/build-windows-artifacts.sh` copies this directory into the final app as `runtime/`.
- The user console looks for `runtime/easytier-core.exe` at startup and reports whether it is bundled.
- `scripts/prepare_easytier_runtime.sh` populates this directory before a user-console build unless `SKIP_EASYTIER_PREPARE=1`.

Notes:
- Do not commit large third-party binaries to the repo unless you intentionally want them versioned here.
- Preferred flow on the packager:
  `./scripts/build_windows_artifacts.sh user`
- Override sources when needed:
  `EASYTIER_VERSION=v2.4.5 ./scripts/build_windows_artifacts.sh user`
  `EASYTIER_CORE_SOURCE=/path/to/easytier-windows-x86_64-v2.4.5.zip ./scripts/build_windows_artifacts.sh user`
  `FORCE_EASYTIER_PREPARE=1 ./scripts/build_windows_artifacts.sh user`
- If you need full manual control, set `SKIP_EASYTIER_PREPARE=1` and place the runtime files here yourself before packaging.
