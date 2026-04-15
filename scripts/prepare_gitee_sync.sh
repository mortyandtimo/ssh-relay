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
echo "Recommended build machine workflow:"
echo "  1. git pull origin <branch>"
echo "  2. ./scripts/build_windows_artifacts.sh publisher"
echo "  3. ./scripts/build_windows_artifacts.sh cert-keeper"
echo "  4. upload built installers manually"
echo
echo "Note: this script does not push code and does not upload installers."

popd >/dev/null
