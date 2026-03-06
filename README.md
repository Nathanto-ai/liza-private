# Liza

A peer-supervised multi-agent coding system (MAS) built on behavioral contracts.

[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/liza-mas/liza)

## Features

- **Behavioral Contract**: 55+ LLM failure modes mapped to specific countermeasures, operating as an explicit state machine with tiered rules
- **Multi-Provider**: Supports claude, codex, kimi, mistral, and gemini CLIs
- **Blackboard Pattern**: All agents read/write to a central `state.yaml` with atomic file locking
- **Git Worktrees**: Each task gets an isolated worktree for parallel development
- **Agent Supervisors**: Long-running processes that claim tasks, execute work, and handle failures
- **State Machine**: Strict task state transitions with 43+ validation rules
- **MCP Server**: Structured API access to Liza operations for agents
- **Code-Enforced Guardrails**: Role boundaries and TDD gates enforced in Go, not just prompts
- **Project Guardrails**: Optional `GUARDRAILS.md` for project-specific constraints using the same Tier 0-3 system
- **Skills System**: 17 composable skill protocols (debugging, code review, testing, architecture, etc.) agents load on demand
- **Multi-Sprint Support**: Sprint numbering, checkpoint summaries, and history across sprints
- **Circuit Breaker**: Pattern detection (loops, repeated failures) triggers automatic sprint checkpoint
- **Crash Recovery**: `recover-agent` and `recover-task` commands for idempotent cleanup after hard crashes
- **Restart Logic**: Agents can request restarts (exit code 42) for incremental work
- **Context Handoff**: Agents hand off with structured notes when approaching context limits
- **Monitoring**: Watch daemon alerts on anomalies (expired leases, blocked tasks, etc.)
- **Agent Log Analysis**: Opt-in logging with token usage, context utilization, and struggle sequence diagnostics
- **Spec Validation**: Automated validation of vision and delivery specs with required section enforcement
- **Auditor Role**: Advisory auditor agents that review completed work and produce structured findings
- **Deterministic Verification**: Command-based verification layer with exit-code-driven PASS/FAIL
- **Runaway Protection**: Budget tracking (iterations, tasks, runtime) and anomaly detection (stagnation, no-diff)
- **Structured Observability**: JSONL event logging for debugging and audit trails

## Requirements

- A supported coding agent CLI: Claude Code, Codex, Kimi, Mistral, or Gemini (see [Provider Compatibility](#provider-compatibility))
- Git 2.38+ (for full worktree support)
- Go 1.25.5+ (only for building from source — pre-built binaries available via `install.sh`)

## Architecture

Liza is a hybrid system. The supervisors that wrap every agent, the state machine, and the validation rules are
deterministic Go code. The agents are LLM-powered. This means critical invariants —
state transitions, role boundaries, merge authority, TDD gates — are enforced
mechanically, not by asking an LLM to please follow rules. Most spec-driven
multi-agent systems are LLM-all-the-way-down: agents coordinating agents, with
compliance dependent on prompt adherence. Liza's mechanical layer cannot
fabricate, cannot skip gates, cannot interpret rules flexibly.

The LLM side is equally differentiated. Where other systems give agents role prompts
and coordination protocols, Liza agents operate under a behavioral contract: 55+ documented
LLM failure modes each mapped to a specific countermeasure, an explicit state machine
with forbidden transitions, and tiered rules that define what degrades gracefully
versus what never bends. Reliability on both sides of the hybrid.

Liza's architecture is made of:
- A behavioral contract
- Agent roles and skills
- A YAML blackboard
- A supervisor wrapping each agent with additional code.
- A template-based prompt builder for every agent
- A Go CLI
- Go MCP tools for the agents to interact with the Liza system and use worktrees
- Markdown files following a convention-over-code principle

Roles aren't composable, Skills are: agents aren't constrained regarding their capabilities by a rigid "Act as a..." prompt
and may use any skill they consider relevant to adapt to the situation.

**Liza has the built-in capability to do things right on the first pass.**

As of today, Liza has 4 roles. More to come: Spec Writer / Spec Reviewer, etc.

```
┌─────────────────────────────────────────────────────────────┐
│                         Human                               │
│   (leads specs, observes terminals, reads blackboard,       │
│               kills agents, pauses system)                  │
└─────────────────────────────────────────────────────────────┘
                              │
          ┌───────────────────┼───────────────────┐
          ▼                   ▼                   ▼
    ┌───────────┐        ┌──────────┐        ┌──────────┐
    │ Planner   │        │  Coder   │        │ Reviewer │
    │           │        │          │        │          │
    │ Decomposes│        │ Claims   │        │ Examines │
    │ goal into │        │ tasks,   │        │ work,    │
    │ tasks,    │        │ iterates │        │ approves │
    │ rescopes  │        │ until    │        │ or       │
    │ on failure│        │ approved │        │ rejects, │
    │           │        │  review  │        │ merges   │
    └─────┬─────┘        └────┬─────┘        └────┬─────┘
          │                   │                   │
          └───────────────────┴───────────────────┘
                              │
                        ┌─────┴─────┐
                        │  Auditor  │  ← advisory role
                        │           │
                        │ Reviews   │
                        │ completed │
                        │ work,     │
                        │ creates   │
                        │ findings  │
                        └─────┬─────┘
                              │
                              ▼
                     ┌─────────────────┐
                     │   .liza/        │
                     │   state.yaml    │  ← blackboard
                     │   log.yaml      │  ← activity history
                     │   alerts.log    │  ← watch daemon output
                     │   archive/      │  ← terminal-state tasks
                     └─────────────────┘
                              │
                              ▼
                     ┌─────────────────┐
                     │  .worktrees/    │
                     │  task-1/        │  ← isolated workspaces
                     │  task-2/        │
                     └─────────────────┘
```

See [Architecture](specs/architecture).

### Task Lifecycle

```
DRAFT → READY → IMPLEMENTING → READY_FOR_REVIEW → REVIEWING → APPROVED → MERGED
                      │ ↑              ↑                ↓          │
                      │ └──────────────┼─────── REJECTED ──┘       │
                      │                │                            ↓
                      ├──> BLOCKED ────┤                 INTEGRATION_FAILED
                      │    ├──> SUPERSEDED
                      │    └──> ABANDONED
                      │
                      ├──> NEEDS_HUMAN_DECISION ──> READY / ABANDONED / SUPERSEDED
                      │
                      └──> READY (release claim)
```

**NEEDS_HUMAN_DECISION**: A safe stop — the system halts when it encounters spec ambiguity, conflicting requirements, or budget/time caps. Not an error state; requires human resolution before the task can proceed.
---

## Getting Started

### Hands-on

- **Pairing**: See [Pairing Guide](docs/USAGE_PAIRING.md) — human-agent collaboration under contract
- **Multi-Agent (Liza)**: See [USAGE](docs/USAGE_MULTI_AGENTS.md), then try the [DEMO](docs/DEMO.md)
- **Reference**: [Configuration](docs/CONFIGURATION.md) · [Recipes](docs/RECIPES.md) · [Troubleshooting](docs/TROUBLESHOOTING.md)

### Deep understanding

Liza is simultaneously a Pairing and Multi-Agent System optimized for thoughtfulness, trust and auditability, leading to faster execution thanks to fewer cycles.
- The contract lives in [contracts/](contracts/). It supports three modes: Pairing (with sub-modes — Autonomous, User Duck, Agent Duck, True Pairing, Spike), MAS, and Subagent (lightweight mode for delegated work).
- The complete [Vision](<specs/build/1 - Vision.md>) of Liza

---

## Why This Exists

It started with a test file that kept getting modified to pass instead of the bug getting fixed. Then the confident "Done!" claims when the verification command hadn't actually run. Then the hour-long debugging spirals of random changes when the agent was clearly stuck but wouldn't say so.

The usual fixes—more detailed prompts, explicit "don't modify tests" instructions, "please verify before claiming success"—worked sometimes. The vigilance tax remained: that background load of never quite trusting what the agent tells you.

The common advice is patience. The next model will be better. I didn't buy it and couldn't wait. Over six months of daily pairing with coding agents, I crafted a behavioral contract to turn them from eager assistants into reliable senior engineering peers.

## The Problem That Won't Fix Itself

Sycophancy isn't a bug in these models. It's a feature that drives adoption. Users prefer tools that say yes, sound confident, don't slow them down with caveats. Engagement metrics reward agreeableness, so that's what gets optimized.

Fast, shallow answers aren't a temporary compromise while providers work on quality. They're the product. Every second of "thinking" costs compute. Every clarifying question risks losing the user to a competitor.

Acing SWE-Bench doesn't transfer to real engineering: follow this git workflow, pause at this gate, don't guess. See [Provider Compatibility](#provider-compatibility)

The incentives don't align with what engineers actually need. Waiting for the next model doesn't change the incentive structure.

## A Different Starting Point

Current agents are capable enough. No need to wait for the next model generation. Not out-of-the-box though.
They need their training incentives counteracted to unleash their latent engineering capabilities.

The typical toolkit—detailed prompts, specification frameworks, coordination systems—addresses process without addressing reliability. Prompts are interpreted flexibly, not followed literally. Frameworks like SpecKit and BMAD structure work and handoffs but assume good-faith execution. Liza starts from the opposite assumption: agents will exhibit predictable failure modes unless specifically constrained not to.

The behavioral contract defines what agents cannot do: guess when they should ask, claim success without validation evidence, modify tests to make bugs pass, spiral through random changes without admitting difficulty, skip the analysis-to-execution gate.

This isn't about making agents try harder like with the Ralph Wiggum technique. It's about removing the behaviors that make them unreliable. 55+ documented LLM failure modes—sycophancy, phantom fixes, scope creep, test corruption, hallucinated completions—each mapped to a specific countermeasure.

The contract operates as an explicit state machine with forbidden transitions, not as suggestions the agent interprets flexibly. Tiered rules define what degrades gracefully under pressure versus what never bends.

Errors caught in specs cost less than errors caught in code. The spec system front-loads understanding so agents don't discover requirements by failing tests. This reinforces the [Cost Gradient](<specs/build/1 - Vision.md>) concept from the contract.

> Quality is the fastest path to real completion.

Claude Opus 4.5 putting the contract philosophy in its own words in its *letter to itself* (a mechanism of the contract):

> **Negative space design**: The contract defines what's forbidden; the shape that remains is where judgment lives. Strict on failure modes, silent on excellence. You can't prescribe good judgment—you can only remove the obstacles to it.

For the full analysis: [Vision](<specs/build/1 - Vision.md>) and [Turning AI Coding Agents into Senior Engineering Peers](https://medium.com/@tangi.vass/turning-ai-coding-agents-into-senior-engineering-peers-c3d178621c9e).

## From Pairing to Peer Supervision

The contract was developed through human-agent pairing. One developer, a couple of agents living in distinct terminals, approval gates at every state change. Over months, the gates became routine. Violations disappeared. Work got delivered as expected.

But the gates are load-bearing. Remove them and the failure modes return.

Multi-agent systems inherit single-agent failure modes and add new ones: agents approve each other's mistakes, drift collectively from the goal, or converge confidently on wrong solutions.

Liza delegates approval to peer agents operating under the same contract. The human observes and provides direction without bottlenecking every approval.

Yes, that's vibe coding—the very thing the original contract was written against. Or more precisely, agentic coding. The difference is the contract makes it work.

**Four pillars hold the system:**

- **Behavioral contracts** discipline individual agents into senior peers. Tier 0 invariants are never violated: no unapproved state changes, no fabrication, no test corruption, no unvalidated success claims.

- **Specification system** externalizes context. Agents are stateless—every restart is a new mind with old artifacts. Specs are those artifacts. Without them, agents rediscover requirements by failing. With them, they read shared understanding and execute.

- **Blackboard coordination** makes state visible. A shared file tracks goals, tasks, assignments, history. Agents claim tasks, update status, hand off work through the blackboard. Humans can observe everything, intervene surgically, or pause the system.

- **External validation** replaces self-certification. Coders cannot mark their own work complete. Reviewers examine and issue binding verdicts. Approval means merge eligibility. Rejection means specific feedback and another iteration.

**Key Mechanisms:**

- **Leases, not just heartbeats.** Agents hold time-bounded leases on tasks. Stale agents' tasks become reclaimable only after lease expires.
- **Commit SHA verification.** Coder records commit SHA when requesting review. Reviewer verifies before examining. No reviewing stale state.
- **Approval-gated merge.** Coders commit to their worktree. The supervisor merges to integration only after Code Reviewer approval. Authority is structural, not advisory.
- **Merge traceability.** Task state records `approved_by` and `merge_commit`. Full audit trail.
- **Hypothesis exhaustion.** Two coders fail the same task? The task framing is wrong—rescope, don't reassign.
- **Rescoping audit trail.** Original task becomes SUPERSEDED with explicit reason. New tasks reference what they replace. No silent rewrites.

The human owns intent and acts as circuit-breaker, not bottleneck. Authority is exercised through a kill switch, not an approval queue.

More at [I Tried to Kill Vibe Coding. I Built Adversarial Vibe Coding. Without the Vibes.](https://medium.com/@tangi.vass/i-tried-to-kill-vibe-coding-i-built-adversarial-vibe-coding-without-the-vibes-bc4a63872440)

---

### Common Commands

```bash
liza setup                                          # One-time global setup
liza init "Project goal" --spec specs/vision.md     # Initialize blackboard
liza add-task --id t1 --desc "..." --spec "..." \
  --done "..." --scope "..."                        # Add tasks
liza agent coder --agent-id coder-1                 # Start agent supervisor
liza validate                                       # Validate state
liza validate-spec specs/delivery.md --type delivery # Validate spec file
liza get tasks                                      # Query tasks
liza status                                         # Dashboard overview
liza watch                                          # Monitor for anomalies
liza pause / liza resume                            # Human intervention
liza stop / liza start                              # System control
liza sprint-checkpoint                              # Sprint checkpoint
liza recover-agent <id>                             # Crash recovery
liza analyze                                        # Circuit breaker analysis
```

## Installation

**Quick install (macOS/Linux):**

```bash
curl -fsSL https://raw.githubusercontent.com/liza-mas/liza/main/install.sh | bash
```

This installs both `liza` (CLI) and `liza-mcp` (MCP server) to `/usr/local/bin`.

**Options:**

```bash
# Specific version
curl -fsSL https://raw.githubusercontent.com/liza-mas/liza/main/install.sh | VERSION=v1.0.0 bash

# Custom directory
curl -fsSL https://raw.githubusercontent.com/liza-mas/liza/main/install.sh | INSTALL_DIR=~/.local/bin bash
```

**From source:**

```bash
git clone https://github.com/liza-mas/liza.git && cd liza
make install
```

**Verify:**

```bash
liza version
```

See [RELEASE.md](RELEASE.md) for maintainer release workflow.

## Status

The contract in Pairing mode is battle-tested for making the **agents write most of the production code (~90%) under human supervision**.

The Multi-Agent mode is an **alpha version** with ongoing refinement. The main limitation to address real projects is
the lack of additional role pairs.

**Planned role pairs:**
- Spec Writer / Spec Reviewer
- Architect / Architecture Reviewer
- Tech Writer / Doc Reviewer
- [Planner] / Plan Reviewer

**Roadmap:**
- Specification phase pipeline (Requirement Planner → Spec Writer → Spec Reviewer) before coding sprints
- Context handoff as blackboard event — structured positive/negative findings on every task completion
- Sprint Analyzer role — steering interface for the human at sprint boundaries
- Deterministic pre/post hooks at role transitions — mechanical checks before spawning agents and before their handoff
- Planner-routed model selection — assign tasks to models based on estimated complexity

### Provider Compatibility

The contract is a capability test. It requires meta-cognitive machinery—the ability to parse instructions as executable specifications, observe state, pause at gates.

| Provider | Classification | Notes |
|----------|----------------|-------|
| Claude Opus 4.x | Fully compatible | Reference provider |
| GPT-5.x-Codex | Fully compatible | Equally capable |
| Kimi 2.5 | Fully compatible | Responsive to tooling feedback |
| Mistral Devstral-2 | Partial | Requires explicit activation and supervision |
| Gemini 2.5 Flash | Incompatible | Architectural limitation—no prompt-level fix |

See [Model Capability Assessment](docs/demo-benchmark/wrap-up.md) for detailed analysis.

## Naming

**Liza** combines two references:

**Lisa Simpson**—the disciplined, systematic counterpoint to Ralph Wiggum. The [Ralph Wiggum technique](https://github.com/anthropics/claude-code/tree/main/plugins/ralph-wiggum) loops agents until they converge through persistence. Lisa makes sure the work is actually right.

**ELIZA**—the 1966 chatbot that demonstrated structured dialogue patterns. Liza is about structured collaboration patterns: explicit states, binding verdicts, auditable transitions.

Liza is not autonomous. She is accountable.

## License

Apache 2.0

## Acknowledgments

The behavioral contract draws on research into LLM failure modes, sycophancy patterns, and code generation failures. The multi-agent design incorporates ideas from:

- **[SpecKit](https://github.com/github/spec-kit)** — Project specification
- **[BMAD Method](https://github.com/bmad-code-org/BMAD-METHOD)** — Role templates and workflow patterns
- **Classical blackboard architecture** — Shared state coordination
- **[Ralph Wiggum technique](https://github.com/anthropics/claude-code/tree/main/plugins/ralph-wiggum)** — Iteration until convergence, validated by an adversarial agent instead of mechanical check or self-declaration
- Stephen Oberther (**[liza-go](https://github.com/smo921/liza-go)**) — Shell to Go CLI migration
- **[CrewAI](https://github.com/crewAIInc/crewAI)'s composable guardrails concept** — Reduced to Liza's convention-over-code pattern.
