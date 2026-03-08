# Configuration Reference

System configuration, tuning parameters, and environment variables.

## MCP Server Setup

Liza provides an MCP server (`liza-mcp`) for Claude Code integration. `liza init` creates both configuration files automatically. If they already exist, `liza init` prompts to merge Liza-specific configuration.

### What Gets Created

**`.mcp.json`** — tells Claude Code how to start the Liza MCP server:

```json
{
  "mcpServers": {
    "liza": {
      "command": "liza-mcp",
      "args": ["--project-root", "."]
    }
  }
}
```

**`.claude/settings.json`** — project-level permissions for Liza MCP tools, skills, git operations, and build commands.

`liza init` writes this file automatically from the master [`claude-settings.json`](../claude-settings.json). The master defines all Liza MCP tools, skills, and the full set of bash permissions agents need. **Do not hand-craft a subset** — agents will be blocked on any missing permission.

**Key elements:**
- **`enableAllProjectMcpServers`** / **`enabledMcpjsonServers`** — enables the Liza MCP server defined in `.mcp.json`
- **`mcp__liza__*`** — grants permission to invoke specific MCP tools (format: `mcp__<server>__<tool>`)
- **`Skill(...)`** — contract skills from `~/.liza/skills/` (installed by `liza setup`)
- **`defaultMode: acceptEdits`** — required for headless agent operation

### Two-Layer Architecture

Claude Code unions permissions from global and project settings:

| Layer | File | Managed by | Contains |
|-------|------|-----------|----------|
| **Project** | `<project>/.claude/settings.json` | `liza init` (automatic) | Liza MCP tools, skills, git/build commands |
| **Global** | `~/.claude/settings.json` | Manual (one-time) | Personal MCP tools (IDE, search, etc.), `additionalDirectories`, `Read(~/.liza/**)` |

The project layer is portable (team-shared). The global layer is machine-specific (personal tools and paths). Neither alone is sufficient — both are needed.

For global settings setup and provider-specific config (Claude, Codex, Gemini), see [Contract Activation](../contracts/contract-activation.md).

### Troubleshooting MCP

**Server won't start:**
- Verify `liza-mcp` is in PATH: `which liza-mcp`
- Check `.mcp.json` exists in project root
- Ensure `.liza/` directory exists

**Permission denied:**
- Verify `settings.json` includes MCP tool permissions (e.g., `"mcp__liza__liza_add_task"`)
- Ensure `enableAllProjectMcpServers: true` is set
- Ensure `enabledMcpjsonServers: ["liza"]` is set

**State file errors:**
- Verify project initialized: `liza validate`
- Check: `ls -la .liza/state.yaml`

### CLI vs MCP

Both interfaces operate on the same `state.yaml` with proper locking.

| | CLI (`liza add-task`) | MCP (`liza_add_task`) |
|---|---|---|
| Use case | Manual / interactive | Programmatic / agent |
| Output | Human-readable text | Structured JSON |
| Error handling | Exit codes + stderr | JSON error responses |

## Configuration Matrix

All configuration lives in `.liza/state.yaml` under the `config` section.

| Parameter | Default | Min | Max | Unit | Purpose |
|-----------|---------|-----|-----|------|---------|
| `max_coder_iterations` | 10 | 1 | 100 | count | Max iterations per coder per task |
| `max_review_cycles` | 5 | 1 | 20 | count | Max review rejection cycles |
| `heartbeat_interval` | 60 | 1 | 300 | seconds | Heartbeat frequency |
| `lease_duration` | 1800 | 300 | 7200 | seconds | Task lease duration |
| `coder_poll_interval` | 30 | 5 | 120 | seconds | Check interval (legacy, now event-driven) |
| `coder_max_wait` | 1800 | 300 | 7200 | seconds | Max idle before agent exits |
| `planner_poll_interval` | 60 | — | — | seconds | Planner polling interval |
| `planner_max_wait` | 1800 | — | — | seconds | Max planner idle before exit |
| `reviewer_poll_interval` | 30 | — | — | seconds | Reviewer polling interval |
| `reviewer_max_wait` | 1800 | — | — | seconds | Max reviewer idle before exit |
| `auditor_poll_interval` | 60 | — | — | seconds | Auditor polling interval |
| `auditor_max_wait` | 1800 | — | — | seconds | Max auditor idle before exit |
| `max_tasks_per_run` | 10 | 1 | 50 | count | Max finding-originated tasks per run (runaway protection) |
| `max_tasks_generated` | 20 | 1 | 100 | count | Global cap on planner-generated tasks |
| `max_agent_iterations` | 50 | 1 | 200 | count | Max total iterations before budget halt |
| `max_runtime_minutes` | 120 | 1 | 480 | minutes | Max runtime before budget halt |
| `exit42_restart_threshold` | 5 | — | — | count | Max exit-42 restarts without progress before task BLOCKED |
| `exit42_max_backoff_seconds` | 60 | — | — | seconds | Cap on exponential backoff between exit-42 restarts |
| `crash_retry_limit` | 5 | — | — | count | Max consecutive non-zero exits before task BLOCKED |
| `crash_retry_base_delay_seconds` | 5 | — | — | seconds | Initial exponential backoff delay on crash |
| `crash_retry_max_delay_seconds` | 120 | — | — | seconds | Cap on exponential backoff between crash retries |
| `enforce_requirement_refs` | false | — | — | bool | Require tasks to have requirement_refs |
| `enforce_deduplication` | false | — | — | bool | Reject duplicate task descriptions |
| `require_audit_for_sprint_close` | false | — | — | bool | Require audit pass before closing sprint |
| `diagnostic_logging` | false | — | — | bool | Enable verbose diagnostic log output |

### Agent Execution Timeouts

| Role | Timeout | Rationale |
|------|---------|-----------|
| Code Reviewer | 30 min | Reviews should complete quickly |
| Coder | 2 hours | Implementation takes longer |
| Planner | 4 hours | Complex planning needs time |
| Auditor | 1 hour | Advisory review of merged tasks with structured findings |

When exceeded, supervisor kills CLI, resets agent to IDLE, retries after 5s delay.

**Note:** Planners now respect `planner_max_wait` (default 30 minutes). Previously planners ran indefinitely; they now exit after the configured idle timeout, same as coders and reviewers.

### Hardcoded Safety Limits

These limits are not configurable via state.yaml:

| Limit | Value | Purpose |
|-------|-------|---------|
| Merge CAS retries | 3 | Max retries when concurrent merges conflict |
| Auditor stale-run limit | 3 | Consecutive runs without findings before auditor exits |
| Anomaly: stagnation threshold | 3 | Consecutive same-task failures before HIGH alert |
| Anomaly: no-diff retry threshold | 3 | Consecutive no-code-change iterations before MEDIUM alert |
| File lock timeout | 10s | Max wait to acquire state.yaml lock |
| MCP max request size | 10 MB | Max JSON-RPC request payload |

### Task Quality Gate

Tasks transitioning to IMPLEMENTING must include:

| Field | Required | Purpose |
|-------|----------|---------|
| `acceptance_criteria` | Yes (non-empty) | What "done" looks like |
| `verify_commands` | Yes (non-empty) | Deterministic verification commands |
| `spec_ref` | Yes (existing) | Traceability to specification |
| `done_when` | Yes (existing) | Human-readable completion criteria |

Tasks with `origin_finding_id` trace back to an audit finding. Tasks with `origin_task_id` trace to the original task the finding was raised against.

### Audit Findings

Auditor agents produce structured findings stored in `state.yaml` under `audit_findings`:

| Field | Required | Values |
|-------|----------|--------|
| `severity` | Yes | `LOW`, `MEDIUM`, `HIGH` |
| `type` | Yes | `SPEC_MISMATCH`, `MISSING_TEST`, `QUALITY_ISSUE`, `SECURITY_CONCERN` |
| `spec_reference` | Yes | Which AC or spec section |
| `evidence` | Yes | What the auditor observed |
| `recommended_action` | Yes | What should be done |

Findings are **advisory** — they cannot set PASS/FAIL or bypass deterministic verification.

## Tuning Guidelines

### Short Tasks (<10 min)
```yaml
config:
  heartbeat_interval: 30
  lease_duration: 900       # 15 min
  coder_max_wait: 600       # 10 min
  max_coder_iterations: 5   # Escalate fast
```

### Long Tasks (30min-2hr)
```yaml
config:
  heartbeat_interval: 60
  lease_duration: 3600      # 1 hour
  coder_max_wait: 7200      # 2 hours
  max_coder_iterations: 15  # More iterations
```

### Network Filesystems (NFS, SMB)
```yaml
config:
  heartbeat_interval: 90    # Less frequent writes
  lease_duration: 2700      # 45 min
  # fsnotify may not work -- agents fall back to polling
```

### Fast Feedback
```yaml
config:
  max_coder_iterations: 5   # Escalate faster
  max_review_cycles: 3      # Fewer rejection cycles
  heartbeat_interval: 30    # Faster crash detection
```

## System Modes

### Auto-Approve (`--auto-approve`)

The `--auto-approve` flag on `liza agent` commands passes `--dangerously-skip-permissions` to the underlying CLI (Claude, Codex, Gemini, etc.), suppressing interactive permission prompts.

**What it controls:** CLI-level permission prompts only (file edits, shell commands, MCP tool calls).

**What it does NOT control:**
- Task review verdicts (reviewers still APPROVE/REJECT independently)
- Merge gating (tasks must be APPROVED to merge)
- Verification commands (still run and enforced)
- State transitions (still validated against lifecycle rules)

**When to enable it:**
- Headless/unattended operation (CI/CD, overnight runs)
- `.claude/settings.json` already grants all necessary permissions
- You trust the agent's actions within the project scope

**When NOT to enable it:**
- First run on a new project (review what agents want to do)
- Shared environments where agents could affect other users
- Projects with destructive commands (database migrations, deploys)

**Risk assessment:** LOW for Liza's use case. Liza's `.claude/settings.json` already pre-approves all known MCP tools and bash commands. Without `--auto-approve`, the CLI may hang on interactive prompts in non-interactive `-p` mode, which is worse than auto-approving pre-whitelisted operations.

**Recommendation:** Enable `--auto-approve` for production multi-agent runs. The permission whitelist in `.claude/settings.json` is the real security boundary, not the interactive prompt.

## System Modes

| Mode | Agents | Heartbeats | Set by |
|------|--------|------------|--------|
| `RUNNING` | Work normally | Yes | `liza resume` / `liza start` |
| `PAUSED` | Block, don't claim | Yes | `liza pause` |
| `STOPPED` | Exit cleanly | Stop | `liza stop` |
| `CIRCUIT_BREAKER_TRIPPED` | Halt | Yes | `liza analyze` or `liza watch` (auto on pattern trigger) |

**PAUSED**: Agents stay alive, resume instantly. Use for manual edits.
**STOPPED**: Agents exit. Must restart manually. Use for end of session.

```
RUNNING <-> PAUSED (liza pause / liza resume)
RUNNING -> STOPPED (liza stop)
STOPPED -> RUNNING (liza start, then restart agents)
CIRCUIT_BREAKER_TRIPPED -> RUNNING (liza resume, after fixing root cause)
```

When `liza watch` triggers the circuit breaker, it also sets `sprint.status` to `CHECKPOINT`.

`liza watch` also auto-checkpoints when all non-terminal planned tasks are BLOCKED (sprint stalled), since no agent can make further progress without human intervention.

## Task Lifecycle States

| Status | Claimable | Reviewable | Terminal |
|--------|-----------|------------|----------|
| DRAFT | No | No | No |
| READY | Yes | No | No |
| IMPLEMENTING | No | No | No |
| READY_FOR_REVIEW | No | Yes | No |
| REJECTED | Yes | No | No |
| APPROVED | No | No | No |
| MERGED | No | No | **Yes** |
| BLOCKED | No | No | No |
| ABANDONED | No | No | **Yes** |
| SUPERSEDED | No | No | **Yes** |
| INTEGRATION_FAILED | Yes | No | No |
| NEEDS_HUMAN_DECISION | No | No | No |

**NEEDS_HUMAN_DECISION**: A safe stop state. The system halts a task when it encounters spec ambiguity, conflicting requirements, or exceeds budget/time caps. Valid transitions: IMPLEMENTING→NHD, BLOCKED→NHD; from NHD: →READY (resume), →ABANDONED, →SUPERSEDED. Requires human resolution before proceeding.

## Supported CLIs

The `--cli` flag on `liza agent` selects which coding agent to invoke:

| CLI | Default | Notes |
|-----|---------|-------|
| `claude` | Yes | Claude Code |
| `codex` | No | OpenAI Codex CLI |
| `gemini` | No | Google Gemini CLI |
| `mistral` | No | Mistral Le Chat CLI |
| `kimi` | No | Kimi (alias to claude with Kimi-specific env vars) |

## Output Logging

The `--log` flag on `liza agent` saves a copy of the agent's output to `.liza/agent-outputs/`. Stdout is saved as `{agent-id}-{timestamp}.txt` and stderr as `{agent-id}-{timestamp}.err`. The directory is created automatically if it does not exist.

```bash
liza agent coder --agent-id coder-1 --log
```

`--log` is incompatible with `-i` (interactive mode).

## Agent Identity

Agent identity can be provided in two ways:

1. **CLI flag** (recommended): `liza agent coder --agent-id coder-1`
2. **Environment variable**: `export LIZA_AGENT_ID=coder-1`

The `--agent-id` flag takes precedence over `LIZA_AGENT_ID`.

**Agent ID format**: `{role}-{number}` — e.g. `coder-1`, `code-reviewer-1`, `planner-1`.

**System commands** (`pause`, `stop`, `start`, `resume`, `release-claim`) use `--changed-by` for audit trail (defaults to `human`).

## Environment Variables

| Variable | Required | Default | Purpose |
|----------|----------|---------|---------|
| `LIZA_AGENT_ID` | For agent commands | -- | Agent identifier (format: `{role}-{number}`) |
| `LIZA_SPECS` | No | `specs/` | Path to specs directory (relative to project root) |
| `LIZA_LOG_LEVEL` | No | `INFO` | Logging verbosity: DEBUG, INFO, WARN, ERROR |

## Making Configuration Changes

1. `liza pause --reason "config update"`
2. Edit `state.yaml` (or use commands)
3. `liza validate`
4. `liza resume`

**Never edit state.yaml while agents are running** without pausing first.
