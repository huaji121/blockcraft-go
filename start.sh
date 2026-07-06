#!/usr/bin/env bash
# Build and run Blockcraft-go.
#
# Usage:
#   ./start.sh                # build, then run
#   ./start.sh --no-build     # skip the build step
#
# Exits non-zero if the build fails or the game returns an error.

set -euo pipefail

# Resolve the project root from the script location so it can be run from
# anywhere (e.g. `bash /path/to/start.sh`).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

BUILD=1
for arg in "$@"; do
	case "$arg" in
		--no-build) BUILD=0 ;;
		-h|--help)
			echo "Usage: $0 [--no-build]"
			exit 0
			;;
		*)
			echo "Unknown option: $arg" >&2
			exit 2
			;;
	esac
done

# Pick the binary name for the host OS so this works on Windows, Linux and macOS.
case "$(uname -s)" in
	MINGW*|MSYS*|CYGWIN*) BIN="blockcraft.exe" ;;
	*)                    BIN="blockcraft"     ;;
esac

if [[ "$BUILD" -eq 1 ]]; then
	echo ">> Building $BIN ..."
	if ! go build -o "$BIN" ./cmd/blockcraft; then
		echo "Build failed." >&2
		exit 1
	fi
fi

if [[ ! -x "$BIN" && ! -x "$BIN.exe" ]]; then
	echo "Binary '$BIN' not found — run without --no-build first." >&2
	exit 1
fi

echo ">> Running $BIN ..."
exec ./"$BIN"
