#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

pushd "$ROOT_DIR" >/dev/null

echo "Gitee remote:"
git remote -v

echo
if [ -n "$(git status --short)" ]; then
  echo "Working tree has changes. Review, commit, then push to Gitee from this machine."
else
  echo "Working tree is clean. Ready to push to Gitee if needed."
fi

echo
echo "Recommended local workflow:"
echo "  1. ./scripts/clean_windows_packaging_cache.sh all"
echo "  2. update code/scripts/docs"
echo "  3. git status"
echo "  4. git add <files> && git commit"
echo "  5. git push origin <branch>"
echo
echo "Build boundary after this fix:"
echo "  - This cloud VM continues to build and deploy Linux/server-side services locally."
echo "  - Only the Windows desktop packaging chain is handed to the Windows packager."
echo "  - The Windows packager pulls from Gitee, rebuilds heavy caches locally, and uploads installers."
echo "  - manage.020309.top download cards read the latest uploaded release metadata automatically."
echo
echo "Recommended build machine workflow:"
echo "  1. export SERVER_URL=https://manage.020309.top"
echo "  2. export COOKIE_FILE=/path/to/admin.cookies"
echo "  3. export VERSION=<release-version>   # optional but recommended for formal releases"
echo "  4. ./scripts/packager_build_and_upload.sh all"
echo
echo "Direct manual equivalents remain available:"
echo "  - ./scripts/build_windows_artifacts.sh [publisher|cert-keeper|all]"
echo "  - ./scripts/upload_windows_artifacts.sh [publisher|cert-keeper|all]"
echo
echo "Note: this script is informational only; it does not push code and does not upload installers."

popd >/dev/null
