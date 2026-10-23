#!/usr/bin/env bash
# tools/bench.sh benchmarks the packaged Sakana app: how long it takes to
# show the pet, what a frame costs, and what the app uses while it rests.
#
# It builds the app with `go tool mygo build -skip-dmg`, then drives the
# running app over the e2e control socket like tools/e2e.sh does. Every
# number lands in artifacts/bench/report.md, in integer milliseconds, and
# the raw values in artifacts/bench/bench.json.
#
# The benchmark is not the test: tools/e2e.sh checks that the app is right,
# this only asks how fast it is. Close other work before a run you want to
# compare: startup is sensitive to a busy machine.
set -euo pipefail

# The launch sway is a Core Animation overlay built with cgo. Mygo's builder
# disables cgo by default, so keep it on for every build in this harness.
export CGO_ENABLED=1

# A sleeping display occludes the pet's window, and Mygo holds its frames
# while nothing shows it: the greeting sway would freeze mid-swing and the
# benchmark would measure a stopped app. Re-exec once under caffeinate so the
# whole run keeps the display awake and the numbers repeatable.
if [[ -z "${SAKANA_BENCH_AWAKE:-}" ]] && command -v caffeinate >/dev/null 2>&1; then
  SAKANA_BENCH_AWAKE=1 exec caffeinate -dimsu "$0" "$@"
fi

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$REPO_ROOT"

APP_NAME=Sakana
APP_BUNDLE="$REPO_ROOT/build/darwin-arm64/$APP_NAME.app"
APP_EXE="$APP_BUNDLE/Contents/MacOS/$APP_NAME"
ART="$REPO_ROOT/artifacts/bench"

if ! command -v python3 >/dev/null 2>&1; then
  echo "bench: python3 is required" >&2
  exit 1
fi

rm -rf "$ART"
mkdir -p "$ART"

echo "== build the app"
BUILD_START=$SECONDS
if ! go tool mygo build -skip-dmg >"$ART/mygo-build.log" 2>&1; then
  echo "bench: go tool mygo build failed; see $ART/mygo-build.log" >&2
  tail -20 "$ART/mygo-build.log" >&2 || true
  exit 1
fi
if [[ ! -x "$APP_EXE" ]]; then
  echo "bench: the build produced no app at $APP_EXE" >&2
  exit 1
fi
echo "== built $APP_BUNDLE in $((SECONDS - BUILD_START))s"

status=0
python3 "$REPO_ROOT/tools/bench.py" \
  --exe "$APP_EXE" \
  --bundle "$APP_BUNDLE" \
  --out "$ART" \
  "$@" || status=$?

if ((status == 0)); then
  echo
  echo "bench: report at $ART/report.md"
else
  echo
  echo "bench: the app missed a benchmark invariant; report at $ART/report.md" >&2
fi
exit "$status"
