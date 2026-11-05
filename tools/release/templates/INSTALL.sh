#!/usr/bin/env bash
set -euo pipefail
pkg_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
target="${UNBURGER_INSTALL_ROOT:-$PWD}"
if [[ ! -f "$pkg_dir/manifest.json" || ! -d "$pkg_dir/payload" ]]; then
  echo 'Invalid complete release envelope' >&2
  exit 1
fi
go run "$pkg_dir/installer.go" "$pkg_dir" "$target"
