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

The `liza` binary provides all system operations. Key commands:

| Command | Purpose |
|---------|---------|
| `liza init <goal> --spec <spec_ref>` | Initialize .liza/ directory with blackboard (spec_ref defaults to specs/vision.md) |
| `liza agent <role> --agent-id <id>` | Agent supervisor (start, restart, backoff loop) |
| `liza claim-task <task-id> <agent-id>` | Atomically claim a task for a coder (creates worktree, updates state) |
| `liza validate [state.yaml]` | Validate blackboard state against schema invariants |
| `liza watch` | Monitor blackboard, alert on anomalies, and auto-checkpoint on circuit-breaker trigger or sprint stall |
| `liza recover-task <task-id>` | Recover by task ID (release claims + remove worktree/branch + recover agent) |
| `liza recover-agent <agent-id>` | Recover by agent ID (release claim + remove worktree + delete agent) |
| `liza release-claim <task-id> [--role R]` | Release claim on a task (manual, granular recovery) |
| `liza sprint-checkpoint` | Create a checkpoint (halt + summary) |
| `liza status` | Show system status |
| `liza pause` / `liza resume` | Pause/resume system |
| `liza stop` / `liza start` | Stop/start system |

**Important:** The supervisor claims tasks *before* starting the Claude agent. This avoids interactive permission prompts in `-p` (non-interactive) mode. Agents receive their assigned task in the bootstrap prompt and should NOT call claim commands directly.

See [Architecture Overview](../specs/architecture/overview.md) for detailed component descriptions.

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
