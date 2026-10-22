#!/usr/bin/env bash
# tools/e2e.sh checks the migrated Sakana app end to end and packages it.
#
# It builds the app, launches it with the E2E control socket in an isolated
# user-data directory, drives internal/e2e's control API with curl, asserts
# the window, viewport, spring, settings, image and menu bar behavior it can
# observe, captures the pet and settings windows into artifacts/e2e/, then
# packages the app with `go tool mygo build -skip-dmg` and checks the bundle.
#
# It is repeatable and idempotent: every run starts from a fresh temp dir and
# artifact set, kills what it started, and exits nonzero if any check failed.
# The human-readable result is artifacts/e2e/report.md.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$REPO_ROOT"

APP_NAME=Sakana
APP_BUNDLE="$REPO_ROOT/build/darwin-arm64/$APP_NAME.app"
APP_EXE="$APP_BUNDLE/Contents/MacOS/$APP_NAME"
PLIST="$APP_BUNDLE/Contents/Info.plist"
ART="$REPO_ROOT/artifacts/e2e"
LOG="$ART/app.log"
CHECKS="$ART/checks.tsv"
FACTS="$ART/facts.txt"
CURL_LOG="$ART/curl.log"

rm -rf "$ART"
mkdir -p "$ART"
: >"$CHECKS"
: >"$FACTS"
: >"$CURL_LOG"

TMP_BASE=${TMPDIR:-/tmp}
TMP_BASE=${TMP_BASE%/}
RUN_TMP=$(mktemp -d "$TMP_BASE/sakana-e2e.XXXXXX")
case "$RUN_TMP" in
  */sakana-e2e.*) ;;
  *)
    echo "e2e: refusing to use unexpected temp directory $RUN_TMP" >&2
    exit 1
    ;;
esac
UD="$RUN_TMP/userdata"
SOCK="$RUN_TMP/pet.sock"
BIN="$RUN_TMP/sakana-e2e"
mkdir -p "$UD"

APP_PID=""
PASS_N=0
FAIL_N=0

# ---------------------------------------------------------------- helpers --

step() { printf '\n== %s\n' "$*"; }

pass() { # NAME DETAIL
  PASS_N=$((PASS_N + 1))
  printf 'PASS\t%s\t%s\n' "$1" "$2" >>"$CHECKS"
  printf 'PASS  %-36s %s\n' "$1" "$2"
}

fail() { # NAME DETAIL
  FAIL_N=$((FAIL_N + 1))
  printf 'FAIL\t%s\t%s\n' "$1" "$2" >>"$CHECKS"
  printf 'FAIL  %-36s %s\n' "$1" "$2"
}

fact() { # KEY VALUE
  printf '%s=%s\n' "$1" "$2" >>"$FACTS"
}

# check_py NAME FILE [EXTRA...] runs the python code on stdin; it reads the
# JSON at FILE as d and may read sys.argv[2:] as extra arguments. It prints a
# one-line detail and exits 0 to pass, nonzero (with the reason printed) to
# fail.
check_py() {
  local name=$1 file=$2
  shift 2
  local out rc=0
  out=$(python3 - "$file" "$@" 2>&1) || rc=$?
  if ((rc == 0)); then
    out=${out//$'\n'/; }
    pass "$name" "$out"
  else
    # A failed check prints a traceback; its last line is the reason.
    fail "$name" "$(printf '%s\n' "$out" | grep -v '^[[:space:]]*$' | tail -1)"
  fi
}

HTTP_STATUS=000

# http_json METHOD PATH OUTFILE [curl args...] writes the response body to
# OUTFILE and its status to HTTP_STATUS. Errors become "curl-error" rather
# than aborting the run, so they show up as failed checks.
http_json() {
  local method=$1 path=$2 outfile=$3
  shift 3
  local rc=0
  HTTP_STATUS=$(curl -sS --max-time 30 --unix-socket "$SOCK" -X "$method" \
    -o "$outfile" -w '%{http_code}' "$@" "http://localhost$path" 2>>"$CURL_LOG") || rc=$?
  if ((rc != 0)); then
    HTTP_STATUS="curl-error"
  fi
}

# request_ok NAME METHOD PATH OUTFILE [curl args...] records a check that the
# request answered 200.
request_ok() {
  local name=$1
  shift
  local method=$1 path=$2 outfile=$3
  http_json "$@"
  if [[ "$HTTP_STATUS" == "200" ]]; then
    pass "$name" "$method $path -> 200"
    return 0
  fi
  local body="(no body)"
  if [[ -s "$outfile" ]]; then
    body=$(head -c 300 "$outfile" | tr '\n' ' ')
  fi
  fail "$name" "$method $path -> HTTP $HTTP_STATUS: $body"
  return 1
}

# make_test_png PATH WIDTH HEIGHT draws an opaque blob on a transparent
# background: a character image for POST /image.
make_test_png() { # PATH W H
  python3 - "$1" "$2" "$3" <<'PY'
import struct, sys, zlib

path, w, h = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
r = min(w, h) * 0.45
raw = bytearray()
for y in range(h):
    raw.append(0)  # filter: none
    for x in range(w):
        dx, dy = x - w / 2, y - h / 2
        if dx * dx + dy * dy <= r * r:
            raw += bytes((226, 128, 92, 255))
        else:
            raw += bytes((0, 0, 0, 0))


def chunk(kind, data):
    return (struct.pack('>I', len(data)) + kind + data +
            struct.pack('>I', zlib.crc32(kind + data) & 0xffffffff))


png = b'\x89PNG\r\n\x1a\n'
png += chunk(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 6, 0, 0, 0))
png += chunk(b'IDAT', zlib.compress(bytes(raw), 9))
png += chunk(b'IEND', b'')
with open(path, 'wb') as f:
    f.write(png)
print('%s %dx%d %d bytes' % (path, w, h, len(png)))
PY
}

# ---------------------------------------------------------------- report ---

write_report() {
  python3 - "$ART" <<'PY' >"$ART/report.md"
import json
import os
import sys

art = sys.argv[1]


def load(name, default=None):
    try:
        with open(os.path.join(art, name)) as f:
            return json.load(f)
    except Exception:
        return default


def text(name):
    try:
        with open(os.path.join(art, name), errors='replace') as f:
            return f.read()
    except Exception:
        return ''


facts = {}
for line in text('facts.txt').splitlines():
    if '=' in line:
        key, value = line.split('=', 1)
        facts[key] = value

checks = []
for line in text('checks.tsv').splitlines():
    parts = line.split('\t')
    if len(parts) == 3:
        checks.append(parts)

state = load('state.json', {}) or {}
native = load('native.json', {}) or {}
pet = load('pet.json', {}) or {}
native_off = load('native-aot-off.json', {}) or {}
state_off = load('state-aot-off.json', {}) or {}
native_on = load('native-aot-on.json', {}) or {}
state_on = load('state-aot-on.json', {}) or {}
drag_pos = load('drag-pos.json', {}) or {}
drag_neg = load('drag-neg.json', {}) or {}
settle = load('settle.json', {}) or {}
image1 = load('state-image1.json', {}) or {}
image2 = load('state-image2.json', {}) or {}
offsets = load('state-offsets.json', {}) or {}
reload = load('reload.json', {}) or {}
settings_capture = load('settings.json', {}) or {}
settings_store = load('settings-store.json', {}) or {}


def g(obj, *path, default=None):
    cur = obj
    for part in path:
        if not isinstance(cur, dict) or part not in cur:
            return default
        cur = cur[part]
    return cur


def fmt(value):
    if isinstance(value, float):
        return ('%.6g' % value)
    return str(value)


def bbox(bounds):
    if not bounds:
        return 'n/a'
    return '(%s,%s) %sx%s' % (fmt(g(bounds, 'x')), fmt(g(bounds, 'y')),
                             fmt(g(bounds, 'width')), fmt(g(bounds, 'height')))


def work_area(s):
    displays = g(s, 'displays', default=[]) or []
    for d in displays:
        if d.get('primary'):
            return d.get('workArea') or {}
    return (displays[0].get('workArea') if displays else {}) or {}


def physics(s):
    p = g(s, 'physics', default={}) or {}
    return 'r=%s y=%s t=%s w=%s size=%s' % tuple(
        fmt(p.get(k, 0)) for k in ('r', 'y', 't', 'w', 'size'))


passed = sum(1 for c in checks if c[0] == 'PASS')
failed = sum(1 for c in checks if c[0] == 'FAIL')
first_failure = next((c for c in checks if c[0] == 'FAIL'), None)

lines = []
lines.append('# Sakana end-to-end report')
lines.append('')
lines.append('- Generated: %s' % facts.get('finished_at', 'unknown'))
lines.append('- Started: %s' % facts.get('started_at', 'unknown'))
lines.append('- Host: %s' % facts.get('host', 'unknown'))
lines.append('- Go: %s' % facts.get('go_version', 'unknown'))
lines.append('- Mygo: %s' % facts.get('mygo_version', 'unknown'))
lines.append('- App binary: `%s`' % facts.get('app_binary', 'unknown'))
lines.append('- App exit code: %s' % facts.get('quit_exit_code', 'not checked'))
lines.append('- Result: **%s** — %d passed, %d failed' % (
    'ALL CHECKS PASSED' if failed == 0 else 'FAILED', passed, failed))
if first_failure:
    lines.append('- First failing check: **%s** — %s' % (first_failure[1], first_failure[2]))
lines.append('')

lines.append('## Checks')
lines.append('')
lines.append('| # | Check | Result | Detail |')
lines.append('|---|-------|--------|--------|')
for i, (status, name, detail) in enumerate(checks, 1):
    lines.append('| %d | `%s` | %s | %s |' % (
        i, name, status, detail.replace('|', '\\|')))
lines.append('')

lines.append('## Run commands')
lines.append('')
lines.append('```sh')
lines.append(facts.get('cmd_build', 'go build'))
lines.append(facts.get('cmd_launch', 'launch'))
lines.append(facts.get('cmd_package', 'go tool mygo build -skip-dmg'))
lines.append('```')
lines.append('')

lines.append('## Window and viewport')
lines.append('')
b = g(state, 'bounds', default={}) or {}
m = g(state, 'metrics', default={}) or {}
wa = work_area(state)
lines.append('| | value |')
lines.append('|---|---|')
lines.append('| pet window bounds | %s |' % bbox(b))
lines.append('| primary work area | x=%s y=%s %sx%s |' % (
    fmt(wa.get('x')), fmt(wa.get('y')), fmt(wa.get('width')), fmt(wa.get('height'))))
lines.append('| viewport metrics | width=%s height=%s imageSize=%s rodLength=%s baseWidth=%s baseHeight=%s basePad=%s rodVerticalScale=%s maxR=%s maxY=%s |' % tuple(
    fmt(m.get(k)) for k in ('width', 'height', 'imageSize', 'rodLength', 'baseWidth',
                            'baseHeight', 'basePad', 'rodVerticalScale', 'maxR', 'maxY')))
lines.append('| panel | %s |' % g(state, 'panel'))
lines.append('| visible | %s |' % g(state, 'visible'))
lines.append('| alwaysOnTop | %s |' % g(state, 'alwaysOnTop'))
lines.append('| initial spring | %s |' % physics(state))
lines.append('| settings | `%s` |' % json.dumps(g(state, 'settings', default={})))
lines.append('')

lines.append('## Native NSWindow probe')
lines.append('')
lines.append('| field | value |')
lines.append('|---|---|')
for key in ('class', 'styleMask', 'level', 'isOpaque', 'hasShadow', 'hidesOnDeactivate',
            'ignoresMouseEvents', 'isKeyWindow', 'isVisible', 'collectionBehavior',
            'activationPolicy', 'appIsActive', 'nsWindowPtr'):
    lines.append('| %s | `%s` |' % (key, fmt(native.get(key))))
lines.append('')

lines.append('## Pet window capture')
lines.append('')
lines.append('| | value |')
lines.append('|---|---|')
lines.append('| path | `%s` |' % pet.get('path', facts.get('pet_png', 'n/a')))
lines.append('| pixel size | %sx%s |' % (fmt(pet.get('width')), fmt(pet.get('height'))))
lines.append('| bytes | %s |' % fmt(pet.get('bytes')))
lines.append('| alphaZeroRatio | %s |' % fmt(pet.get('alphaZeroRatio')))
lines.append('| cornerAlphas | `%s` |' % json.dumps(pet.get('cornerAlphas')))
lines.append('| centerAlpha | %s |' % fmt(pet.get('centerAlpha')))
lines.append('| opaqueBBox | `%s` |' % json.dumps(pet.get('opaqueBBox')))
lines.append('')

lines.append('## Always-on-top')
lines.append('')
lines.append('| | alwaysOnTop (state) | NSWindow level |')
lines.append('|---|---|---|')
lines.append('| default (fresh settings) | %s | %s |' % (
    g(state, 'alwaysOnTop'), fmt(native.get('level'))))
lines.append('| after /aot?on=1 | %s | %s |' % (
    g(state_on, 'alwaysOnTop'), fmt(native_on.get('level'))))
lines.append('| after /aot?on=0 | %s | %s |' % (
    g(state_off, 'alwaysOnTop'), fmt(native_off.get('level'))))
lines.append('')

lines.append('## Drag and spring')
lines.append('')
dpos = g(drag_pos, 'state', default={}) or {}
dneg = g(drag_neg, 'state', default={}) or {}
lines.append('| | R | Y | maxR | maxY |')
lines.append('|---|---|---|---|---|')
lines.append('| drag +400/+200 | %s | %s | %s | %s |' % (
    fmt(g(dpos, 'physics', 'r')), fmt(g(dpos, 'physics', 'y')),
    fmt(m.get('maxR')), fmt(m.get('maxY'))))
lines.append('| drag -400/-200 | %s | %s | %s | %s |' % (
    fmt(g(dneg, 'physics', 'r')), fmt(g(dneg, 'physics', 'y')),
    fmt(m.get('maxR')), fmt(m.get('maxY'))))
lines.append('| after drag 0/0 | %s | %s | | |' % (
    fmt(g(settle, 'physics', 'r')), fmt(g(settle, 'physics', 'y'))))
lines.append('')
lines.append('Settle time: %ss. Final physics: %s' % (
    facts.get('settle_seconds', 'n/a'), physics(settle)))
lines.append('')

lines.append('## Character image and viewport resize')
lines.append('')
lines.append('| | imageSize | bounds |')
lines.append('|---|---|---|')
lines.append('| before /image | %s | %s |' % (
    fmt(g(state, 'metrics', 'imageSize')), bbox(g(state, 'bounds'))))
lines.append('| after 400×200 PNG | %s | %s |' % (
    fmt(g(image1, 'metrics', 'imageSize')), bbox(g(image1, 'bounds'))))
lines.append('| after 100×100 PNG | %s | %s |' % (
    fmt(g(image2, 'metrics', 'imageSize')), bbox(g(image2, 'bounds'))))
lines.append('')
lines.append('The 400×200 image keeps the 120 DIP image size (the shorter side is capped at 120), '
             'so the window does not resize; the 100×100 image does resize it.')
lines.append('')

lines.append('## Position offsets')
lines.append('')
ob = g(offsets, 'bounds', default={}) or {}
owa = work_area(offsets)
lines.append('| | value |')
lines.append('|---|---|')
lines.append('| requested offsets | x=100 y=140 |')
lines.append('| work area | x=%s y=%s %sx%s |' % (
    fmt(owa.get('x')), fmt(owa.get('y')), fmt(owa.get('width')), fmt(owa.get('height'))))
lines.append('| pet window bounds | %s |' % bbox(ob))
lines.append('| settings after apply | `%s` |' % json.dumps(g(offsets, 'settings', default={})))
lines.append('')

lines.append('## Settings window and persistence')
lines.append('')
lines.append('| | value |')
lines.append('|---|---|')
lines.append('| capture path | `%s` |' % settings_capture.get('path', facts.get('settings_png', 'n/a')))
lines.append('| capture pixel size | %sx%s |' % (
    fmt(settings_capture.get('width')), fmt(settings_capture.get('height'))))
lines.append('| capture bytes | %s |' % fmt(settings_capture.get('bytes')))
lines.append('| capture alphaZeroRatio | %s |' % fmt(settings_capture.get('alphaZeroRatio')))
lines.append('| save/reload same | %s |' % reload.get('same'))
lines.append('| save/reload before | `%s` |' % json.dumps(reload.get('before')))
lines.append('| save/reload after | `%s` |' % json.dumps(reload.get('after')))
lines.append('| settings.json on disk | `%s` |' % json.dumps(settings_store))
lines.append('')

lines.append('## Packaging')
lines.append('')
lines.append('| | value |')
lines.append('|---|---|')
lines.append('| bundle | `%s` |' % facts.get('bundle_path', 'build/darwin-arm64/Sakana.app'))
lines.append('| executable | `%s` |' % facts.get('app_exe', 'Contents/MacOS/Sakana'))
lines.append('| executable arch | %s |' % facts.get('app_arch', 'unknown'))
lines.append('| app size | %s MB |' % facts.get('app_size_mb', 'unknown'))
lines.append('| CFBundleIdentifier | %s |' % facts.get('plist_cf_bundle_identifier', 'unknown'))
lines.append('| LSUIElement | %s |' % facts.get('plist_lsuielement', 'unknown'))
lines.append('| NSHighResolutionCapable | %s |' % facts.get('plist_high_res', 'unknown'))
lines.append('| LSMinimumSystemVersion | %s |' % facts.get('plist_min_system', 'unknown'))
lines.append('| signing | %s |' % facts.get('codesign_identity', 'unknown'))
lines.append('')
lines.append('Codesign details:')
lines.append('')
lines.append('```')
lines.append(text('codesign.txt').strip() or 'n/a')
lines.append('```')
lines.append('')

lines.append('## Artifacts')
lines.append('')
for name in sorted(os.listdir(art)):
    path = os.path.join(art, name)
    # report.md is this file, still empty while it is being written.
    if os.path.isfile(path) and not name.startswith('.') and name != 'report.md':
        lines.append('- `artifacts/e2e/%s` (%d bytes)' % (name, os.path.getsize(path)))
lines.append('')

lines.append('## App log')
lines.append('')
lines.append('```')
log_tail = text('app.log').strip().splitlines()[-30:]
lines.extend(log_tail if log_tail else ['(empty)'])
lines.append('```')
lines.append('')

sys.stdout.write('\n'.join(lines) + '\n')
PY
}

print_table() {
  printf '\n%-4s %-36s %s\n' "" "CHECK" "DETAIL"
  awk -F'\t' '{ printf "%-4s %-36s %s\n", $1, $2, substr($3, 1, 150) }' "$CHECKS"
  printf '\npassed: %d, failed: %d\n' "$PASS_N" "$FAIL_N"
  printf 'report: %s\n' "$ART/report.md"
}

finish() {
  write_report
  print_table
  if ((FAIL_N > 0)); then
    exit 1
  fi
  exit 0
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ -n "$APP_PID" ]] && kill -0 "$APP_PID" 2>/dev/null; then
    kill "$APP_PID" 2>/dev/null || true
    for _ in $(seq 1 50); do
      kill -0 "$APP_PID" 2>/dev/null || break
      sleep 0.1
    done
    if kill -0 "$APP_PID" 2>/dev/null; then
      kill -9 "$APP_PID" 2>/dev/null || true
    fi
  fi
  if pgrep -f "$BIN" >/dev/null 2>&1; then
    pkill -9 -f "$BIN" 2>/dev/null || true
    echo "e2e: killed a leftover app process" >&2
  fi
  rm -f "$SOCK"
  rm -rf "$RUN_TMP"
  exit "$status"
}
trap cleanup EXIT INT TERM

# ------------------------------------------------------------------- run ---

STARTED_AT=$(date '+%Y-%m-%d %H:%M:%S %Z')
fact started_at "$STARTED_AT"
fact host "$(uname -m) $(sw_vers -productName 2>/dev/null || true) $(sw_vers -productVersion 2>/dev/null || true)"
fact go_version "$(go version)"
fact mygo_version "$(go list -m -f '{{.Path}} {{.Version}}' github.com/egoist/mygo 2>/dev/null || echo unknown)"
fact cmd_build "go build -o <tmp>/sakana-e2e ."
fact cmd_launch "SAKANA_E2E_SOCK=<tmp>/pet.sock SAKANA_E2E_USERDATA=<tmp>/userdata SAKANA_E2E_ARTIFACTS=$ART $BIN"
fact cmd_package "go tool mygo build -skip-dmg"

step "build the app"
BUILD_START=$SECONDS
if go build -o "$BIN" . >"$ART/go-build.log" 2>&1; then
  pass "app-build" "go build . ($((SECONDS - BUILD_START))s)"
else
  fail "app-build" "go build failed: $(tail -12 "$ART/go-build.log" | tr '\n' ';')"
  echo "e2e: first failing check is app-build; see $ART/go-build.log" >&2
  tail -20 "$ART/go-build.log" >&2 || true
  finish
fi
fact app_binary "$BIN"

step "launch the app"
SAKANA_E2E_SOCK="$SOCK" SAKANA_E2E_USERDATA="$UD" SAKANA_E2E_ARTIFACTS="$ART" \
  "$BIN" >"$LOG" 2>&1 &
APP_PID=$!
fact app_pid "$APP_PID"
LAUNCH_START=$SECONDS
HEALTHY=0
while ((SECONDS - LAUNCH_START < 15)); do
  if curl -sf --max-time 1 --unix-socket "$SOCK" http://localhost/health >"$ART/health.json" 2>/dev/null; then
    HEALTHY=1
    break
  fi
  if ! kill -0 "$APP_PID" 2>/dev/null; then
    break
  fi
  sleep 0.1
done
if ((HEALTHY == 1)); then
  pass "app-health" "/health answered after $((SECONDS - LAUNCH_START))s (pid $APP_PID)"
else
  fail "app-health" "no /health within 15s (pid $APP_PID); see $LOG"
  echo 'e2e: the app did not come up; its log says (panic|fatal|error):' >&2
  grep -E "panic|fatal|error" "$LOG" | head -20 >&2 || tail -40 "$LOG" >&2
  finish
fi

step "state and default placement"
state_get() { # NAME OUTFILE [PATH]
  local name=$1 outfile=$2 path=${3:-/state}
  request_ok "$name" GET "$path" "$outfile" || true
}
state_get "state" "$ART/state.json"

check_py "state.visible" "$ART/state.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("visible") is True, "pet window is not visible: visible=%r" % (d.get("visible"),)
print("visible=true")
PY

check_py "state.panel" "$ART/state.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("panel") is True, "pet window is not an NSPanel: panel=%r" % (d.get("panel"),)
print("panel=true")
PY

check_py "settings.defaults" "$ART/state.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
s = d["settings"]
# Swift parity: a fresh settings store is not always-on-top and sits 64 DIPs
# from the bottom-right corner of the primary display's work area.
want = {"alwaysOnTop": False, "screenLabel": "", "offsetX": 64, "offsetY": 64, "imagePath": ""}
for key, value in want.items():
    assert s.get(key) == value, "settings.%s=%r, want %r" % (key, s.get(key), value)
print("alwaysOnTop=false offsets=64,64 screenLabel='' imagePath=''")
PY

check_py "bounds.matchMetrics" "$ART/state.json" <<'PY'
import json, math, sys
d = json.load(open(sys.argv[1]))
b, m = d["bounds"], d["metrics"]
w, h = int(math.floor(m["width"] + 0.5)), int(math.floor(m["height"] + 0.5))
assert b["width"] == w and b["height"] == h, \
    "window %dx%d != round(metrics) %dx%d" % (b["width"], b["height"], w, h)
print("window %dx%d == round(metrics width %s, height %s)" % (
    b["width"], b["height"], m["width"], m["height"]))
PY

check_py "metrics.formula" "$ART/state.json" <<'PY'
import json, math, sys
d = json.load(open(sys.argv[1]))
m = d["metrics"]
size = m["imageSize"]
max_r = min(max(size / 5, 30), 60)
max_y = size / 4
rod = size * 0.5625
rad = math.radians(max_r)
half_w = math.sin(rad) * rod + (size / 2) * math.cos(rad) + size * math.sin(rad)
want = {
    "width": 2 * half_w + 20,
    "height": 4 + 6 + rod + max_y * 0.6 + size + 20,
    "imageSize": size,
    "rodLength": rod,
    "baseWidth": size * 0.625,
    "baseHeight": 6,
    "basePad": 4,
    "rodVerticalScale": 0.6,
    "maxR": max_r,
    "maxY": max_y,
}
for key, value in want.items():
    got = m[key]
    assert abs(got - value) < 1e-6, "metrics.%s=%r, want %r" % (key, got, value)
print("metrics match calculateViewport(imageSize=%s): %gx%g, maxR=%g, maxY=%g" % (
    size, m["width"], m["height"], max_r, max_y))
PY

check_py "bounds.defaultPlacement" "$ART/state.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
b = d["bounds"]
displays = d.get("displays") or []
primary = next((x for x in displays if x.get("primary")), None)
assert primary, "state.displays has no primary display"
wa = primary["workArea"]
ox, oy = d["settings"]["offsetX"], d["settings"]["offsetY"]
want_x = wa["x"] + wa["width"] - b["width"] - ox
want_y = wa["y"] + wa["height"] - b["height"] - oy
assert b["x"] == want_x and b["y"] == want_y, \
    "bounds (%r,%r) != bottom-right (%r,%r) of work area %r with offsets %r,%r" % (
        b["x"], b["y"], want_x, want_y, wa, ox, oy)
print("bounds (%d,%d) == work area bottom-right %dx%d minus offsets %g,%g" % (
    b["x"], b["y"], wa["width"], wa["height"], ox, oy))
PY

step "native window"
request_ok "native" GET /native "$ART/native.json" || true

check_py "native.class" "$ART/native.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("class") == "MyGoPanel", "class=%r, want MyGoPanel" % (d.get("class"),)
print("class=MyGoPanel")
PY

check_py "native.isOpaque" "$ART/native.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("isOpaque") is False, "isOpaque=%r, want false (transparent window)" % (d.get("isOpaque"),)
print("isOpaque=false")
PY

check_py "native.level" "$ART/native.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("level") == 0, \
    "level=%r, want 0 (normal) with the default always-on-top off" % (d.get("level"),)
print("level=0 (NSNormalWindowLevel) with always-on-top off by default")
PY

check_py "native.collectionBehavior" "$ART/native.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
cb = d.get("collectionBehavior")
# canJoinAllSpaces | transient | ignoresCycle | fullScreenAuxiliary = 329
want = (1 << 0) | (1 << 3) | (1 << 6) | (1 << 8)
assert isinstance(cb, int) and cb & want == want, \
    "collectionBehavior=%r does not have canJoinAllSpaces|transient|ignoresCycle|fullScreenAuxiliary (%d)" % (cb, want)
print("collectionBehavior=%d has canJoinAllSpaces|transient|ignoresCycle|fullScreenAuxiliary (%d)" % (cb, want))
PY

check_py "native.styleMask" "$ART/native.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
style = d.get("styleMask")
nonactivating = 1 << 7
assert isinstance(style, int) and style & nonactivating, \
    "styleMask=%r has no NonactivatingPanel bit (%d)" % (style, nonactivating)
print("styleMask=%d has NonactivatingPanel (1<<7); frameless full-size content view" % style)
PY

check_py "native.panelChrome" "$ART/native.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("hasShadow") is False, "hasShadow=%r, want false" % (d.get("hasShadow"),)
assert d.get("hidesOnDeactivate") is False, "hidesOnDeactivate=%r, want false" % (
    d.get("hidesOnDeactivate"),)
assert d.get("ignoresMouseEvents") is False, "ignoresMouseEvents=%r, want false" % (
    d.get("ignoresMouseEvents"),)
assert d.get("isVisible") is True, "isVisible=%r, want true" % (d.get("isVisible"),)
print("hasShadow=false hidesOnDeactivate=false ignoresMouseEvents=false isVisible=true")
PY

check_py "native.activationPolicy" "$ART/native.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("activationPolicy") == 1, \
    "activationPolicy=%r, want 1 (NSApplicationActivationPolicyAccessory)" % (d.get("activationPolicy"),)
print("activationPolicy=1 (accessory, no Dock icon)")
PY

step "capture the pet window"
PET_RETRIES=0
while ((PET_RETRIES < 50)); do
  http_json GET "/capture?name=pet" "$ART/pet.json"
  [[ "$HTTP_STATUS" == "200" ]] && break
  PET_RETRIES=$((PET_RETRIES + 1))
  sleep 0.2
done
if [[ "$HTTP_STATUS" == "200" ]]; then
  pass "capture.pet" "GET /capture?name=pet -> $(cat "$ART/pet.json" | tr -d '\n ' | head -c 200)"
else
  fail "capture.pet" "no pet capture after 10s: HTTP $HTTP_STATUS $(head -c 200 "$ART/pet.json")"
fi
fact pet_png "$ART/pet.png"

check_py "capture.pet.transparent" "$ART/pet.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
ratio = d.get("alphaZeroRatio")
assert ratio is not None and ratio > 0.5, \
    "alphaZeroRatio=%r, want > 0.5 (transparent window around the character)" % (ratio,)
assert d.get("bytes", 0) > 0 and d.get("width", 0) > 0 and d.get("height", 0) > 0, \
    "capture is empty: %r" % (d,)
print("alphaZeroRatio=%.3f, %dx%d px, %d bytes" % (
    ratio, d["width"], d["height"], d["bytes"]))
PY

check_py "capture.pet.corners" "$ART/pet.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
corners = d.get("cornerAlphas") or {}
assert set(corners) == {"topLeft", "topRight", "bottomLeft", "bottomRight"}, \
    "cornerAlphas=%r" % (corners,)
for name, alpha in corners.items():
    assert alpha == 0, "corner %s alpha=%r, want 0" % (name, alpha)
print("all four corner alphas are 0")
PY

check_py "capture.pet.opaque" "$ART/pet.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
# The character's pose depends on where the spring is in its kick, so a
# fixed pixel may fall between the character and the rod; check the ink
# the capture holds instead.
bbox = d.get("opaqueBBox") or {}
assert bbox.get("width", 0) >= 100, "opaque bbox too narrow: %r" % (bbox,)
assert bbox.get("height", 0) >= 200, "opaque bbox too short: %r" % (bbox,)
assert bbox["width"] < d["width"] and bbox["height"] < d["height"], \
    "the whole capture is opaque; the window is not transparent: %r" % (bbox,)
ratio = d.get("alphaZeroRatio")
assert ratio is not None and 0.5 < ratio < 0.99, "alphaZeroRatio=%r" % (ratio,)
print("opaque pixels bbox=%rx%d at (%d,%d), alphaZeroRatio=%.3f" % (
    bbox["width"], bbox["height"], bbox["x"], bbox["y"], ratio))
PY

check_py "capture.pet.bbox" "$ART/pet.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
b = d["opaqueBBox"]
assert 0 <= b["x"] and 0 <= b["y"], "bbox starts outside the image: %r" % (b,)
assert b["x"] + b["width"] <= d["width"], "bbox exceeds width: %r vs %d" % (b, d["width"])
assert b["y"] + b["height"] <= d["height"], "bbox exceeds height: %r vs %d" % (b, d["height"])
assert b["width"] < d["width"] and b["height"] < d["height"], \
    "the whole capture is opaque; the window is not transparent: %r" % (b,)
print("opaque bbox %rx%d at (%d,%d) inside %dx%d capture" % (
    b["width"], b["height"], b["x"], b["y"], d["width"], d["height"]))
PY

step "always-on-top toggle"
request_ok "aot.on" POST "/aot?on=1" "$ART/aot-on.json" || true
state_get "state.aot.on" "$ART/state-aot-on.json"
request_ok "native.aot.on" GET /native "$ART/native-aot-on.json" || true
request_ok "aot.off" POST "/aot?on=0" "$ART/aot-off.json" || true
state_get "state.aot.off" "$ART/state-aot-off.json"
request_ok "native.aot.off" GET /native "$ART/native-aot-off.json" || true

check_py "aot.on.on" "$ART/state-aot-on.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("alwaysOnTop") is True, "alwaysOnTop=%r after /aot?on=1" % (d.get("alwaysOnTop"),)
assert d["settings"].get("alwaysOnTop") is True, \
    "settings.alwaysOnTop=%r after /aot?on=1" % (d["settings"].get("alwaysOnTop"),)
print("alwaysOnTop=true in state and settings")
PY

check_py "aot.on.level" "$ART/native-aot-on.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("level") == 3, "NSWindow level=%r after /aot?on=1, want 3 (floating)" % (d.get("level"),)
print("level=3 (NSFloatingWindowLevel)")
PY

check_py "aot.off.off" "$ART/state-aot-off.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("alwaysOnTop") is False, "alwaysOnTop=%r after /aot?on=0" % (d.get("alwaysOnTop"),)
assert d["settings"].get("alwaysOnTop") is False, \
    "settings.alwaysOnTop=%r after /aot?on=0" % (d["settings"].get("alwaysOnTop"),)
print("alwaysOnTop=false in state and settings")
PY

check_py "aot.off.level" "$ART/native-aot-off.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("level") == 0, "NSWindow level=%r after /aot?on=0, want 0 (normal)" % (d.get("level"),)
print("level=0 (NSNormalWindowLevel)")
PY

step "drag the character"
request_ok "drag.positive" POST "/drag?dx=400&dy=200" "$ART/drag-pos.json" || true
request_ok "drag.negative" POST "/drag?dx=-400&dy=-200" "$ART/drag-neg.json" || true

check_py "drag.positive.clamp" "$ART/drag-pos.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
p = d["state"]["physics"]
m = d["state"]["metrics"]
max_r, max_y = m["maxR"], m["maxY"]
assert abs(p["r"] - max_r) <= max_r * 0.1, \
    "physics.r=%r after drag dx=400, want +maxR=%r" % (p["r"], max_r)
assert abs(p["y"] - max_y) <= max_y * 0.1, \
    "physics.y=%r after drag dy=200, want +maxY=%r" % (p["y"], max_y)
print("r=%g (+maxR %g), y=%g (+maxY %g)" % (p["r"], max_r, p["y"], max_y))
PY

check_py "drag.negative.clamp" "$ART/drag-neg.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
p = d["state"]["physics"]
m = d["state"]["metrics"]
max_r, max_y = m["maxR"], m["maxY"]
assert abs(p["r"] + max_r) <= max_r * 0.1, \
    "physics.r=%r after drag dx=-400, want -maxR=%r" % (p["r"], -max_r)
assert abs(p["y"] + max_y) <= max_y * 0.1, \
    "physics.y=%r after drag dy=-200, want -maxY=%r" % (p["y"], -max_y)
print("r=%g (-maxR %g), y=%g (-maxY %g)" % (p["r"], -max_r, p["y"], -max_y))
PY

request_ok "drag.release" POST "/drag?dx=0&dy=0" "$ART/drag-release.json" || true
SETTLE_START=$SECONDS
RESTING=0
while ((SECONDS - SETTLE_START < 5)); do
  http_json GET /state "$ART/settle.json"
  if [[ "$HTTP_STATUS" == "200" ]]; then
    if python3 - "$ART/settle.json" <<'PY'
import json, sys
p = json.load(open(sys.argv[1]))["physics"]
sys.exit(0 if max(abs(p[k]) for k in ("r", "y", "t", "w")) < 0.1 else 1)
PY
    then
      RESTING=1
      break
    fi
  fi
  sleep 0.1
done
SETTLE_SECONDS=$((SECONDS - SETTLE_START))
fact settle_seconds "$SETTLE_SECONDS"
if ((RESTING == 1)); then
  pass "drag.settle" "spring at rest after ${SETTLE_SECONDS}s"
else
  fail "drag.settle" "spring still moving ${SETTLE_SECONDS}s after drag 0/0: $(cat "$ART/settle.json" | tr -d '\n ' | head -c 200)"
fi

check_py "drag.settle.resting" "$ART/settle.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
p = d["physics"]
worst = max(abs(p[k]) for k in ("r", "y", "t", "w"))
assert worst < 0.1, "spring not at rest: %r" % (p,)
print("|r|,|y|,|t|,|w| all below 0.1: %s" % ", ".join(
    "%s=%g" % (k, p[k]) for k in ("r", "y", "t", "w")))
PY

step "apply a generated character image"
make_test_png "$ART/test-400x200.png" 400 200
make_test_png "$ART/test-100x100.png" 100 100
request_ok "image.400x200" POST /image "$ART/image-400.json" \
  -H 'Content-Type: image/png' --data-binary @"$ART/test-400x200.png" || true
state_get "state.image.400x200" "$ART/state-image1.json"

check_py "image.400x200.metrics" "$ART/state-image1.json" "$ART/state.json" <<'PY'
import json, math, sys
after = json.load(open(sys.argv[1]))
before = json.load(open(sys.argv[2]))
size = after["metrics"]["imageSize"]
assert size == 120, "imageSize=%r after a 400x200 image, want 120 (shorter side capped)" % (size,)
b, m = after["bounds"], after["metrics"]
w, h = int(math.floor(m["width"] + 0.5)), int(math.floor(m["height"] + 0.5))
assert b["width"] == w and b["height"] == h, \
    "window %dx%d != round(metrics) %dx%d after the image" % (b["width"], b["height"], w, h)
ob, nb = before["bounds"], after["bounds"]
assert nb["x"] == ob["x"] + ob["width"] // 2 - nb["width"] // 2, \
    "horizontal center moved: %r -> %r" % (ob, nb)
assert nb["y"] + nb["height"] == ob["y"] + ob["height"], \
    "bottom edge moved: %r -> %r" % (ob, nb)
print("imageSize=120, window %dx%d, bottom edge and horizontal center kept" % (w, h))
PY

request_ok "image.100x100" POST /image "$ART/image-100.json" \
  -H 'Content-Type: image/png' --data-binary @"$ART/test-100x100.png" || true
state_get "state.image.100x100" "$ART/state-image2.json"

check_py "image.100x100.resize" "$ART/state-image2.json" "$ART/state-image1.json" <<'PY'
import json, math, sys
after = json.load(open(sys.argv[1]))
before = json.load(open(sys.argv[2]))
b, m = after["bounds"], after["metrics"]
assert m["imageSize"] == 100, "imageSize=%r after a 100x100 image, want 100" % (m["imageSize"],)
w, h = int(math.floor(m["width"] + 0.5)), int(math.floor(m["height"] + 0.5))
assert (b["width"], b["height"]) == (w, h), \
    "window %dx%d != round(metrics) %dx%d after the resize" % (b["width"], b["height"], w, h)
ob = before["bounds"]
assert (b["width"], b["height"]) != (ob["width"], ob["height"]), \
    "window did not resize: %r -> %r" % (ob, b)
assert b["x"] == ob["x"] + ob["width"] // 2 - b["width"] // 2, \
    "horizontal center moved: %r -> %r" % (ob, b)
assert b["y"] + b["height"] == ob["y"] + ob["height"], \
    "bottom edge moved: %r -> %r" % (ob, b)
print("window resized %dx%d -> %dx%d; bottom edge and horizontal center kept" % (
    ob["width"], ob["height"], b["width"], b["height"]))
PY

step "position offsets"
request_ok "offsets.apply" POST "/offsets?x=100&y=140" "$ART/offsets.json" || true
state_get "state.offsets" "$ART/state-offsets.json"

check_py "offsets.position" "$ART/state-offsets.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
b = d["bounds"]
displays = d.get("displays") or []
primary = next((x for x in displays if x.get("primary")), None)
assert primary, "state.displays has no primary display"
wa = primary["workArea"]
want_x = wa["x"] + wa["width"] - b["width"] - 100
want_y = wa["y"] + wa["height"] - b["height"] - 140
assert b["x"] == want_x and b["y"] == want_y, \
    "bounds (%r,%r) != work area bottom-right minus offsets 100,140 (%r,%r)" % (
        b["x"], b["y"], want_x, want_y)
s = d["settings"]
assert s["offsetX"] == 100 and s["offsetY"] == 140, "settings offsets %r,%r" % (
    s["offsetX"], s["offsetY"])
print("bounds (%d,%d) == work area bottom-right %dx%d minus offsets 100,140" % (
    b["x"], b["y"], wa["width"], wa["height"]))
PY

step "settings save and reload"
request_ok "settings.reload" POST /settings/reload "$ART/reload.json" || true

check_py "settings.reload.same" "$ART/reload.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("same") is True, "settings did not round-trip: before=%r after=%r" % (
    d.get("before"), d.get("after"))
print("before == after: %r" % (d["after"],))
PY

check_py "settings.reload.state" "$ART/reload.json" "$ART/state-offsets.json" <<'PY'
import json, sys
reload = json.load(open(sys.argv[1]))
state = json.load(open(sys.argv[2]))
assert reload["after"] == state["settings"], \
    "reloaded settings %r != state settings %r" % (reload["after"], state["settings"])
print("reloaded settings match GET /state")
PY

# Keep a copy of the settings file to check the app wrote it to the
# isolated user-data directory.
cp "$UD/settings.json" "$ART/settings-store.json"

check_py "settings.file" "$ART/settings-store.json" "$ART/reload.json" <<'PY'
import json, sys
store = json.load(open(sys.argv[1]))
reload = json.load(open(sys.argv[2]))
for key in ("alwaysOnTop", "screenLabel", "offsetX", "offsetY", "imagePath"):
    assert store.get(key) == reload["after"].get(key), \
        "settings.json %s=%r != reload %r" % (key, store.get(key), reload["after"].get(key))
assert store["offsetX"] == 100 and store["offsetY"] == 140, \
    "settings.json offsets %r,%r, want 100,140" % (store["offsetX"], store["offsetY"])
print("settings.json in the isolated user-data dir matches: %s" % json.dumps(store))
PY

step "open and capture the settings window"
request_ok "settings.open" POST /settings/open "$ART/settings-open.json" || true
SETTINGS_RETRIES=0
while ((SETTINGS_RETRIES < 50)); do
  http_json GET "/settings/capture?name=settings" "$ART/settings.json"
  [[ "$HTTP_STATUS" == "200" ]] && break
  SETTINGS_RETRIES=$((SETTINGS_RETRIES + 1))
  sleep 0.2
done
if [[ "$HTTP_STATUS" == "200" ]]; then
  pass "settings.capture" "GET /settings/capture -> $(cat "$ART/settings.json" | tr -d '\n ' | head -c 200)"
else
  fail "settings.capture" "no settings capture after 10s: HTTP $HTTP_STATUS $(head -c 200 "$ART/settings.json")"
fi
fact settings_png "$ART/settings.png"

check_py "settings.capture.opaque" "$ART/settings.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("bytes", 0) > 20 * 1024, \
    "settings capture is %r bytes, want > 20 KB" % (d.get("bytes"),)
ratio = d.get("alphaZeroRatio")
assert ratio is not None and ratio < 0.2, \
    "alphaZeroRatio=%r, want < 0.2 (mostly opaque settings UI)" % (ratio,)
print("%d bytes, alphaZeroRatio=%.3f, %dx%d px" % (
    d["bytes"], ratio, d["width"], d["height"]))
PY

step "quit the app"
request_ok "app.quit" POST /quit "$ART/quit.json" || true
QUIT_EXIT="not checked"
for _ in $(seq 1 100); do
  if ! kill -0 "$APP_PID" 2>/dev/null; then
    break
  fi
  sleep 0.1
done
if kill -0 "$APP_PID" 2>/dev/null; then
  fail "app.exit" "app still running 10s after POST /quit"
  QUIT_EXIT="still running"
else
  set +e
  wait "$APP_PID" 2>/dev/null
  QUIT_CODE=$?
  set -e
  QUIT_EXIT=$QUIT_CODE
  APP_PID=""
  if ((QUIT_CODE == 0)); then
    pass "app.exit" "process exited with code 0"
  else
    fail "app.exit" "process exited with code $QUIT_CODE"
  fi
fi
fact quit_exit_code "$QUIT_EXIT"

SOCKET_GONE=0
for _ in $(seq 1 50); do
  if [[ ! -e "$SOCK" ]]; then
    SOCKET_GONE=1
    break
  fi
  sleep 0.1
done
if ((SOCKET_GONE == 1)); then
  pass "app.socket.removed" "control socket removed on quit"
else
  fail "app.socket.removed" "control socket still at $SOCK after quit"
fi

if pgrep -f "$BIN" >/dev/null 2>&1; then
  fail "app.noLeftovers" "a process matching $BIN is still running"
else
  pass "app.noLeftovers" "no process left behind"
fi

step "package the app"
rm -rf "$APP_BUNDLE"
PACKAGE_START=$SECONDS
if go tool mygo build -skip-dmg >"$ART/mygo-build.log" 2>&1; then
  pass "pkg.build" "go tool mygo build -skip-dmg ($((SECONDS - PACKAGE_START))s)"
else
  fail "pkg.build" "go tool mygo build -skip-dmg failed: $(tail -12 "$ART/mygo-build.log" | tr '\n' ';')"
fi

if [[ -x "$APP_EXE" ]]; then
  pass "pkg.executable" "$APP_EXE is executable"
else
  fail "pkg.executable" "missing $APP_EXE"
fi
fact bundle_path "build/darwin-arm64/$APP_NAME.app"
fact app_exe "build/darwin-arm64/$APP_NAME.app/Contents/MacOS/$APP_NAME"

if [[ -x "$APP_EXE" ]]; then
  APP_ARCH=$(file -b "$APP_EXE" | head -c 200)
else
  APP_ARCH="(missing)"
fi
fact app_arch "$APP_ARCH"

if [[ -d "$APP_BUNDLE" ]]; then
  APP_SIZE_KB=$(du -sk "$APP_BUNDLE" | awk '{print $1}')
  APP_SIZE_MB=$(((APP_SIZE_KB + 512) / 1024))
  fact app_size_mb "$APP_SIZE_MB"
  if ((APP_SIZE_MB >= 2 && APP_SIZE_MB <= 300)); then
    pass "pkg.size" "app bundle is $APP_SIZE_MB MB ($APP_SIZE_KB KB)"
  else
    fail "pkg.size" "app bundle is $APP_SIZE_MB MB ($APP_SIZE_KB KB), want 2..300 MB"
  fi
else
  fail "pkg.size" "no app bundle at $APP_BUNDLE"
fi

if [[ -f "$PLIST" ]]; then
  plutil -p "$PLIST" >"$ART/Info.plist.txt" 2>&1 || true
  plist_string() { # KEY
    plutil -extract "$1" raw -o - "$PLIST" 2>/dev/null || echo "(missing)"
  }
  PLIST_ID=$(plist_string CFBundleIdentifier)
  PLIST_UI=$(plist_string LSUIElement)
  PLIST_HR=$(plist_string NSHighResolutionCapable)
  PLIST_MIN=$(plist_string LSMinimumSystemVersion)
  fact plist_cf_bundle_identifier "$PLIST_ID"
  fact plist_lsuielement "$PLIST_UI"
  fact plist_high_res "$PLIST_HR"
  fact plist_min_system "$PLIST_MIN"
  if [[ "$PLIST_ID" == "com.fimars.sakana-desktop" ]]; then
    pass "pkg.plist.identifier" "CFBundleIdentifier=$PLIST_ID"
  else
    fail "pkg.plist.identifier" "CFBundleIdentifier=$PLIST_ID, want com.fimars.sakana-desktop"
  fi
  if [[ "$PLIST_UI" == "true" ]]; then
    pass "pkg.plist.lsuielement" "LSUIElement=true (menu bar app, no Dock icon)"
  else
    fail "pkg.plist.lsuielement" "LSUIElement=$PLIST_UI, want true"
  fi
  if [[ "$PLIST_HR" == "true" ]]; then
    pass "pkg.plist.highres" "NSHighResolutionCapable=true"
  else
    fail "pkg.plist.highres" "NSHighResolutionCapable=$PLIST_HR, want true"
  fi
  if [[ "$PLIST_MIN" == "12.0" ]]; then
    pass "pkg.plist.minsystem" "LSMinimumSystemVersion=12.0"
  else
    fail "pkg.plist.minsystem" "LSMinimumSystemVersion=$PLIST_MIN, want 12.0"
  fi
else
  fail "pkg.plist.identifier" "missing $PLIST"
  fail "pkg.plist.lsuielement" "missing $PLIST"
  fail "pkg.plist.highres" "missing $PLIST"
  fail "pkg.plist.minsystem" "missing $PLIST"
fi

if [[ -d "$APP_BUNDLE" ]]; then
  set +e
  CODESIGN_OUT=$(codesign -dv "$APP_BUNDLE" 2>&1)
  CODESIGN_RC=$?
  set -e
  printf '%s\n' "$CODESIGN_OUT" >"$ART/codesign.txt"
  SIGN_ID=$(printf '%s\n' "$CODESIGN_OUT" | grep -E '^(Identifier|Signature|Authority|TeamIdentifier)=' | tr '\n' ' ')
  fact codesign_identity "${SIGN_ID:-unknown}"
  if printf '%s' "$CODESIGN_OUT" | grep -qi 'adhoc'; then
    pass "pkg.codesign" "ad-hoc signed: $(printf '%s\n' "$CODESIGN_OUT" | grep -i 'Signature=' | head -1)"
  else
    fail "pkg.codesign" "codesign -dv reports no ad-hoc signature: $(printf '%s' "$CODESIGN_OUT" | head -c 300)"
  fi
  set +e
  codesign --verify --deep --strict "$APP_BUNDLE" >"$ART/codesign-verify.log" 2>&1
  VERIFY_RC=$?
  set -e
  if ((VERIFY_RC == 0)); then
    pass "pkg.codesign.verify" "codesign --verify --deep --strict passed"
  else
    fail "pkg.codesign.verify" "codesign --verify failed ($VERIFY_RC): $(head -c 300 "$ART/codesign-verify.log")"
  fi
else
  fail "pkg.codesign" "no app bundle to sign"
  fail "pkg.codesign.verify" "no app bundle to verify"
  fact codesign_identity "unknown"
fi

fact finished_at "$(date '+%Y-%m-%d %H:%M:%S %Z')"
finish
