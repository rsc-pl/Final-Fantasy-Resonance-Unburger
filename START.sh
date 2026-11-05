#!/usr/bin/env bash
# One entry point for the project. It builds the single production binary into .local/bin, rebuilds only when
# the build inputs changed, and never runs a stale binary after a failed build.
#
# Works from any directory and needs no interactive shell: run it from fish, from a script, from cron.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$ROOT/.local/bin"
BIN="$BIN_DIR/unburger"
FP_FILE="$BIN_DIR/.fingerprint"
LOG_FILE="$BIN_DIR/build.log"

mkdir -p "$BIN_DIR"

usage() {
	cat <<'EOF'
usage: ./START.sh [command] [arguments]

  (none) | tui    open the corpus browser
  build            compose and package every build target (UNVERIFIED runtime state)
  package          run the same composition and packaging path as build
  status           show the configured project and corpus counts
  doctor           file hygiene, structure and corpus validation
  provenance       summarize corpus review state
  corpus ...       validate|build|export|import|search|scenes
  thread [scene]   print one scene or list the scene namespaces
  locres fetch     extract localisation resources from the configured archives
  testmod ...      compose one private test change
  verify-mod ...   check the mount layout of a mod archive or directory
  patch-pak ...    build one patch archive from a localisation resource
  verify-pak ...   verify a patch archive
  rebuild          rebuild the binary even when the fingerprint is unchanged
  help             this text

The binary is built into .local/bin/unburger and rebuilt only when the code or its inputs change.
Run .local/bin/unburger help for the complete binary command list.
EOF
}

fingerprint() {
	(
		cd "$ROOT"
		find cmd internal tools go.mod schemas -type f \( -name '*.go' -o -name '*.json' -o -name 'go.mod' \) -print0 2>/dev/null |
			LC_ALL=C sort -z |
			xargs -0 sha256sum 2>/dev/null |
			sha256sum |
			cut -d' ' -f1
	)
}

build_binary() {
	local fp tmp
	fp="$(fingerprint)"
	if [ "${1:-}" != "force" ] && [ -x "$BIN" ] && [ -f "$FP_FILE" ] && [ "$(cat "$FP_FILE")" = "$fp" ]; then
		return 0
	fi
	echo "building unburger (inputs changed)" >&2
	tmp="$BIN.tmp.$$"
	if ! (cd "$ROOT" && go build -trimpath -o "$tmp" ./cmd/unburger) >"$LOG_FILE" 2>&1; then
		echo "BUILD FAILED — see $LOG_FILE" >&2
		tail -n 20 "$LOG_FILE" >&2 || true
		rm -f "$tmp"
		echo "not running a previous binary: the source and the binary would disagree" >&2
		exit 3
	fi
	mv -f "$tmp" "$BIN"
	printf '%s\n' "$fp" >"$FP_FILE"
}

cmd="${1:-tui}"
case "$cmd" in
help | -h | --help)
	usage
	exit 0
	;;
rebuild)
	build_binary force
	shift || true
	exec "$BIN" "${@:-status}"
	;;
tui | build | package | doctor | status | provenance | "corpus" | thread | locres | testmod | verify-mod | patch-pak | verify-pak)
	build_binary
	shift || true
	exec "$BIN" "$cmd" "$@"
	;;
*)
	usage >&2
	exit 2
	;;
esac
