#!/usr/bin/env bash
# tools/verify-reminder.sh model-checks the rest reminder's scheduler.
#
# It runs the exhaustive interleaving check in
# internal/reminder/model_check_test.go (build tag sakana_verify), which
# drives the real Scheduler rather than a copy. The properties, the coverage
# record and how to update the check are in
# .grok/skills/verify-sakana-reminder/SKILL.md.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$REPO_ROOT"

if ! command -v go >/dev/null 2>&1; then
  echo "verify-reminder: go is not on PATH" >&2
  exit 1
fi

status=0
out=$(go test -tags sakana_verify ./internal/reminder \
  -run '^TestSchedulerModelCheck$' -count=1 -v 2>&1) || status=$?
printf '%s\n' "$out"

if ((status != 0)); then
  exit "$status"
fi

# A missing build tag or a renamed test would otherwise exit 0 with no test.
if ! grep -q -- '--- PASS: TestSchedulerModelCheck' <<<"$out"; then
  echo "verify-reminder: TestSchedulerModelCheck did not run; check the sakana_verify build tag and the test name" >&2
  exit 1
fi
