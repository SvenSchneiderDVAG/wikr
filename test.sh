#!/usr/bin/env bash
set -euo pipefail

min=${MIN_COVERAGE:-80}

# Colors (fallback to no color if not a TTY)
if [[ -t 1 ]]; then
  RED='\033[31m'; GREEN='\033[32m'; YELLOW='\033[33m'; BLUE='\033[34m'; BOLD='\033[1m'; RESET='\033[0m'
else
  RED=''; GREEN=''; YELLOW=''; BLUE=''; BOLD=''; RESET=''
fi

VERBOSE=false
if [[ ${1:-} == -v || ${1:-} == --verbose ]]; then
  VERBOSE=true
fi

echo -e "${BOLD}${BLUE}==> Running tests...${RESET}" >&2

# Portable timestamp in milliseconds (macOS BSD date lacks %N). Prefer Python if available.
get_now_ms() {
  if command -v python3 >/dev/null 2>&1; then
    python3 - <<'PY'
import time,sys
sys.stdout.write(str(int(time.time()*1000)))
PY
    return 0
  fi
  # Fallback: seconds * 1000
  printf '%s' $(( $(date +%s) * 1000 ))
}

start_ts_ms=$(get_now_ms)

JSON_OUT=$(mktemp)
RAW_OUT=$(mktemp)

set +e
if $VERBOSE; then
  go test -count=1 -cover -coverprofile=coverage.out -covermode=atomic -json ./... | tee "$RAW_OUT" > "$JSON_OUT"
else
  go test -count=1 -cover -coverprofile=coverage.out -covermode=atomic -json ./... > "$JSON_OUT" 2> "$RAW_OUT"
fi
test_exit=${PIPESTATUS[0]}
set -e

end_ts_ms=$(get_now_ms)

# Ensure numeric fallbacks (strip non-digits just in case)
start_ts_ms=${start_ts_ms//[^0-9]/}
end_ts_ms=${end_ts_ms//[^0-9]/}
if [[ -z "$start_ts_ms" || -z "$end_ts_ms" ]]; then
  duration_ms=0
else
  duration_ms=$(( end_ts_ms - start_ts_ms ))
fi

# Coverage extraction
if [[ -f coverage.out ]]; then
  total=$(go tool cover -func=coverage.out | awk '/^total:/ {sub(/%$/, "", $3); print $3}')
else
  total=0
fi

# Compute per-test stats (only lines with a Test field and action pass/fail/skip)
passed_tests=$(grep '"Action":"pass"' "$JSON_OUT" | grep '"Test":' | wc -l | tr -d ' ' || true)
failed_tests=$(grep '"Action":"fail"' "$JSON_OUT" | grep '"Test":' | wc -l | tr -d ' ' || true)
skipped_tests=$(grep '"Action":"skip"' "$JSON_OUT" | grep '"Test":' | wc -l | tr -d ' ' || true)
total_tests=$(( passed_tests + failed_tests + skipped_tests ))

cov_color=$GREEN
if awk -v t="$total" -v m="$min" 'BEGIN{exit !(t < m)}'; then
  cov_color=$RED
elif awk -v t="$total" 'BEGIN{exit !(t < 90)}'; then
  cov_color=$YELLOW
fi

status_color=$GREEN
overall_status="PASS"
if (( failed_tests > 0 )) || (( test_exit != 0 )); then
  status_color=$RED
  overall_status="FAIL"
fi

printf "\n${BOLD}Test Summary${RESET}\n"
printf "  Status:   ${status_color}%s${RESET}\n" "$overall_status"
printf "  Duration: %s ms\n" "$duration_ms"
printf "  Tests:    %s (pass: %s, fail: %s, skip: %s)\n" "$total_tests" "$passed_tests" "$failed_tests" "$skipped_tests"
printf "  Coverage: ${cov_color}%s%%%s (min %s%%)\n" "$total" "$RESET" "$min"

if $VERBOSE; then
  echo -e "\n${BOLD}Verbose Output${RESET}" >&2
  # Filter raw output to show only standard go test lines (RUN/PASS/FAIL/coverage summary)
  grep -E '^(=== RUN|--- PASS|--- FAIL|PASS$|FAIL$|coverage:)' "$RAW_OUT" || true
fi

if awk -v t="$total" -v m="$min" 'BEGIN{exit !(t < m)}'; then
  echo -e "${RED}Coverage below threshold (${total}% < ${min}%).${RESET}" >&2
  exit 1
fi

if (( failed_tests > 0 )) || (( test_exit != 0 )); then
  exit 1
fi

echo -e "${GREEN}All tests passed and coverage threshold met.${RESET}" >&2

rm -f "$JSON_OUT" "$RAW_OUT"
