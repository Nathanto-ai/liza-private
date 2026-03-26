# Liza V&V Pipeline Test Run — Operator Playbook

## Objective

Build a mid-complexity application **autonomously using Liza with Copilot (gpt-5-mini)**. Monitor the entire pipeline for issues, manual interventions, and anomalies. Stop when **10 issues occur** or the **application completes autonomously**. Write a performance report at the end. If the app completes, manually test it. If it already exists make a new version of the application in a new directory and repo. ONLY USE GPT 5 MINI TO RUN THE AGENTS THIS IS A HARD REQUIREMENT.

---

## Step 1: Create the Project

```powershell
mkdir recipe-api; cd recipe-api; git init
mkdir specs
```

### Vision Spec

Create `specs/vision.md`:

```markdown
# Vision: Recipe API

## Goal

Build a REST API for managing recipes using Go and the standard library (no frameworks).

## Requirements

- R1: GET /recipes — list all recipes (JSON array)
- R2: GET /recipes/{id} — get a single recipe by ID (JSON object, 404 if not found)
- R3: POST /recipes — create a new recipe (JSON body, return 201 + created recipe with generated ID)
- R4: PUT /recipes/{id} — update an existing recipe (JSON body, 404 if not found)
- R5: DELETE /recipes/{id} — delete a recipe (204 on success, 404 if not found)
- R6: GET /recipes?tag={tag} — filter recipes by tag
- R7: Data model: Recipe has id, title, description, ingredients (list of strings), steps (list of strings), tags (list of strings), prep_time_minutes (int), created_at, updated_at
- R8: In-memory storage (no database required) with thread-safe access
- R9: Input validation: title required (non-empty), at least one ingredient, at least one step
- R10: Structured JSON error responses: {"error": "message", "code": "ERROR_CODE"}
- R11: Request logging middleware (method, path, status, duration)
- R12: Graceful shutdown on SIGINT/SIGTERM
- R13: Health check endpoint: GET /health → {"status": "ok"}

## Constraints

- Go 1.21+ compatible
- Standard library only (net/http, encoding/json, sync, etc.)
- No external dependencies
- Include comprehensive tests (unit + integration)
- Must pass `go vet` and `go test ./...`

## Success Criteria

- All 13 requirements implemented and tested
- `go test -cover ./...` passes with ≥80% coverage
- Server starts on configurable port (default :8080)
- Manual smoke test: create, read, update, delete a recipe via curl
```

### Dev Tooling

```powershell
# Create go.mod
go mod init recipe-api

# Initial commit
git add .
git commit -m "Initial commit: vision spec and go.mod"
```

---

## Step 2: Initialize Liza

```powershell
liza setup          # one-time global setup (skip if already done)
liza init "Build Recipe API per specs/vision.md" --spec specs/vision.md
```

---

## Step 3: Configure for Long Leases + Copilot

Edit `.liza/state.yaml` config section to use long leases and gpt-5-mini:

```yaml
config:
  heartbeat_interval: 60
  lease_duration: 7200         # 2 hours — long lease for all agents
  coder_poll_interval: 30
  coder_max_wait: 7200         # 2 hours
  planner_poll_interval: 60
  planner_max_wait: 7200       # 2 hours
  reviewer_poll_interval: 30
  reviewer_max_wait: 7200      # 2 hours
  auditor_poll_interval: 60
  auditor_max_wait: 7200       # 2 hours
  max_coder_iterations: 15
  max_review_cycles: 5
  max_agent_iterations: 100
  max_runtime_minutes: 240     # 4 hour budget
  copilot_default_model: gpt-5-mini
  mcp_inactivity_timeout: 600  # 10 min — kill agent if no MCP tool calls
  max_no_submit_iterations: 3   # block task after 3 coder exits without submit (Fix 37)
  integration_branch: integration
```

---

## Step 4: Launch All 4 Agents

Open **4 separate terminals**. Each runs one agent:

```powershell
# Terminal 1 — Planner
cd recipe-api
liza agent planner --agent-id planner-1 --cli copilot

# Terminal 2 — Coder
cd recipe-api
liza agent coder --agent-id coder-1 --cli copilot

# Terminal 3 — Code Reviewer
cd recipe-api
liza agent code-reviewer --agent-id code-reviewer-1 --cli copilot

# Terminal 4 — Auditor
cd recipe-api
liza agent auditor --agent-id auditor-1 --cli copilot
```

**Record the start time:** `Get-Date -Format o`

---

## Step 5: Monitor — Issue & Intervention Log

Open a 5th terminal for monitoring:

```powershell
# Dashboard (refresh every 5s)
while ($true) { Clear-Host; liza status; Start-Sleep 5 }

# Or use watch-style:
# Task list
liza get tasks --format table

# Agent statuses
liza get agents

# Check for anomalies
Get-Content .liza/alerts.log -Wait
```

### What to watch for

| Category | Signals |
|----------|---------|
| **Stuck agent** | Same status for >10 min, no heartbeat updates |
| **Loop** | Same task claimed/crashed >3 times |
| **Wrong task type** | Planner creates non-"coding" tasks |
| **Idle waste** | Agent running but no work for >15 min with no backoff |
| **Timeout cascade** | liza_exec timeouts leading to misdiagnosis |
| **Deadlock code** | Coder writes code with mutex re-entrancy |
| **Merge conflict** | Integration merge fails repeatedly |
| **Planner misdiagnosis** | Planner trusts coder's wrong blocked reason |
| **Manual intervention** | You had to edit state.yaml, kill a process, or restart an agent |
| **Dep graph break** | Tasks stuck waiting on SUPERSEDED dependency (Fix 1 regressed) |
| **Auditor busy-loop** | Auditor cycling every 60s with no findings, burning tokens on READY tasks (Fix 3 regressed) |
| **Direct state.yaml edit** | Any agent editing .liza/state.yaml directly instead of using MCP tools (Fix 4 regressed) |
| **Coder on superseded task** | Coder continues work after task was SUPERSEDED, retries checkpoint/submit (Fix 5 regressed) |
| **Planner file creation** | Planner creates/writes project files instead of only calling liza MCP tools (Fix 9/17 regressed) |
| **Auditor/Reviewer invalid liza_get** | Auditor or reviewer calls liza_get with invalid resource subpaths like tasks/{id}/files (Fix 11/15 regressed) |
| **Rebase conflict stall** | Coder stuck on rebase conflict instead of auto-recovery handling it (Fix 12 regressed) |
| **Auditor read-only violation** | Auditor executes write commands (rm, mv, go run) through liza_exec instead of read-only verification (Fix 14 regressed) |
| **Reviewer liza_get subpath loop** | Code-reviewer loops through 50+ invalid liza_get subpaths per review (Fix 15 regressed) |
| **Remediation infinite loop** | Auditor→planner→coder cycle repeating on same finding type — circuit breaker should cap at depth 1 (Fix 16/21 regressed) |
| **Reviewer session without verdict** | Code-reviewer CLI session ends without submitting verdict, task stuck in REVIEWING (Fix 18 regressed) |
| **Duplicate audit findings** | Auditor files identical findings (same type+severity) across remediation chain (Fix 19 regressed) |
| **Agent idle with pending work** | Agent remains IDLE while tasks await processing — especially reviewer with REVIEWING tasks (Fix 20 regressed) |
| **File lock crash on state.yaml** | Agent crashes with "file is being used by another process" instead of retrying with backoff (Fix 22 — ✅ VALIDATED Run 5, regressed Run 9 in callback path — Fix 48 APPLIED) |
| **False positive TDD gate** | Coder blocked/rejected for missing tests when test files ARE in the diff (Fix 23 regressed) |
| **Budget exceeded without warning** | Agent hits budget limit with no prior 80% warning event in logs (Fix 24 — ✅ VALIDATED Run 5) |
| **Reviewer idle iteration burn** | Reviewer consuming iterations in idle loop — Fix 26 APPLIED: idle polls no longer count as iterations, reviewer uses exponential backoff |
| **Reviewer approval lost** | Reviewer approves task but state.yaml status stays at REVIEWING — Fix 27 APPLIED: post-write verification detects concurrent overwrites |
| **Auditor branch model mismatch** | Auditor verifies on master but code is on integration branch — Fix 28 APPLIED: auditor prompt now references integration branch + language-agnostic verification |
| **Reviewer adds flags not in spec** | Reviewer tries flags not in done_when criteria (e.g. -race) — Fix 29 APPLIED: reviewer prompt has ENVIRONMENT CONSTRAINTS section |
| **Planner duplicate remediation** | Planner creates remediation tasks for active origin tasks — Fix 30 APPLIED: AddTask rejects when origin task IMPLEMENTING/REVIEWING/REJECTED |
| **MCP inactivity stall** | Agent copilot session alive but no MCP tool calls for >10 min — Fix 31 APPLIED: supervisor monitors mcp-activity file, kills agent after mcp_inactivity_timeout (default 600s) |
| **Non-existent spec_ref in task** | Planner creates task with spec_ref pointing to file that doesn't exist — Fix 32 APPLIED: AddTask validates spec_ref file exists on disk before writing |
| **Reviewer edits source code** | Reviewer creates/modifies/deletes project files instead of read-only review — Fix 33 APPLIED: "ROLE BOUNDARY — READ-ONLY" section in reviewer prompt |
| **Coder calls task_complete** | Coder calls task_complete tool instead of liza_submit_work — Fix 34 APPLIED but ❌ FAILED Run 7: prompt-level fix insufficient. Fix 37 NEEDED: supervisor-level interception |
| **Coder long analysis no commits** | Coder spends >15 min analyzing before first commit — Fix 35 APPLIED but ❌ FAILED Run 7: coder made zero commits. Fix 37 NEEDED: no-progress counter |
| **Build artifacts in commits** | Generated files (binaries, .exe, vendor/) committed to repo — Fix 36 APPLIED: planner .gitignore gate + reviewer build artifact checklist |
| **Simultaneous agent crash** | All agents crash at same instant — observed Run 7 at T+10 min (R7-01). Investigate: external trigger, OneDrive lock, or MCP server crash |
| **INTEGRATION_FAILED deadlock** | Task approved but merge fails on verify_commands path — no recovery mechanism. Fix 38 NEEDED: retry from repo root |
| **Auditor excessive remediation filing** | Auditor files 5+ findings for same root cause (stale worktree paths) — Fix needed: circuit breaker limiting verification-gap findings |
| **state.yaml corruption via Set-Content** | PowerShell Set-Content + OneDrive locking deletes state.yaml — use [System.IO.File]::WriteAllText() instead. Fix 39 NEEDED: pre-write .bak backup |
| **Coder idle with READY tasks** | Coder in WAITING state while READY tasks exist — stale claim prevents re-entry to claim loop (R7-06). Fix 41 APPLIED: detectAndFixStaleClaim releases dead-agent claims |
| **task_complete loop** | Coder exits 0 without calling liza_submit_for_review; task reverts IMPLEMENTING→READY and is re-claimed infinitely. Fix 37 APPLIED: noSubmitTracker escalates task to NEEDS_HUMAN_DECISION after 3 consecutive no-submit exits. Fix 43 APPLIED: uses NEEDS_HUMAN_DECISION instead of BLOCKED to prevent planner wake cascade. Fix 46 APPLIED: prompt allows task_complete AFTER liza_submit_for_review for clean session exit |
| **Verification-gap remediation spiral** | Auditor files 5+ VERIFICATION_GAP findings for same root cause creating excess remediation tasks. Fix 40 APPLIED: circuit breaker limits to 2 per run |
| **state.yaml corruption/truncation** | state.yaml truncated or empty after crash/OneDrive lock. Fix 39 APPLIED: pre-write .bak backup + auto-recovery on read |
| **Planner meta-task cascade** | Planner creates repair/investigate/test/fix tasks targeting liza framework behavior that coders cannot implement. Fix 45 APPLIED: AddTask rejects tasks with framework-internal terms in description/done_when |
| **SUPERSEDED dep blocks downstream** | Downstream tasks permanently stuck because dependency was SUPERSEDED (not MERGED). Fix 44 APPLIED: IsClaimable treats SUPERSEDED deps as satisfied |
| **Reviewer session exit without verdict** | Reviewer copilot session exits without calling liza_submit_verdict, task stuck in REVIEWING. Fix 42 APPLIED: reviewer re-claims own REVIEWING tasks |
| **Copilot autopilot infinite loop** | Copilot in --autopilot mode loops 20+ times on task_complete after submission because prompt forbids task_complete. Fix 46 APPLIED: prompt now allows task_complete AFTER MCP submission |
| **MCP inactivity timeout not firing** | Monitor loops forever if activity file is missing (write failed, path mismatch). Fix 47 APPLIED: fallback wall-clock deadline of 2× timeout |
| **Sharing violation crashes agent** | Agent process crashes on state.yaml sharing violation during unregister because retry-with-backoff only retried stale lock errors, not fn() callback sharing violations. Fix 48 APPLIED: WithRetryBackoff now classifies and retries Windows sharing violations from callback. Fix 50 APPLIED: unregisterAgent retries up to 3 times on transient errors |
| **Race detector rejection cycle** | Reviewer rejects tasks with -race in verify_commands because CGO disabled on Windows. Planner supersedes and re-creates — infinite cycle. Fix 49 APPLIED: prompt builders sanitize verify_commands (strip -race on Windows). Fix 51 APPLIED: planner prompt has ENVIRONMENT CONSTRAINTS preventing -race in verify_commands |
| **Any surprise** | Anything unexpected, bizarre, or concerning |

### Issue Log Template

For each issue found, record:

```markdown
### Issue #N: [Short Title]
- **Time:** [timestamp]
- **Category:** [stuck/loop/timeout/idle/misdiagnosis/intervention/other]
- **Agent:** [which agent]
- **Task:** [which task ID, if applicable]
- **Description:** [what happened]
- **Root cause:** [why it happened, if known]
- **Action taken:** [what you did, or "none — observed only"]
- **Impact:** [time lost, pipeline stall, etc.]
```

---

## Step 6: Stopping Criteria

Stop the pipeline when **ANY** of these occur:

1. **10 issues/interventions logged** → run `liza stop`
2. **All tasks reach MERGED** → app is complete
3. **4 hours elapsed** → budget timeout

Record the stop time: `Get-Date -Format o`

---

## Step 7: Write the Performance Report

After stopping, create `PIPELINE_REPORT.md` with:

```markdown
# Pipeline Performance Report — Recipe API

## Summary
- **Project:** Recipe API (Go, stdlib only)
- **Model:** gpt-5-mini via Copilot CLI
- **Duration:** [start] → [stop] ([X] minutes)
- **Outcome:** [COMPLETED / STOPPED_AT_THRESHOLD / TIMED_OUT]
- **Tasks:** [N created, N merged, N blocked, N in-progress]
- **Issues found:** [N]
- **Manual interventions:** [N]

## Agent Performance

| Agent | Role | Iterations | Tasks Completed | Time Active | Issues |
|-------|------|-----------|-----------------|-------------|--------|
| planner-1 | Planner | ? | N/A | ? | ? |
| coder-1 | Coder | ? | ? | ? | ? |
| code-reviewer-1 | Reviewer | ? | ? | ? | ? |
| auditor-1 | Auditor | ? | N/A | ? | ? |

## Issue Log
[Paste all issues from Step 5]

## Comparison with Prior Runs
[Compare with bookshelf2 if applicable — did fixes help?]

## Fixes Validated
- [ ] Stuck-coder detection (auto-block after no progress)
- [ ] Idle auditor backoff (exponential delay)
- [ ] Enhanced liza_exec timeout hints (deadlock guidance)
- [ ] Task type enum enforcement (only "coding" accepted)
- [ ] Planner blocked-task verification (step 1b)
- [ ] Coder timeout troubleshooting (self-debug checklist)
- [ ] Worktree path in blocked context
- [ ] Planner file creation prohibition — all sessions (Fix 9/17)
- [ ] Auditor/reviewer invalid liza_get subpath detection (Fix 11/15)
- [ ] Rebase conflict auto-recovery (Fix 12)
- [ ] Auditor read-only exec enforcement (Fix 14)
- [ ] Code-reviewer liza_exec access + subpath errors (Fix 15)
- [ ] Remediation loop circuit breaker + depth limit (Fix 16/21)
- [ ] Reviewer session re-claim for stuck REVIEWING tasks (Fix 18)
- [ ] Duplicate finding deduplication (Fix 19)
- [ ] Agent health monitoring — reviewer/coder re-claim (Fix 20)
- [ ] File lock retry-with-backoff for state.yaml reads (Fix 22) — ✅ VALIDATED Run 5: No crashes in 73 min
- [ ] HasTestFiles false positive fix — debug logging + Java pattern tightening (Fix 23) — ❓ Run 5: Not triggered (Go project)
- [ ] Budget 80% warning event before hard exceeded (Fix 24) — ✅ VALIDATED Run 5: Warning fired at 80/100 iterations
- [ ] TDD error message lists all expected test file patterns (Fix 25) — ❓ Run 5: Not triggered
- [ ] Idle polls ≠ iterations — RecordIteration moved to after agent execution (Fix 26) — ✅ VALIDATED Run 6: All idle agents at iterations_total: 0 after 15+ min
- [ ] Reviewer idle backoff — reviewer stays alive with exponential backoff like auditor (Fix 26) — ✅ VALIDATED Run 6: Reviewer stayed alive and claimed tasks correctly
- [ ] Verdict post-write verification — re-reads state to confirm status change persisted (Fix 27) — ✅ VALIDATED Run 6: 5 task transitions all succeeded cleanly
- [ ] Auditor integration branch — prompt references integration branch for verification (Fix 28) — ✅ VALIDATED Run 6: Auditor prompt shows "INTEGRATION BRANCH: integration"
- [ ] Auditor language-agnostic verification — no hardcoded Go commands (Fix 28) — ✅ VALIDATED Run 6: Language-agnostic detection in auditor prompt
- [ ] Reviewer environment constraints — done_when-only flags, no -race (Fix 29) — ✅ VALIDATED Run 6: Reviewer did NOT add -race in any of 4 reviews
- [ ] Active origin task rejection — blocks remediation while origin IMPLEMENTING/REVIEWING/REJECTED (Fix 30) — ❓ Run 6: Not triggered (no remediation scenario occurred)
- [ ] MCP inactivity timeout — supervisor kills agent after mcp_inactivity_timeout seconds with no MCP tool calls (Fix 31) — ❓ Run 7: Not cleanly tested (agent crashes/restarts muddied observation)
- [ ] Atomic spec_ref validation — AddTask rejects tasks with non-existent spec_ref file (Fix 32) — ✅ VALIDATED Run 7: No invalid spec_ref errors during 14 task creations
- [ ] Reviewer read-only role boundary — explicit prompt prohibition on file creation/editing (Fix 33) — ❓ Run 7: Inconclusive (reviewer did not complete independent review cycles)
- [ ] Coder task_complete prohibition — explicit warning not to call task_complete (Fix 34) — ❌ FAILED Run 7: Coder called task_complete 17+ times across 2 tasks despite prompt prohibition; prompt-level fix insufficient
- [ ] Coder incremental commits — prompt guidance to commit early and often (Fix 35) — ❌ FAILED Run 7: Coder made zero commits across 12+ iterations on middleware task
- [ ] Planner .gitignore build hygiene — first task must include .gitignore creation (Fix 36) — ✅ VALIDATED Run 7: setup-repo task included .gitignore; file present on integration
- [ ] Reviewer build artifact checklist — reject if committed files include generated artifacts (Fix 36) — ❓ Run 7: Not triggered (no build artifacts committed)
- [ ] task_complete supervisor interception — detect coder exit without IMPLEMENTING→REVIEWING transition (Fix 37) — ✅ VALIDATED Run 8: correctly blocked implement-crud-handlers after 3 no-submit exits
- [ ] INTEGRATION_FAILED auto-recovery — retry verify_commands from repo root (Fix 38) — ❓ Run 8: Not triggered (no INTEGRATION_FAILED scenarios)
- [ ] Pre-write state.yaml backup — copy to .bak before every write (Fix 39) — ❓ Run 8: Not triggered (no corruption observed)
- [ ] Verification-gap circuit breaker — cap VERIFICATION_GAP findings at 2 per run, force LOG_ONLY beyond (Fix 40) — ❓ Run 8: Not triggered (only 2 audit findings)
- [ ] Stale claim detector — auto-release IMPLEMENTING tasks from dead agents on claim failure (Fix 41) — ❓ Run 8: Not triggered (no stale claims observed)
- [ ] Reviewer re-claim own REVIEWING tasks — reviewer re-claims task stuck in REVIEWING after session exit (Fix 42) — ✅ VALIDATED Run 8: reviewer successfully re-claimed create-gitignore REVIEWING task
- [ ] No-submit escalation to NEEDS_HUMAN_DECISION — blockTaskOnNoSubmit uses NEEDS_HUMAN_DECISION instead of BLOCKED to prevent planner wake cascade (Fix 43) — ❓ Run 9: Not triggered (Fix 46 prevents the scenario)
- [ ] SUPERSEDED dependency satisfaction — IsClaimable treats SUPERSEDED deps as satisfied so downstream tasks can proceed (Fix 44) — ✅ VALIDATED Run 9 (partial): SUPERSEDED tasks exist, downstream tasks claimable
- [ ] Framework meta-task rejection — AddTask rejects tasks whose description/done_when references liza-internal terms (Fix 45) — ❓ Run 9: Not triggered
- [ ] Prompt allows task_complete after submission — coder/reviewer prompts now instruct calling task_complete AFTER MCP submission for clean session exit, preventing copilot autopilot infinite loops (Fix 46) — ✅ VALIDATED Run 9: coder exits cleanly after every submission, no more infinite loops
- [ ] MCP monitor fallback deadline — monitorMCPActivity enforces 2× timeout wall-clock fallback when activity file never appears (Fix 47) — ❓ Run 9: Not triggered
- [ ] Context.Canceled handling in executeAgent — MCP timeout cancellation returns exit code 1 instead of crashing supervisor (Fix 47b) — ❓ Run 9: Not triggered
- [ ] Sharing violation retry in WithRetryBackoff — classifies Windows errno 32 as LockErrorSharingViolation and retries fn() callback sharing violations, preventing agent crashes during concurrent state.yaml access (Fix 48) — ⏳ PENDING Run 9: code and tests pass, not yet deployed
- [ ] Env-aware prompt verify_commands — prompt builders sanitize task verify_commands and done_when via StripRaceFlagIfNeeded before rendering, so agents see the commands that will actually execute (Fix 49) — NEW
- [ ] Auto-retry unregisterAgent — unregisterAgent retries up to 3 times with 500ms backoff on transient errors, preventing agent crash on shutdown due to sharing violations (Fix 50) — NEW
- [ ] Planner environment constraints — planner prompt now includes ENVIRONMENT CONSTRAINTS section prohibiting -race in verify_commands on Windows (Fix 51) — NEW

## Root Cause Analysis
[Diagram or prose: what was the cascade? Single root cause or independent?]

## Recommendations
[What to fix next]
```

---

## Step 8: Manual Application Test (if completed)

If all tasks merged and the app built successfully:

```powershell
# Build
cd recipe-api
git checkout integration
go build -o recipe-api.exe ./...

# Start the server
.\recipe-api.exe

# In another terminal — smoke test:
# Health check
curl http://localhost:8080/health

# Create a recipe
curl -X POST http://localhost:8080/recipes -H "Content-Type: application/json" -d '{
  "title": "Pasta Carbonara",
  "description": "Classic Italian pasta dish",
  "ingredients": ["spaghetti", "eggs", "pecorino", "guanciale", "black pepper"],
  "steps": ["Boil pasta", "Fry guanciale", "Mix eggs and cheese", "Combine all"],
  "tags": ["italian", "pasta"],
  "prep_time_minutes": 25
}'

# List recipes
curl http://localhost:8080/recipes

# Get by ID (use ID from create response)
curl http://localhost:8080/recipes/{id}

# Update
curl -X PUT http://localhost:8080/recipes/{id} -H "Content-Type: application/json" -d '{
  "title": "Pasta Carbonara (Updated)",
  "description": "Classic Italian pasta dish - family recipe",
  "ingredients": ["spaghetti", "eggs", "pecorino romano", "guanciale", "black pepper"],
  "steps": ["Boil pasta al dente", "Fry guanciale until crispy", "Mix eggs and cheese off heat", "Combine and toss quickly"],
  "tags": ["italian", "pasta", "classic"],
  "prep_time_minutes": 30
}'

# Filter by tag
curl "http://localhost:8080/recipes?tag=italian"

# Delete
curl -X DELETE http://localhost:8080/recipes/{id}
# Should return 204

# Verify deleted
curl http://localhost:8080/recipes/{id}
# Should return 404
```

Record results: all pass / which fail / error messages.

---

## Quick Reference

| Command | Purpose |
|---------|---------|
| `liza status` | Dashboard |
| `liza get tasks --format table` | Task list |
| `liza get agents` | Agent statuses |
| `liza get tasks/{id}` | Single task detail |
| `liza stop` | Stop all agents gracefully |
| `liza validate` | Check state consistency |
| `liza inspect traceability` | Full requirement traceability |
| `Get-Content .liza/log.yaml -Tail 20` | Recent log entries |
| `Get-Content .liza/alerts.log -Wait` | Live alerts |


