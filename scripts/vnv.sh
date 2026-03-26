#!/usr/bin/env bash
# Verification & Validation script for the closed-loop improvement system.
# See docs/VNV_PLAN.md for the full V&V plan.
# See scripts/vnv.ps1 for the PowerShell equivalent.

set -euo pipefail

PASS=0
FAIL=0
TOTAL=0

check() {
    local label="$1"
    shift
    TOTAL=$((TOTAL + 1))
    echo -n "  [$TOTAL] $label ... "
    if "$@" > /dev/null 2>&1; then
        echo "PASS"
        PASS=$((PASS + 1))
    else
        echo "FAIL"
        FAIL=$((FAIL + 1))
    fi
}

echo "=== Liza V&V Suite ==="
echo ""

echo "--- Phase 1: Compilation ---"
check "specvalidate compiles"  go build ./internal/specvalidate/...
check "statevalidate compiles" go build ./internal/statevalidate/...
check "verify compiles"        go build ./internal/verify/...
check "auditor compiles"       go build ./internal/auditor/...
check "planner compiles"       go build ./internal/planner/...
check "runtime compiles"       go build ./internal/runtime/...
check "observability compiles" go build ./internal/observability/...
check "commands compiles"      go build ./internal/commands/...
echo ""

echo "--- Phase 2: Unit Tests ---"
check "specvalidate tests"  go test -count=1 ./internal/specvalidate/...
check "statevalidate tests" go test -count=1 ./internal/statevalidate/...
check "verify tests"        go test -count=1 ./internal/verify/...
check "auditor tests"       go test -count=1 ./internal/auditor/...
check "planner tests"       go test -count=1 ./internal/planner/...
check "runtime tests"       go test -count=1 ./internal/runtime/...
check "observability tests" go test -count=1 ./internal/observability/...
echo ""

echo "--- Phase 3: Integration Tests ---"
check "closed-loop integration" go test -count=1 ./internal/integration/...
echo ""

echo "--- Phase 4: Traceability ---"
check "vnv-traceability.yaml exists" test -f specs/vnv-traceability.yaml
check "spec_delivery template exists" test -f templates/spec_delivery.md
check "VNV_PLAN.md exists" test -f docs/VNV_PLAN.md
echo ""

echo "=== Results ==="
echo "  Total: $TOTAL  Pass: $PASS  Fail: $FAIL"
echo ""
if [ "$FAIL" -eq 0 ]; then
    echo "ALL CHECKS PASSED"
    exit 0
else
    echo "SOME CHECKS FAILED"
    exit 1
fi
