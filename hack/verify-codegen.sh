#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

make generate
make manifests

if ! git diff --quiet -- api config; then
  echo "Generated Go code or manifests are out of date. Run make generate && make manifests and commit the result." >&2
  git diff -- api config >&2
  exit 1
fi
