
# Liza End-to-End Verification & Validation Plan (Post-Changes)

**Purpose:** Define a complete, repeatable **Verification & Validation (V&V)** process to prove that, after implementing the planned changes (spec hardening, auditor integration, deterministic verification, runaway protection, observability, optional coordinator), the **Liza** repository works end-to-end and reliably produces outputs that match the spec.

**Audience:** Maintainers, contributors, and CI owners.

**Status:** Draft (ready to implement as repo evolves)

**Date:** 2026-03-05

---

## 1) Definitions

### Verification vs Validation
- **Verification (Are we building the thing right?)**
  - Deterministic checks that the system behaves according to **requirements** and internal contracts.
  - Examples: unit tests, integration tests, schema validations, state invariants, exit-code handling, budgets enforcement.
- **Validation (Are we building the right thing?)**
  - Evidence that the system meets **user goals** and delivers intended value in real workflows.
  - Examples: scenario-based end-to-end runs on representative repos, traceability from spec → tasks → tests, usability checks.

### PASS / FAIL / NEEDS_HUMAN_DECISION
- **PASS:** All required verification gates are green; all acceptance criteria are satisfied (or explicitly deferred in Out-of-Scope).
- **FAIL:** Any deterministic gate fails; or a required criterion is not satisfied.
- **NEEDS_HUMAN_DECISION:** The system stops safely when spec ambiguity, conflicting requirements, or budget/time caps are reached. This is a *safe stop* (not an error state), but the run is considered **not PASS** until resolved.

---

## 2) V&V Objectives (What we must prove)

### O1 — Spec Discipline
1. Vision + Delivery spec templates exist and are usable.
2. Specs can be validated automatically.
3. Tasks cannot enter IMPLEMENTING unless they include:
   - acceptance criteria
   - verification command(s)
   - explicit inputs/outputs (or referenced interface)
   - spec references

### O2 — Auditor as Advisory (Non-Decider)
1. Auditor produces structured findings.
2. Auditor cannot mark PASS/FAIL or bypass deterministic checks.
3. Auditor findings can *propose* new tasks, but cannot spawn tasks directly.

### O3 — Deterministic Verification Layer Works
1. Supervisor executes `verify.commands` deterministically.
2. PASS is determined strictly from exit codes and required checks.
3. Failure outcomes are reproducible.

### O4 — Closed Loop Works End-to-End
1. Planner generates tasks from spec.
2. Supervisor schedules/leases tasks correctly.
3. Coder implements changes in a worktree.
4. Verifier runs checks.
5. Auditor reviews outcomes.
6. Planner creates bounded follow-up tasks from findings.
7. Loop ends in PASS or safe stop.

### O5 — Runaway Protection Works
1. Budgets limit iterations, tasks generated, runtime, and tokens (where applicable).
2. Duplicate tasks/findings are de-duplicated.
3. No-progress detection triggers a safe stop.
4. The system does not spiral indefinitely even under adversarial conditions.

### O6 — Observability & Debuggability
1. Event logs allow reconstructing “what happened and why”.
2. All major state transitions emit structured events.
3. Failures produce actionable summaries with pointers to evidence.

### O7 — Optional Coordinator (If Implemented)
1. Supervisor works in standalone mode without coordinator.
2. With coordinator enabled, scheduling is correct and stable.
3. Coordinator enforces global policies (optional gates) without breaking determinism.

---

## 3) V&V Scope

### In Scope
- Spec validation commands and failure modes
- State machine invariants and transitions
- Task lifecycle and leases
- Planner → tasks generation policies (bounded, deduped)
- Auditor role outputs + integration hooks
- Deterministic verifier
- Runaway protection budgets and stop conditions
- Event logging correctness
- Optional coordinator behavior (if included)
- Cross-platform behavior where supported (at minimum: Linux + macOS; Windows if you officially support it)

### Out of Scope (Unless you explicitly choose to include)
- UI/dashboard (if not in repo yet)
- Multi-cloud distributed deployments (beyond local networking)
- Third-party model provider reliability (treated as environment variability)

---

## 4) Test Environments

### E1 — Local Developer Environment
- OS: Ubuntu 22.04+ (primary)
- Python: per repo config (e.g., >=3.12)
- Shell: bash/zsh
- Git: latest stable
- Optional: docker for isolated test harness

### E2 — CI Environment (Required)
- GitHub Actions runner (ubuntu-latest)
- Deterministic seed for tests where applicable
- Cache strategy (pip cache)
- Artifacts upload on failure (logs, state snapshots)

### E3 — “Lab” Environment (Recommended)
- A controlled machine where you run longer scenario tests
- Used for performance + longer end-to-end runs

---

## 5) Evidence Artifacts (What we collect)

Every V&V run must produce (as CI artifacts on failure; optionally on pass):
- `state.yaml` (final)
- `logs/events.jsonl` (or equivalent)
- supervisor logs
- verifier logs (command outputs)
- auditor findings log
- planner decisions log (task creation, dedup decisions)
- per-task prompt snapshots (if stored) **with redaction controls**

---

## 6) Test Strategy Overview

Use 4 layers:

1. **Static & Fast Checks (pre-commit / CI)**
   - lint / formatting
   - type checking
   - schema validation
2. **Unit Tests (fast, deterministic)**
   - small modules: spec validation, budgets, dedup, state transitions
3. **Integration Tests (moderate)**
   - supervisor loop with stubbed executor
   - blackboard concurrency behavior
   - verifier command runner with sandbox commands
4. **End-to-End Scenario Tests (slow, highest confidence)**
   - run Liza on a small “fixture repo” and verify it reaches PASS with correct artifacts
   - run negative scenarios to prove safe stopping

---

## 7) Verification Test Plan (Detailed)

### 7.1 Static & Quality Gates (CI required)
**Goal:** Reject broken code early.

Required gates:
- Lint: `ruff check` (or equivalent)
- Format: `ruff format --check` (or equivalent)
- Type check: `mypy` (if used)
- Unit tests: `pytest -q`

**PASS:** all exit code 0.

---

### 7.2 Spec Validation Tests
**Goal:** Prevent ambiguous or incomplete specs from entering execution.

#### Tests
1. **Valid vision + delivery spec passes**
2. **Missing acceptance criteria fails**
3. **Missing verification plan fails**
4. **Undefined term used in acceptance criteria triggers a warning or fail (your policy)**
5. **Open Questions present triggers NEEDS_HUMAN_DECISION gate (optional policy)**

**Evidence:** `liza validate-spec` output + unit tests around spec validator.

---

### 7.3 Task Quality Gate Tests
**Goal:** Prove tasks can’t enter IMPLEMENTING without hard requirements.

#### Tests
- Attempt transition: TODO → IMPLEMENTING with missing fields (expect reject)
- Attempt transition with all required fields (expect allow)

**Evidence:** unit tests for `statevalidate/task_quality.go` or equivalent.

---

### 7.4 Auditor Integration Tests
**Goal:** Prove auditor is advisory and produces structured findings.

#### Tests
1. Auditor emits finding with:
   - `severity`, `type`, `spec_reference`, `evidence`, `recommended_action`
2. Auditor cannot set PASS/FAIL fields directly.
3. Auditor findings are stored and traceable to tasks.

**Implementation note:** Use a stub auditor in tests to remove LLM variability.

---

### 7.5 Planner Task Creation Policy Tests
**Goal:** Prove findings become tasks only when bounded and deduped.

#### Tests
1. Finding without spec reference → no task created
2. Finding without evidence → no task created
3. Finding too broad → task rejected (policy)
4. Duplicate findings (same hash) → only one task created
5. Task creation count cap enforced

**Evidence:** unit tests for task_policy + dedup module.

---

### 7.6 Deterministic Verification Runner Tests
**Goal:** Prove `verify.commands` is executed reliably and results drive PASS/FAIL.

#### Tests
1. `verify.commands` includes `python -c "exit(0)"` → PASS
2. `verify.commands` includes `python -c "exit(1)"` → FAIL
3. Timeout behavior works (if implemented)
4. Output capture is stored and linked

**Evidence:** integration tests + captured logs.

---

### 7.7 Supervisor Lifecycle & Lease Tests
**Goal:** Prove scheduling is safe under concurrency.

#### Tests
1. Two supervisors compete; only one acquires a lease for a task.
2. Lease expiry returns task to queue correctly.
3. Crash simulation: supervisor dies mid-implement; lease expiry recovers.
4. Atomic blackboard writes remain valid even under concurrent Modify calls.

**Evidence:** integration tests with parallel processes (or threads) + blackboard lock verification.

---

### 7.8 Runaway Protection Tests
**Goal:** Prove system stops safely.

#### Tests
1. **Max iterations per task**: task loops without progress → transitions to BLOCKED/NEEDS_HUMAN_DECISION
2. **Max tasks generated**: planner attempts to create > cap → excess rejected; system stops or proceeds safely
3. **No progress detector**: identical verifier output N times → safe stop with summary
4. **Exit-code loops**: repeated restart-trigger exit code (e.g., 42 policy) → BLOCKED after threshold

**Evidence:** scenario test harness with controlled stub executor outputs.

---

### 7.9 Observability Tests
**Goal:** Prove logs are sufficient to debug.

#### Tests
1. Every state transition emits an event.
2. Events include: timestamp, agent_id, task_id, action, metadata.
3. On failure, a single “run summary” event is emitted with:
   - cause
   - key evidence pointers (log offsets / file paths)

**Evidence:** tests that parse `events.jsonl` and enforce schema completeness.

---

## 8) Validation Test Plan (Real Workflows)

Validation proves the system achieves its intent on realistic usage.

### 8.1 Fixture Repos
Create small, deterministic fixture repositories under `tests/fixtures/` such as:

- `fixture_api_bugfix/`
  - a minimal python/ts API with a failing test and a clear spec
- `fixture_cli_feature/`
  - small CLI app requiring a new command + docs
- `fixture_refactor_guardrails/`
  - intentionally ambiguous spec to force NEEDS_HUMAN_DECISION

Each fixture includes:
- `specs/vision.md`
- `specs/delivery.md`
- expected acceptance tests in repo

### 8.2 End-to-End Scenarios (E2E)
Each E2E scenario must:
1. Start from clean state
2. Run planner → supervisor loop in “autonomous mode”
3. End in PASS or NEEDS_HUMAN_DECISION for expected cases
4. Collect artifacts and compare to expectations

#### E2E Scenario Examples
- **S1: Happy path**
  - Spec is clear; acceptance tests exist; system implements change; tests pass.
- **S2: Spec mismatch discovered**
  - Auditor flags missing AC coverage; planner creates bounded task; system converges.
- **S3: Ambiguous spec**
  - Auditor flags ambiguity; planner creates “clarify spec” task; run stops in NEEDS_HUMAN_DECISION.
- **S4: Runaway protection**
  - Executor produces repeated failures with no diff changes; system stops safely under caps.

**PASS criteria for validation:** expected end state + expected artifacts + no budget overruns beyond allowed.

---

## 9) Traceability Matrix (Spec → Tasks → Tests)

**Goal:** Prove every acceptance criterion is covered by verification.

### Requirement Traceability Rules
For each acceptance criterion (AC):
- Must map to:
  - at least one test OR one verification command
  - one or more tasks implementing it

### Implementation recommendation
Maintain a simple table or YAML file per delivery spec:
- `specs/traceability.yaml`

Example:
```yaml
ac_1:
  description: "Endpoint returns JSON schema X"
  tasks: ["task-12", "task-13"]
  verification:
    - "pytest tests/test_endpoint_schema.py"
ac_2:
  description: "P95 latency < 300ms on dataset Y"
  tasks: ["task-20"]
  verification:
    - "python scripts/bench.py --dataset Y --p95 300"
```

Validation includes a check that:
- every AC has at least one verification mapping
- every verification mapping exists and runs

---

## 10) Security & Safety Checks (Recommended)

Even if you’re not shipping a security product, add baseline checks:
- dependency scan (where possible)
- secrets scan (prevent committing tokens)
- safe prompt/command boundaries:
  - verify command runner cannot execute outside sandbox (if you sandbox)
  - path traversal protection in file operations

---

## 11) Performance & Robustness Tests (Recommended)

### Performance
- time to claim tasks under concurrency
- time to run spec validation
- overhead of logging

### Robustness / Chaos
- kill supervisor mid-task; verify recovery
- corrupt state attempt; ensure validator blocks
- network failure (if coordinator used)

---

## 12) CI Implementation Plan

### Required CI Jobs
1. `lint-and-typecheck`
2. `unit-tests`
3. `integration-tests`
4. `e2e-scenarios` (may be nightly if slow)
5. `artifact-upload-on-failure`

### Artifact Rules
On any failure:
- upload `events.jsonl`, `state.yaml`, and test outputs

---

## 13) Release Criteria

A change set is releasable when:
- all CI jobs pass
- at least one E2E happy-path scenario passes
- runaway tests demonstrate safe stopping
- traceability check passes for all fixtures (or real project specs)

---

## 14) Maintenance: Keeping V&V Current

- New features must add:
  - at least one unit test
  - at least one integration or E2E test (when behavior spans modules)
- Every new acceptance criterion must update traceability mapping.
- Run E2E suite nightly or on release branches if it’s too slow for PR CI.

---

## Appendix A — Suggested File Layout

```
specs/
  vision.md
  delivery.md
  traceability.yaml
tests/
  unit/
  integration/
  e2e/
  fixtures/
logs/
  events.jsonl
internal/
  specvalidate/
  verify/
  observability/
  runtime/
```

---

## Appendix B — Minimal “One-Command” V&V Script (Concept)

You can provide a wrapper script (example name):
- `scripts/vnv.sh`

Responsibilities:
1. validate specs
2. run lint/typecheck/unit/integration
3. run fixture E2E
4. summarize results + store artifacts

(Exact commands depend on your repo tooling.)

---

# End of Document
