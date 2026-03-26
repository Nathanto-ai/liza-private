# Liza V&V Script (PowerShell)
# Runs the complete Verification & Validation suite for the improvements implementation.
# Usage: .\scripts\vnv.ps1

$ErrorActionPreference = "Continue"
$failures = @()

Write-Host "=== Liza V&V Suite ===" -ForegroundColor Cyan
Write-Host ""

# 1. Sync embedded files
Write-Host "[1/7] Syncing embedded files..." -ForegroundColor Yellow
Copy-Item contracts/*.md internal/embedded/contracts/ -Force
Copy-Item skills/*/*.md internal/embedded/skills/ -Force
Copy-Item claude-settings.json internal/embedded/ -Force
Copy-Item mcp.json internal/embedded/ -Force
Write-Host "  OK" -ForegroundColor Green

# 2. Build verification
Write-Host "[2/7] Building all packages..." -ForegroundColor Yellow
$buildOutput = go build ./... 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "  FAIL: build errors" -ForegroundColor Red
    $failures += "build"
    $buildOutput | Write-Host
} else {
    Write-Host "  OK" -ForegroundColor Green
}

# 3. Unit tests (new packages)
Write-Host "[3/7] Running unit tests (new packages)..." -ForegroundColor Yellow
$unitPackages = @(
    "./internal/specvalidate/..."
    "./internal/statevalidate/..."
    "./internal/verify/..."
    "./internal/auditor/..."
    "./internal/planner/..."
    "./internal/runtime/..."
    "./internal/observability/..."
)
foreach ($pkg in $unitPackages) {
    $out = go test -count=1 $pkg 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "  FAIL: $pkg" -ForegroundColor Red
        $failures += "unit:$pkg"
        $out | Write-Host
    } else {
        $line = $out | Select-String "^ok" | Select-Object -First 1
        Write-Host "  $line" -ForegroundColor Green
    }
}

# 4. Modified packages (roles, models, cmd)
Write-Host "[4/7] Running modified package tests..." -ForegroundColor Yellow
$modifiedPackages = @(
    "./internal/roles/..."
    "./internal/models/..."
    "./cmd/..."
)
foreach ($pkg in $modifiedPackages) {
    $out = go test -count=1 $pkg 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "  FAIL: $pkg" -ForegroundColor Red
        $failures += "modified:$pkg"
        $out | Write-Host
    } else {
        $line = $out | Select-String "^ok" | Select-Object -First 1
        if ($line) { Write-Host "  $line" -ForegroundColor Green }
        else { Write-Host "  OK: $pkg" -ForegroundColor Green }
    }
}

# 5. Integration tests (new cross-cutting tests)
Write-Host "[5/7] Running cross-cutting integration tests..." -ForegroundColor Yellow
$integrationOut = go test -count=1 -run "TestClosedLoop|TestRunawayPrevention|TestSpecValidation_Integration|TestVerification_Integration|TestQualityGate_AuditFinding" ./internal/integration/... 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "  FAIL: integration tests" -ForegroundColor Red
    $failures += "integration"
    $integrationOut | Write-Host
} else {
    $line = $integrationOut | Select-String "^ok" | Select-Object -First 1
    Write-Host "  $line" -ForegroundColor Green
}

# 6. Artifact verification
Write-Host "[6/7] Verifying V&V artifacts..." -ForegroundColor Yellow
$artifacts = @(
    "specs/vnv-traceability.yaml"
    "templates/spec_delivery.md"
    "docs/VNV_PLAN.md"
    "scripts/vnv.sh"
)
foreach ($a in $artifacts) {
    if (Test-Path $a) {
        Write-Host "  $a exists" -ForegroundColor Green
    } else {
        Write-Host "  MISSING: $a" -ForegroundColor Red
        $failures += "artifact:$a"
    }
}

# 7. Summary
Write-Host ""
Write-Host "=== V&V Summary ===" -ForegroundColor Cyan
Write-Host ""
if ($failures.Count -eq 0) {
    Write-Host "ALL CHECKS PASSED" -ForegroundColor Green
    exit 0
} else {
    Write-Host "FAILURES ($($failures.Count)):" -ForegroundColor Red
    foreach ($f in $failures) {
        Write-Host "  - $f" -ForegroundColor Red
    }
    exit 1
}
