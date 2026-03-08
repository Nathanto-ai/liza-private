# Control-Plane Improvements — Session 5 Final Report

**Date**: 2025-01-XX
**Branch**: `private-main`
**Test result**: 28/28 packages PASS, zero failures

---

## Summary

Implemented all 8 priority control-plane improvements identified in the external audit documents. All changes are backward-compatible, fully tested, and documented.

## Changes by Priority

### P1: Deterministic Audit Classification ✅

**Problem**: Auditor agents owned classification decisions (LOG_ONLY, REMEDIATE_WITH_TASK, etc.), making the control plane dependent on agent judgment.

**Solution**: Added `ClassifyFinding()` deterministic policy function in `internal/ops/classify_finding.go`. The supervisor applies classification based on finding severity, type, task status, and repeat-finding count — regardless of what the auditor suggests.

**Policy rules** (first match wins):
| Condition | Classification |
|-----------|---------------|
| 3+ unresolved findings on same task | REPLAN_REQUIRED |
| HIGH + SPEC_MISMATCH/SYSTEMIC_SPEC_DRIFT on MERGED task | REOPEN_TASK |
| HIGH + SPEC_MISMATCH/SYSTEMIC_SPEC_DRIFT on non-MERGED | REPLAN_REQUIRED |
| HIGH + other type | REMEDIATE_WITH_TASK |
| MEDIUM + MISSING_TEST/EDGE_CASE/SPEC_MISMATCH/VERIFICATION_GAP/CAPABILITY_MISSING | REMEDIATE_WITH_TASK |
| MEDIUM + ARCHITECTURE_DEBT/SYSTEMIC_SPEC_DRIFT | REPLAN_REQUIRED |
| MEDIUM + QUALITY_ISSUE | LOG_ONLY |
| LOW | LOG_ONLY |

**Finding types expanded** from 4 to 8: added CAPABILITY_MISSING, VERIFICATION_GAP, ARCHITECTURE_DEBT, SYSTEMIC_SPEC_DRIFT.

**Files changed**: classify_finding.go (new), classify_finding_test.go (new), submit_audit_finding.go, mcp/server.go, mcp/handlers.go, cmd/liza/main.go, prompts/templates/auditor_context.tmpl

**Tests**: 12 classifier subtests + 4 submission tests + MCP schema test

---

### P2: Tighten Sprint-Close V&V ✅

**Problem**: `validateSprintVV` checked `VerificationResult == nil` but NOT `VerificationResult.Passed == true`. A task with a failed verification result could pass sprint close.

**Solution**: Added `else if !task.VerificationResult.Passed` check in `sprint_checkpoint.go`.

**Files changed**: sprint_checkpoint.go, sprint_vv_test.go

**Tests**: 7/7 TestValidateSprintVV pass (1 new regression test)

---

### P3: Unify Verification Execution ✅

**Problem**: `wt_merge.go` had a hand-rolled shell loop duplicating `verify/executor.go` logic, with different error handling and no per-command tracking.

**Solution**: Replaced the hand-rolled loop (lines 370-410) with a single `verify.RunVerification()` call. Added `toVerificationResult()` to convert `verify.Result` → `models.VerificationResult` with per-command data.

**Files changed**: wt_merge.go, merge_verify_test.go

**Tests**: All 12 merge tests pass + 2 new conversion tests

---

### P4: Richer VerificationResult Schema ✅

**Problem**: `VerificationResult` only stored `Passed`, `Output`, `Timestamp` — losing per-command exit codes, durations, errors.

**Solution**: Added `Commands []VerificationCmdResult` and `Phase string` fields. `VerificationCmdResult` captures Command, ExitCode, Output, Duration, Error per command. All fields `omitempty` for backward compatibility.

**Files changed**: models/state.go, merge_verify_test.go

**Tests**: YAML round-trip test confirms serialization/deserialization

---

### P5: Deepen Spec Validation ✅

**Problem**: Spec validation only checked required sections and Given/When/Then format. No checks for requirement IDs, AC-to-requirement linkage, verification executability, or blocking open questions.

**Solution**: Added 4 new validation checks for delivery specs:
1. `checkRequirementIDs()` — warns if Requirements section has no R# IDs
2. `checkACRequirementLinkage()` — warns if AC IDs don't reference requirements
3. `checkVerificationCommands()` — warns if Verification Plan has no executable content
4. `checkOpenQuestions()` — warns if Open Questions has blocking items

**Files changed**: specvalidate/validate.go, specvalidate/validate_test.go

**Tests**: 17/17 specvalidate tests (8 new + 9 existing all pass)

---

### P6: Finding Clustering / Remediation ✅

**Problem**: Each audit finding generated its own remediation task, causing task sprawl when multiple findings target the same spec/task.

**Solution**: Added `ClusterProposals()` in `internal/planner/task_policy.go`. Groups proposals by `(SpecRef, OriginTaskID)` and merges clusters into single consolidated tasks with combined descriptions, highest priority, and all finding IDs.

**Files changed**: planner/task_policy.go, planner/task_policy_test.go

**Tests**: 6 cluster tests (empty, single, same-spec clustered, different-spec not clustered, different-task not clustered, mixed)

---

### P7: Acceptance Refs Traceability ✅

**Problem**: Traceability matrix showed Requirement → Tasks → VerifyCommands but skipped AcceptanceCriteria, leaving a gap in the chain.

**Solution**: Added `AcceptanceCriteria []string` to `TraceabilityEntry` and populated it from tasks in `inspectTraceability()`. The full chain is now: Requirement → Tasks → AcceptanceCriteria → VerifyCommands → Coverage.

**Files changed**: commands/traceability.go, commands/traceability_test.go

**Tests**: 9/9 traceability tests (1 new + 8 existing all pass)

---

### P8: Runtime Anomaly/Budget Integration ✅

**Problem**: MEDIUM severity anomalies (no-diff retries) only logged but never triggered corrective action. Agent could loop indefinitely producing no changes.

**Solution**: Added `NoDiffEscalationThreshold` (default: 6) to `AnomalyDetector`. When consecutive no-diff iterations reach the escalation threshold (2x the detection threshold), severity escalates from MEDIUM to HIGH, triggering supervisor shutdown.

**Files changed**: runtime/anomaly_detector.go, runtime/anomaly_detector_test.go

**Tests**: 5 escalation subtests (3→MEDIUM, 5→MEDIUM, 6→HIGH, 7→HIGH, diff-resets-escalation) + 4 consecutive-count tests

---

## Test Results

```
28/28 packages PASS
go test ./... — 0 failures
```

| Package | Tests | Duration |
|---------|-------|----------|
| cmd/liza | PASS | 43s |
| internal/agent | PASS | 77s |
| internal/commands | PASS | 39s |
| internal/ops | PASS | 31s |
| internal/mcp | PASS | 17s |
| internal/planner | PASS | 2s |
| internal/runtime | PASS | 1s |
| internal/specvalidate | PASS | 1s |
| (20 more packages) | ALL PASS | — |

## Documentation Updated

- [docs/CONFIGURATION.md](../CONFIGURATION.md) — Audit findings (8 types, deterministic classification, finding clustering), verification results schema, traceability matrix, anomaly escalation threshold
- [REPOSITORY.md](../../REPOSITORY.md) — Updated package descriptions for specvalidate, planner, runtime

## New Files

| File | Purpose |
|------|---------|
| `internal/ops/classify_finding.go` | Deterministic supervisor classification policy |
| `internal/ops/classify_finding_test.go` | 12 classifier subtests + counter test |

## Architecture Impact

- **Classification ownership**: Moved from auditor → deterministic supervisor policy
- **Verification execution**: Unified under `verify.RunVerification()` (no more dual paths)
- **Finding-to-task pipeline**: Now includes clustering step before dedup
- **Traceability chain**: Complete: Requirement → AcceptanceCriteria → Task → VerifyCommands → VerificationResult
- **Anomaly response**: MEDIUM anomalies now escalate to HIGH after sustained pattern
