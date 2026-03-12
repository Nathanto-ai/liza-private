# Liza - Usage Guide

## Activation of the Contract for Pairing Agents

See [Contract Activation](../contracts/contract-activation.md).

## Liza

See [DEMO](DEMO.md) for a full example.

### Project Structure

```
~/.liza/                               # Created by `liza setup`
├── CORE.md                            # Universal rules + mode selection gate
├── PAIRING_MODE.md                    # Human-supervised collaboration
├── MULTI_AGENT_MODE.md                # Peer-supervised Liza system
├── AGENT_TOOLS.md                     # Agent tool contracts
├── COLLABORATION_CONTINUITY.md        # Session continuity
└── skills/                            # Skill definitions
    ├── code-review/SKILL.md
    ├── debugging/SKILL.md
    └── ...

<project>/
├── GUARDRAILS.md                  # Project-specific constraints (optional)
├── .liza/
│   ├── state.yaml                 # Current state
│   ├── log.yaml                   # Activity history
│   └── archive/                   # Terminal-state tasks
└── .worktrees/
    └── task-N/                    # Per-task workspace
```

### Project Guardrails

`GUARDRAILS.md` is an optional file at the project root that defines project-specific constraints for Liza agents. It uses the same tier system (Tier 0-3) from the core contract:

- **Tier 0 (Inviolable)** — Triggers mandatory halt (RESET) if violated
- **Tier 1 (Hard Constraints)** — Suspended only with explicit waiver
- **Tier 2 (Strong Defaults)** — Best-effort under pressure
- **Tier 3 (Preferences)** — Degraded gracefully

**How it's created:** `liza init` writes a template with empty tier sections. You can also create it manually.

**How to use it:** Fill in the tier sections with project-specific rules. Agents read and enforce `GUARDRAILS.md` automatically during their initialization sequence. If the file doesn't exist, agents are governed by the core contract only.

### Quick Start (Target Usage)

**Prerequisites:**
- Claude Code CLI and git installed
- Go >= 1.25.5 installed
- `liza` and `liza-mcp` Go binaries in PATH

**Installing the Liza CLI:**

```bash
# Build
make build

# Copy to a directory in PATH
sudo cp liza liza-mcp /usr/local/bin/

# Verify
liza version
```

**1. Global Setup (one-time)**
```bash
liza setup          # installs contracts + skills to ~/.liza/
liza setup --force  # overwrite existing (e.g., after liza upgrade)
```

**2. Initialize Project**
```bash
# Create .liza/ directory with blackboard
liza init "[Goal description]" --spec [spec_ref]

# spec_ref: Path to goal specification (default: specs/vision.md)
# Examples:
#   liza init "Implement retry logic"                        # uses specs/vision.md
#   liza init "Add auth" --spec specs/auth-feature.md        # uses custom spec

# Verify
cat .liza/state.yaml
```

`liza init` creates:
- `.liza/state.yaml` — Blackboard state
- `.liza/log.yaml` — Activity log
- `.claude/settings.json` — Claude Code project permissions (Liza MCP tools, skills, git/build commands)
- `.mcp.json` — MCP server configuration (tells Claude Code how to start liza-mcp)
- `CLAUDE.md`, `AGENTS.md`, `GEMINI.md` — Symlinks to `~/.liza/CORE.md`
- `GUARDRAILS.md` — Project-specific constraints template (if not already present)
- `integration` branch — For merging completed work

Contracts and skills live in `~/.liza/` (global, from `liza setup`), not in the project.
Operational reference content (blackboard fields, anomaly types, etc.) is inlined directly into agent prompts.

**3. Start Agents (3 terminals)**

Agent identity is provided via the `--agent-id` flag. IDs must follow the pattern `{role}-{number}` (e.g., `coder-1`, `code-reviewer-1`, `planner-1`, `auditor-1`).

Terminal 1 — Planner:
```bash
liza agent planner --agent-id planner-1
```

Terminal 2 — Coder:
```bash
liza agent coder --agent-id coder-1
```

Terminal 3 — Code Reviewer:
```bash
liza agent code-reviewer --agent-id code-reviewer-1
```

Terminal 4 — Auditor (optional):
```bash
liza agent auditor --agent-id auditor-1
```

Each agent command accepts a `--cli` flag to select the coding agent CLI: `claude` (default), `codex`, `gemini`, `mistral`, or `kimi`. For example: `liza agent coder --agent-id coder-1 --cli gemini`.

Pass `--auto-approve` to skip CLI permission prompts (passes `--dangerously-skip-permissions` to the underlying CLI). This eliminates human confirmation for file edits, command execution, and MCP tool calls. Recommended for unattended/headless operation where `.claude/settings.json` already grants all necessary permissions. Without `--auto-approve`, the CLI may block on interactive prompts that can't be answered in `-p` mode.

Pass `--log` to persist the agent's output to `.liza/agent-outputs/` (stdout as `.txt`, stderr as `.err`). Incompatible with `-i`.
See [Analyzing Agent Logs](#analyzing-agent-logs) for analysis tools.

Note that it is possible to run multiple agents of the same roles in different terminals.
```bash
liza agent coder --agent-id coder-1
```
```bash
liza agent coder --agent-id coder-2
```

**3. Observe**
```bash
# Run the watcher for alerts and automatic circuit-breaker escalation
liza watch
```

```bash
# Watch blackboard state
watch -n 2 'liza get tasks --format table'
```

**4. Human Interventions**
```bash
# Pause all agents
liza pause

# Resume
liza resume

# Abort
liza stop

# Checkpoint (halt + generate summary)
liza sprint-checkpoint
```

**Signal handling:** Agents cleanly exit on `Ctrl+C` (SIGINT) or `kill` (SIGTERM). On exit, the agent unregisters and atomically releases any active task claim — the task returns to READY (coder) or READY_FOR_REVIEW (reviewer) — so no orphaned claims are left behind.

**5. Review Results**
```bash
# Activity log
cat .liza/log.yaml

# Integration branch
git log integration --oneline
```

### Running Multiple Sprints

After a sprint completes (all tasks MERGED/ABANDONED), the system pauses at a checkpoint.
To start a new sprint:

1. Remove the old blackboard: `rm -rf .liza`
2. Re-initialize: `liza init "<new goal>" --spec <spec_ref>`
3. Restart agents

The planner does not auto-detect changes to `vision.md` between sprints. Each sprint starts fresh from `liza init`.

### CLI Commands

The `liza` binary provides all system operations. Commands are organized by category:

**Setup & Validation:**

| Command | Purpose |
|---------|---------|
| `liza init <goal> --spec <spec_ref>` | Initialize .liza/ directory with blackboard (spec_ref defaults to specs/vision.md) |
| `liza setup` | Install global files (~/.liza/ contracts, skills) |
| `liza validate [state.yaml]` | Validate blackboard state against schema invariants |
| `liza validate-spec <spec_ref>` | Validate a spec file against Liza spec conventions |
| `liza version` | Print Liza version |

**Agent Supervision:**

| Command | Purpose |
|---------|---------|
| `liza agent <role> --agent-id <id>` | Agent supervisor (start, restart, backoff loop) |

Roles: `coder`, `code-reviewer`, `planner`, `auditor`. Flags: `--cli <name>`, `--auto-approve`, `--log`, `-i` (interactive).

**Task Lifecycle (used by agents via MCP):**

| Command | Purpose |
|---------|---------|
| `liza add-task` | Create a new task (planner) |
| `liza claim-task <task-id> <agent-id>` | Claim a task (creates worktree, updates state) |
| `liza write-checkpoint <task-id>` | Record pre-execution checkpoint (intent, files, validation plan) |
| `liza submit-for-review <task-id> <commit>` | Submit implemented task for code review |
| `liza handoff <task-id> <agent-id>` | Hand off task on context exhaustion |
| `liza submit-verdict <task-id> <verdict>` | Submit review verdict (APPROVE/REJECT) |
| `liza mark-blocked <task-id>` | Block task with reason and clarifying questions |
| `liza release-claim <task-id> [--role R]` | Release claim on a task (manual recovery) |
| `liza supersede-task <old-id> <new-id>` | Replace a blocked/rejected task |
| `liza submit-audit-finding` | Submit structured audit finding (auditor) |

**Worktree Operations:**

| Command | Purpose |
|---------|---------|
| `liza wt-create <task-id>` | Create a Git worktree for a task |
| `liza wt-delete <task-id>` | Remove a task's worktree and branch |
| `liza wt-merge <task-id> <agent-id>` | Merge approved task to integration branch (CAS-safe, with tests) |

**System Control:**

| Command | Purpose |
|---------|---------|
| `liza pause` | Pause system (agents block, don't claim) |
| `liza resume` | Resume from PAUSED or CIRCUIT_BREAKER_TRIPPED |
| `liza stop` | Stop system (agents exit) |
| `liza start` | Start from STOPPED |
| `liza sprint-checkpoint` | Create checkpoint (halt + summary) |

**Monitoring & Analysis:**

| Command | Purpose |
|---------|---------|
| `liza status` | Show system status |
| `liza watch` | Monitor blackboard, alert on anomalies, auto-checkpoint on circuit-breaker |
| `liza analyze` | Run circuit-breaker analysis (detect systemic failure patterns) |
| `liza update-sprint-metrics` | Recalculate sprint metrics from current state |
| `liza clear-stale-review-claims` | Release expired reviewer claims |

**Query (`liza get`):**

| Command | Purpose |
|---------|---------|
| `liza get tasks [--format table\|json\|yaml]` | List tasks |
| `liza get agents [--format table\|json\|yaml]` | List agents |
| `liza get config` | Show configuration |
| `liza get sprint` | Show sprint status |
| `liza get findings` | List audit findings |

**Recovery & Admin:**

| Command | Purpose |
|---------|---------|
| `liza recover-task <task-id>` | Full task recovery (release claims + remove worktree/branch + recover agent) |
| `liza recover-agent <agent-id>` | Full agent recovery (release claim + remove worktree + delete agent) |
| `liza delete agent <agent-id>` | Delete an agent from state |
| `liza delete task <task-id>` | Delete a task from state |

**Important:** The supervisor claims tasks *before* starting the Claude agent. This avoids interactive permission prompts in `-p` (non-interactive) mode. Agents receive their assigned task in the bootstrap prompt and should NOT call claim commands directly.

### MCP Tools

Agents interact with Liza via MCP tools (JSON-RPC 2.0 over stdio). Every CLI mutation has an MCP equivalent. The MCP server (`liza-mcp`) registers 22 tools:

**Read-Only:**
`liza_get`, `liza_status`, `liza_validate`, `liza_version`

**Task Mutations:**
`liza_add_task`, `liza_claim_task`, `liza_submit_for_review`, `liza_handoff`, `liza_submit_verdict`, `liza_mark_blocked`, `liza_release_claim`, `liza_supersede_task`, `liza_submit_audit_finding`

**Complex Operations:**
`liza_wt_create`, `liza_wt_delete`, `liza_wt_merge`, `liza_analyze`, `liza_update_sprint_metrics`, `liza_sprint_checkpoint`, `liza_clear_stale_review_claims`, `liza_write_checkpoint`, `liza_delete_agent`

**Resources (MCP resource protocol):**
`liza://state` (full state), `liza://tasks` (task list), `liza://agents` (agent list)

All MCP tools must be listed in `.claude/settings.json` permissions for Claude Code to invoke them. `liza init` generates this automatically.

See [Architecture Overview](../specs/architecture/overview.md) for detailed component descriptions.

### Role Enforcement

Both CLI commands and MCP tools enforce role-based access control. Mutation commands validate the calling agent's role against an allowlist before executing.

**Role → Allowed Commands:**

| Role | Commands |
|------|----------|
| Coder | `claim-task`, `submit-for-review`, `handoff`, `write-checkpoint`, `wt-create`, `wt-delete`, `mark-blocked`, `release-claim`, `liza_exec` |
| Code Reviewer | `submit-verdict`, `wt-merge`, `claim-review`, `clear-stale-review-claims`, `wt-create`, `wt-delete`, `mark-blocked`, `release-claim` |
| Planner | `add-task`, `supersede-task`, `sprint-checkpoint`, `delete-agent`, `update-sprint-metrics`, `analyze` |
| Auditor | `submit-audit-finding`, `analyze`, `mark-blocked` |

**Behavior:**
- CLI commands extract the agent role from the `--agent-id` flag (e.g., `coder-1` → coder role)
- MCP tools are filtered per-role — non-permitted tools are not registered
- MCP handlers additionally call `requireRole()` as defense-in-depth
- Empty agent ID (human/manual usage) bypasses the CLI check for backward compatibility

### Configuring Claude Code (MCP)

Liza integrates with Claude Code through the Model Context Protocol (MCP). `liza init` creates the configuration automatically:

**`.mcp.json`** — MCP server configuration:
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

**`claude-settings.json`** — Minimal permissions for Claude Code agents:
```json
{
  "additionalDirectories": [ "~/.liza" ],
  "permissions": {
    "defaultMode": "acceptEdits",
    "allow": [
      "Read(~/.claude/**)",
      "Read(~/.liza/**)",
      "mcp__liza__liza_get",
      "mcp__liza__liza_status",
      "mcp__liza__liza_add_task",
      "mcp__liza__liza_submit_for_review",
      "mcp__liza__liza_submit_verdict",
      "Bash(git add:*)",
      "Bash(git commit:*)",
      "Bash(git status:*)",
      "Bash(git diff:*)",
      "WebFetch"
    ]
  }
}
```

Both CLI commands (e.g., `liza add-task`) and MCP tools (e.g., `liza_add_task`) operate on the same `.liza/state.yaml` file. Claude Code agents use MCP tools for better error handling; the CLI is for manual use.

The root-level `claude-settings.json` and `mcp.json` are templates embedded into the binary. `liza init` writes the active copies to `.claude/settings.json` and `.mcp.json` in the project directory.

### Analyzing Agent Logs

Logs captured with `--log` are NDJSON files (one JSON object per line) from `claude --verbose --output-format stream-json`. Two formats exist depending on the agent role:

| Format | First event | Seen in | Token detail |
|--------|-------------|---------|--------------|
| **Rich** | `type: system` | Planner | Per-API-call breakdown (input, cache, output) |
| **Sparse** | `type: thread.started` | Coder, Reviewer | Aggregate only (`turn.completed`) |

Both analysis tools auto-detect the format.

**CLI analyzer** (`scripts/analyze-log.py`) — stdlib-only Python 3.12+, for batch/CI use:

```bash
# Single file
python3 scripts/analyze-log.py .liza/agent-outputs/planner-1-*.txt

# Multiple files
python3 scripts/analyze-log.py .liza/agent-outputs/*.txt
```

Report sections: session header, token summary (fresh/cached/output, cache hit rate), content breakdown by type (chars, estimated tokens, share %), top 10 items by size, tool call frequency. Rich format adds per-turn context growth and cost breakdown.

**Browser analyzer** (`liza-session-analyzer.html`) — drag-and-drop, visual charts:

```bash
open liza-session-analyzer.html   # or xdg-open on Linux
```

Drop one or more log files. Produces the same analysis with bar charts for content breakdown and context growth.

**Raw inspection** with `jq` (no dependencies):

```bash
# Sparse format: extract items
jq -c 'select(.item) | .item | {type, text, command, tool, usage}
  | with_entries(select(.value != null))' .liza/agent-outputs/coder-1-*.txt

# Rich format: extract token usage per API call
jq -c 'select(.type == "assistant") | {id: .message.id, usage: .message.usage}' \
  .liza/agent-outputs/planner-1-*.txt
```

### Differences from Pairing Mode

| Aspect | Pairing Mode | Multi-Agent Mode |
|--------|--------------|------------------|
| Approval | Human approves | Peer agent approves |
| Gates | Approval request → wait | Pre-execution checkpoint → proceed |
| Communication | Conversation | Blackboard |
| Iteration | Human feedback | Code Reviewer feedback |
| Debugging | Debugging skill | Log anomaly, BLOCKED |
| Magic Phrases | Active | Not applicable |
| Session Init | Greet user | Silent execution |

### Supervisor Circuit Breaker

The supervisor automatically handles these conditions (transparent to agents):

| Condition | Action |
|-----------|--------|
| Agent crash loop (3× in 5min) | Supervisor stops the agent |
| Blackboard validation fails | All agents pause |
| Integration branch conflict | Task set to INTEGRATION_FAILED |
| Circuit-breaker pattern detected in anomalies | Set mode to `CIRCUIT_BREAKER_TRIPPED`, create sprint `CHECKPOINT`, write reports |
