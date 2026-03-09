package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/liza-mas/liza/internal/agent"
	"github.com/liza-mas/liza/internal/commands"
	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/identity"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/roles"
	"github.com/spf13/cobra"
)

var (
	// Version information (set via ldflags during build)
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "liza",
	Short: "Liza - Multi-agent task execution system",
	Long: `Liza is a multi-agent task execution system that uses a YAML-based
"blackboard" pattern with file locking for state management, git worktrees
for task isolation, and agent supervisors with restart logic.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("liza version %s\n", Version)
		fmt.Printf("  commit: %s\n", GitCommit)
		fmt.Printf("  built:  %s\n", BuildDate)
	},
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "One-time global setup of Liza contracts and skills",
	Long: `Write Liza contracts and skills to ~/.liza/ for global access.

This is a one-time setup step that populates the global config directory.
Contracts are written flat (e.g., ~/.liza/CORE.md) and skills are written
to ~/.liza/skills/.

After running setup, use 'liza init' in each project to create the
project-local blackboard and symlinks.

Use --force to overwrite an existing global config.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		targetDir, err := paths.GlobalLizaDir()
		if err != nil {
			return err
		}
		force, _ := cmd.Flags().GetBool("force")
		return commands.SetupCommand(targetDir, force, os.Stdin)
	},
}

var initCmd = &cobra.Command{
	Use:   "init [description]",
	Short: "Initialize a new Liza workspace",
	Long: `Initialize a new Liza workspace by creating .liza directory structure,
generating initial state.yaml, and setting up the integration branch.

The description argument is required and describes the goal.
The spec file (default: specs/vision.md) must exist before initialization.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		description := args[0]
		specRef, _ := cmd.Flags().GetString("spec")
		return commands.InitCommand(description, specRef, os.Stdin)
	},
}

var validateCmd = &cobra.Command{
	Use:   "validate [state-file]",
	Short: "Validate state.yaml against schema rules",
	Long: `Validate the state.yaml file against all 43+ validation rules including:
- Required fields and task state invariants
- Dependency validation (existence, circularity, MERGED deps for IMPLEMENTING tasks)
- Agent validation (WORKING must have current_task)
- Lease expiry checking with grace periods
- Spec file reference validation
Returns detailed error messages if validation fails.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		statePath := ""
		if len(args) > 0 {
			statePath = args[0]
		} else {
			statePath = filepath.Join(paths.LizaDirName, paths.StateFileName)
		}

		skipSpecCheck, _ := cmd.Flags().GetBool("skip-spec-check")
		err := commands.ValidateCommand(statePath, skipSpecCheck)
		if err != nil {
			return err
		}
		fmt.Println("VALID")
		return nil
	},
}

var validateSpecCmd = &cobra.Command{
	Use:   "validate-spec <spec-file>",
	Short: "Validate a specification file against required sections",
	Long: `Validate a Vision or Delivery spec file against required section rules.

Vision specs must contain: Problem Statement, Target Users, MVP Scope,
Explicit Out of Scope, Success Criteria, Risks and Assumptions.

Delivery specs must contain: Definitions / Glossary, User Stories,
Acceptance Criteria (with Given/When/Then), Data & Interfaces, Constraints,
Verification Plan, Non Goals, Open Questions.

When --type is not specified, the type is auto-detected from the filename:
  *vision* → vision, *delivery* → delivery. Falls back to delivery if unclear.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		specPath := args[0]
		specType, _ := cmd.Flags().GetString("type")
		if specType == "" {
			specType = inferSpecType(specPath)
		}
		err := commands.ValidateSpecCommand(specPath, specType)
		if err != nil {
			return err
		}
		fmt.Println("VALID")
		return nil
	},
}

var wtCreateCmd = &cobra.Command{
	Use:   "wt-create <task-id>",
	Short: "Create a worktree for an IMPLEMENTING task",
	Long: `Create a git worktree for an IMPLEMENTING task from the integration branch.

The worktree is created in .worktrees/<task-id> and a new branch task/<task-id>
is created from the integration branch. The task's base_commit is recorded for
drift tracking.

If the worktree already exists and --fresh is not specified, the command succeeds
without error. With --fresh, any existing worktree is deleted before creating
a new one (useful for task reassignment).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		fresh, _ := cmd.Flags().GetBool("fresh")

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.WtCreateCommand(projectRoot, taskID, fresh)
	},
}

var wtDeleteCmd = &cobra.Command{
	Use:   "wt-delete <task-id>",
	Short: "Delete a worktree for a completed/abandoned task",
	Long: `Delete a git worktree and branch for a task.

For safety, deletion is only allowed for tasks in the following states:
  - BLOCKED: Task is blocked and cannot proceed
  - ABANDONED: Task has been abandoned
  - SUPERSEDED: Task has been superseded by another task
  - MERGED: Task is complete (worktree should already be cleaned)

This prevents accidental destruction of in-progress work. If the task is
IMPLEMENTING or READY_FOR_REVIEW, deletion is not allowed as the coder may be
actively working in the worktree.

The worktree directory and branch are removed, and task.worktree is set to null.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.WtDeleteCommand(projectRoot, taskID)
	},
}

var wtMergeCmd = &cobra.Command{
	Use:   "wt-merge <task-id>",
	Short: "Merge an approved task into the integration branch",
	Long: `Merge an approved task's worktree into the integration branch.

This is the final step in the task lifecycle, integrating completed and approved
work back into the main codebase.

Requirements:
  - Task must be in APPROVED status
  - Agent ID must be provided (via --agent-id flag or LIZA_AGENT_ID env var)
  - Agent must be a code-reviewer role
  - Worktree HEAD must match the task's review_commit

Process:
  1. Validates task status and review_commit
  2. Attempts fast-forward merge (or merge commit if needed)
  3. Handles merge conflicts by marking task as INTEGRATION_FAILED
  4. Runs integration tests if scripts/integration-test.sh exists
  5. On success: cleans up worktree, marks task as MERGED, updates sprint metrics
  6. On failure: preserves worktree for conflict resolution

The worktree and branch are automatically cleaned up after a successful merge.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		agentID, err := requireAgentID(cmd)
		if err != nil {
			return err
		}

		role, err := identity.ExtractRole(agentID)
		if err != nil {
			return err
		}
		if role != roles.RuntimeCodeReviewer {
			return fmt.Errorf("wt-merge requires code-reviewer agent (got: %s)", role)
		}

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.WtMergeCommand(projectRoot, taskID, agentID)
	},
}

var claimTaskCmd = &cobra.Command{
	Use:   "claim-task <task-id> <agent-id>",
	Short: "Claim a task for a coder agent",
	Long: `Claim a task for a coder agent using the three-phase claim pattern.

Supports claiming from multiple source states:
  - READY: normal new claim
  - REJECTED: re-claim (same coder preserves worktree, different coder gets fresh)
  - INTEGRATION_FAILED: any coder can claim (worktree preserved for conflict resolution)

Phase 1: Validate under lock (check status, deps, agent availability)
Phase 2: Handle worktree outside lock (create/preserve/delete as needed)
Phase 3: Re-validate and commit under lock (atomic state update)

This pattern prevents TOCTOU races in multi-agent scenarios.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		agentID := args[1]

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.ClaimTaskCommand(projectRoot, taskID, agentID)
	},
}

var claimReviewCmd = &cobra.Command{
	Use:   "claim-review <agent-id>",
	Short: "Claim the highest-priority reviewable task for a reviewer agent",
	Long: `Claim the highest-priority READY_FOR_REVIEW task for a code-reviewer agent.

Atomically transitions the task to REVIEWING and assigns the reviewer.
The task is selected automatically based on priority ordering.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentID := args[0]

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.ClaimReviewerTaskCommand(projectRoot, agentID)
	},
}

var writeCheckpointCmd = &cobra.Command{
	Use:   "write-checkpoint <task-id>",
	Short: "Write pre-execution checkpoint for a task",
	Long: `Write a pre-execution checkpoint to a task's history.

This must be done before submitting a task for review (submit-for-review).
The checkpoint documents the coder's intent and validation plan.

Requirements:
  - Agent ID must be provided (via --agent-id flag or LIZA_AGENT_ID env var)
  - Task must be in IMPLEMENTING status
  - Task must be assigned to the agent`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		agentID, err := requireAgentID(cmd)
		if err != nil {
			return err
		}

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		intent, _ := cmd.Flags().GetString("intent")
		validationPlan, _ := cmd.Flags().GetString("validation-plan")
		filesStr, _ := cmd.Flags().GetString("files")

		if intent == "" {
			return fmt.Errorf("--intent is required")
		}
		if validationPlan == "" {
			return fmt.Errorf("--validation-plan is required")
		}
		if filesStr == "" {
			return fmt.Errorf("--files is required (comma-separated file list)")
		}

		input := &ops.WriteCheckpointInput{
			TaskID:         taskID,
			AgentID:        agentID,
			Intent:         intent,
			ValidationPlan: validationPlan,
			FilesToModify:  strings.Split(filesStr, ","),
		}

		if err := ops.WriteCheckpoint(projectRoot, input); err != nil {
			return fmt.Errorf("write checkpoint: %w", err)
		}

		fmt.Printf("CHECKPOINT WRITTEN: %s\n", taskID)
		fmt.Printf("  agent: %s\n", agentID)
		return nil
	},
}

var submitForReviewCmd = &cobra.Command{
	Use:   "submit-for-review <task-id> <commit-sha>",
	Short: "Submit a task for review",
	Long: `Validate a task worktree commit and submit it for review.

Used by coder agents to submit completed work for review.

Requirements:
  - Agent ID must be provided (via --agent-id flag or LIZA_AGENT_ID env var)
  - Task must be in IMPLEMENTING status
  - Task must be assigned to the submitting agent
  - <commit-sha> must exactly match current worktree HEAD before rebase

Updates:
  - status = READY_FOR_REVIEW
  - review_commit = post-rebase worktree HEAD
  - Adds history entry with event "submitted_for_review"`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		commitSHA := args[1]

		agentID, err := requireAgentID(cmd)
		if err != nil {
			return err
		}

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.SubmitForReviewCommand(projectRoot, taskID, commitSHA, agentID)
	},
}

var handoffCmd = &cobra.Command{
	Use:   "handoff <task-id> <summary> <next-action>",
	Short: "Initiate context-exhaustion handoff for a claimed task",
	Long: `Atomically initiate handoff when a coder is nearing context exhaustion.

Requirements:
  - Agent ID must be provided (via --agent-id flag or LIZA_AGENT_ID env var)
  - Task must be in IMPLEMENTING status
  - Task must be assigned to the submitting agent

Updates:
  - task.handoff_pending = true
  - task history appends handoff_initiated event
  - handoff.<task-id> note is recorded with summary and next_action
  - agent status = HANDOFF`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		summary := args[1]
		nextAction := args[2]

		agentID, err := requireAgentID(cmd)
		if err != nil {
			return err
		}

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.HandoffCommand(projectRoot, taskID, summary, nextAction, agentID)
	},
}

var submitVerdictCmd = &cobra.Command{
	Use:   "submit-verdict <task-id> <APPROVED|REJECTED> [rejection-reason]",
	Short: "Submit a review verdict",
	Long: `Atomically submit a review verdict (APPROVED or REJECTED) for a task.

Used by reviewer agents to approve or reject work.

Requirements:
  - Agent ID must be provided (via --agent-id flag or LIZA_AGENT_ID env var)
  - Task must be in READY_FOR_REVIEW status
  - For REJECTED verdicts, a rejection reason is required

For APPROVED verdict:
  - status = APPROVED
  - approved_by = <agent-id>
  - Clear rejection_reason
  - Clear reviewing_by and review_lease_expires
  - Add history entry with event "approved"

For REJECTED verdict:
  - status = REJECTED
  - rejection_reason = <reason>
  - Increment review_cycles_current and review_cycles_total
  - Clear reviewing_by and review_lease_expires
  - Add history entry with event "rejected" and reason`,
	Args: cobra.RangeArgs(2, 3),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		verdict := args[1]
		reason := ""
		if len(args) == 3 {
			reason = args[2]
		}

		agentID, err := requireAgentID(cmd)
		if err != nil {
			return err
		}

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.SubmitVerdictCommand(projectRoot, taskID, verdict, reason, agentID)
	},
}

var submitAuditFindingCmd = &cobra.Command{
	Use:   "submit-audit-finding",
	Short: "Submit an audit finding for a task",
	Long: `Atomically submit an audit finding for a merged task.

Used by auditor agents to record spec mismatches, missing tests,
quality issues, and other observations discovered during code audit.
Classification is assigned by the deterministic supervisor policy.

Required flags:
  --finding-id       Unique ID for this finding
  --task-id          The task this finding applies to
  --severity         HIGH, MEDIUM, or LOW
  --type             Finding type (SPEC_MISMATCH, MISSING_TEST, MISSING_EDGE_CASE, QUALITY_ISSUE, CAPABILITY_MISSING, VERIFICATION_GAP, ARCHITECTURE_DEBT, SYSTEMIC_SPEC_DRIFT)
  --phase            pre_execution, post_execution, or post_merge
  --evidence         Description of what was found

Optional flags:
  --classification       Suggested classification (supervisor assigns final)
  --recommended-action   Suggested remediation
  --spec-reference       Reference to relevant spec section`,
	RunE: func(cmd *cobra.Command, args []string) error {
		findingID, _ := cmd.Flags().GetString("finding-id")
		taskID, _ := cmd.Flags().GetString("task-id")
		severity, _ := cmd.Flags().GetString("severity")
		findingType, _ := cmd.Flags().GetString("type")
		phase, _ := cmd.Flags().GetString("phase")
		classification, _ := cmd.Flags().GetString("classification")
		evidence, _ := cmd.Flags().GetString("evidence")
		recommendedAction, _ := cmd.Flags().GetString("recommended-action")
		specReference, _ := cmd.Flags().GetString("spec-reference")

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.SubmitAuditFindingCommand(projectRoot, ops.AuditFindingInput{
			FindingID:         findingID,
			TaskID:            taskID,
			Severity:          severity,
			Type:              findingType,
			Phase:             phase,
			Classification:    classification,
			Evidence:          evidence,
			RecommendedAction: recommendedAction,
			SpecReference:     specReference,
		})
	},
}

func init() {
	submitAuditFindingCmd.Flags().String("finding-id", "", "unique finding ID (required)")
	submitAuditFindingCmd.Flags().String("task-id", "", "task ID this finding applies to (required)")
	submitAuditFindingCmd.Flags().String("severity", "", "HIGH, MEDIUM, or LOW (required)")
	submitAuditFindingCmd.Flags().String("type", "", "SPEC_MISMATCH, MISSING_TEST, MISSING_EDGE_CASE, QUALITY_ISSUE, CAPABILITY_MISSING, VERIFICATION_GAP, ARCHITECTURE_DEBT, or SYSTEMIC_SPEC_DRIFT (required)")
	submitAuditFindingCmd.Flags().String("phase", "", "pre_execution, post_execution, or post_merge (required)")
	submitAuditFindingCmd.Flags().String("classification", "", "Suggested classification (optional — supervisor assigns final classification)")
	submitAuditFindingCmd.Flags().String("evidence", "", "description of what was found (required)")
	submitAuditFindingCmd.Flags().String("recommended-action", "", "suggested remediation (optional)")
	submitAuditFindingCmd.Flags().String("spec-reference", "", "reference to relevant spec section (optional)")
	_ = submitAuditFindingCmd.MarkFlagRequired("finding-id")
	_ = submitAuditFindingCmd.MarkFlagRequired("task-id")
	_ = submitAuditFindingCmd.MarkFlagRequired("severity")
	_ = submitAuditFindingCmd.MarkFlagRequired("type")
	_ = submitAuditFindingCmd.MarkFlagRequired("phase")
	// classification is now optional — the deterministic supervisor policy assigns it
	_ = submitAuditFindingCmd.MarkFlagRequired("evidence")
}

var markBlockedCmd = &cobra.Command{
	Use:   "mark-blocked <task-id>",
	Short: "Mark a task as BLOCKED due to unresolvable blocker",
	Long: `Mark a task as BLOCKED when work cannot proceed.

Per the blocking protocol (specs/architecture/roles.md), use this when:
  - Spec ambiguity prevents implementation
  - Missing external dependency blocks progress
  - Design conflict discovered that requires rescoping

Requirements:
  - Agent ID must be provided (via --agent-id flag or LIZA_AGENT_ID env var)
  - Task must be in IMPLEMENTING status
  - Only the assigned agent can mark a task as blocked
  - Requires a reason and 1-3 clarifying questions

Effects:
  - status = BLOCKED
  - blocked_reason = <reason>
  - blocked_questions = [<questions>]
  - Clear assigned_to
  - Clear lease_expires
  - Add history entry with event "blocked"
  - Triggers planner wake`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]

		reason, _ := cmd.Flags().GetString("reason")
		questions, _ := cmd.Flags().GetStringSlice("questions")

		agentID, err := requireAgentID(cmd)
		if err != nil {
			return err
		}

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.MarkBlockedCommand(projectRoot, taskID, reason, questions, agentID)
	},
}

var releaseClaimCmd = &cobra.Command{
	Use:   "release-claim <task-id>",
	Short: "Manually release claims on a task",
	Long: `Manually release claims on a task (code-reviewer, coder, or both).

Used to release task claims manually when needed, such as when an agent
crashes or a lease needs to be freed.

Roles:
  - code-reviewer: Release review claim (reviewing_by, review_lease_expires)
  - coder: Release coder claim (assigned_to, lease_expires) and set IMPLEMENTING → READY
  - both: Release both reviewer and coder claims

Safety:
  - By default, refuses to release claims with valid (non-expired) leases
  - Use --force to override lease expiry checks
  - Warns if no claims exist to release

Agent ID for audit trail:
  - Can be specified via --changed-by flag or LIZA_AGENT_ID env var
  - Defaults to "human" if not provided`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		role, _ := cmd.Flags().GetString("role")
		force, _ := cmd.Flags().GetBool("force")
		reason, _ := cmd.Flags().GetString("reason")
		full, _ := cmd.Flags().GetBool("full")

		if full { // --full is an alias for --role both
			role = "both"
		}

		agentID := resolveChangedBy(cmd)

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.ReleaseClaimCommand(projectRoot, taskID, role, force, reason, agentID)
	},
}

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Run circuit breaker pattern detection analysis",
	Long: `Analyze anomalies in the blackboard and detect systemic failure patterns.

Detects the following patterns:
  - retry_cluster: 3+ retry_loops with similar error patterns (ARCHITECTURE_FLAW)
  - debt_accumulation: 3+ trade_offs creating technical debt (SCOPE_FLAW)
  - assumption_cascade: 2+ assumption violations with same assumption (SPEC_FLAW)
  - spec_gap_cluster: 2+ spec ambiguities hitting same spec reference (SPEC_FLAW)
  - workaround_pattern: 2+ workarounds/trade-offs with similar root causes (ARCHITECTURE_FLAW)
  - external_service_outage: 2+ external blockers from same service (EXTERNAL_DEPENDENCY)

If a pattern is detected:
  - Updates circuit_breaker.status to TRIGGERED
  - Generates .liza/circuit_breaker_report.md with evidence
  - Sets sprint.status to CHECKPOINT (equivalent to 'liza checkpoint')
  - Requires human review and resolution

If no patterns are detected:
  - Updates circuit_breaker.status to OK
  - Continues normal operation`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.AnalyzeCommand(projectRoot)
	},
}

var updateSprintMetricsCmd = &cobra.Command{
	Use:   "update-sprint-metrics",
	Short: "Recompute sprint metrics from current state",
	Long: `Recompute sprint metrics by aggregating data from task and agent state.

Metrics computed:
  - tasks_done: Count of terminal tasks (MERGED, ABANDONED, SUPERSEDED)
  - tasks_in_progress: Count of active tasks (IMPLEMENTING, READY_FOR_REVIEW, REJECTED, INTEGRATION_FAILED)
  - tasks_blocked: Count of BLOCKED tasks
  - iterations_total: Sum of iterations_total from all agents
  - review_cycles_total: Sum of review_cycles_total from all tasks
  - review_verdict_approvals: Count of approval verdicts in task history
  - review_verdict_rejections: Count of rejection verdicts in task history
  - review_verdict_count: Total review verdicts
  - review_verdict_approval_rate_percent: Percentage of approvals
  - task_submitted_for_review_count: Count of tasks that have been submitted
  - task_outcome_approval_rate_percent: Percentage of submitted tasks that ended up approved/merged

Warnings:
  - Alerts if review verdict approval rate >95% (suspiciously high)
  - Alerts if task outcome approval rate >95% (suspiciously high)

The metrics are used to track sprint progress and detect quality issues.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.UpdateSprintMetricsCommand(projectRoot)
	},
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Monitor Liza blackboard and alert on conditions",
	Long: `Continuously monitor the Liza blackboard and alert on anomalies.

Runs periodic checks (default: every 10 seconds) for:
  - Expired leases (coder and reviewer)
  - Blocked tasks
  - Orphaned rejected tasks (assigned to inactive agents)
  - Review loops (>=5 cycles)
  - Integration failures
  - Hypothesis exhaustion (failed_by >= 2)
  - Reassigned tasks
  - Approaching limits (8/10 iterations, 3/5 review cycles)
  - Stalled progress (no log activity 30+ min)
  - Stale drafts (>30min old)
  - Immediate discoveries not converted to tasks
  - Circuit breaker anomaly patterns (auto-checkpoints sprint on trigger)
  - State validity
  - Stale checkpoint/pause files

Alerts are written to .liza/alerts.log and printed to stderr.

Press Ctrl+C to stop watching.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		interval, _ := cmd.Flags().GetInt("interval")
		if interval <= 0 {
			return fmt.Errorf("interval must be positive")
		}

		config := commands.WatchConfig{
			ProjectRoot:   projectRoot,
			CheckInterval: time.Duration(interval) * time.Second,
			StateCache:    make(map[string]time.Time),
		}

		return commands.WatchCommand(context.Background(), config)
	},
}

var clearStaleReviewClaimsCmd = &cobra.Command{
	Use:   "clear-stale-review-claims",
	Short: "Clear expired review leases",
	Long: `Find and clear expired review leases on REVIEWING tasks.

When a Code Reviewer crashes mid-review, reviewing_by and review_lease_expires
remain set. This command clears expired claims so other reviewers can claim the task.

Typically called by:
  - Code Reviewer supervisor on startup
  - Periodically by cron or monitoring
  - liza-watch.sh (though watch shouldn't mutate state by default)

Reports the number of claims cleared and logs each cleanup action.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		cleared, err := commands.ClearStaleReviewClaimsCommand(projectRoot)
		if err != nil {
			return err
		}

		if cleared > 0 {
			fmt.Printf("Cleared %d stale review claim(s)\n", cleared)
		} else {
			fmt.Println("No stale review claims found")
		}
		return nil
	},
}

var pauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Pause the Liza system",
	Long: `Pause the Liza system by setting config.mode to PAUSED in state.yaml.

Agents will detect the PAUSED mode and block at their next check. They will
continue sending heartbeats but will not claim new tasks or make progress
until the system is resumed.

This is useful for:
- Making manual adjustments to state.yaml
- Investigating issues
- Coordinated maintenance

Use 'liza resume' to continue normal operation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reason, _ := cmd.Flags().GetString("reason")
		changedBy := resolveChangedBy(cmd)

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.PauseCommand(projectRoot, reason, changedBy)
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the Liza system",
	Long: `Stop the Liza system by setting config.mode to STOPPED in state.yaml.

Agents will detect the STOPPED mode and exit cleanly at their next check.
This provides a graceful shutdown of all agents.

This is different from pause:
- PAUSED: Agents block and wait for resume
- STOPPED: Agents exit (must be restarted manually)

Use this for:
- Ending a work session
- System maintenance requiring agent restart
- Shutting down before system updates`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reason, _ := cmd.Flags().GetString("reason")
		changedBy := resolveChangedBy(cmd)

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.StopCommand(projectRoot, reason, changedBy)
	},
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Liza system from STOPPED mode",
	Long: `Start the Liza system by setting config.mode to RUNNING in state.yaml.

This command transitions from STOPPED back to RUNNING mode.
After starting, you must manually restart agent processes to resume work.

Difference from resume:
- RESUME: For PAUSED or CHECKPOINT states (agents still running)
- START: For STOPPED state (agents have exited)

Use this for:
- Beginning a new work session after stopping
- Restarting the system after maintenance
- Recovering from a graceful shutdown

After running this command, restart agents manually:
  LIZA_AGENT_ID=coder-1 liza agent coder &
  LIZA_AGENT_ID=code-reviewer-1 liza agent code-reviewer &`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reason, _ := cmd.Flags().GetString("reason")
		changedBy := resolveChangedBy(cmd)

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.StartCommand(projectRoot, reason, changedBy)
	},
}

var resumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume the Liza system from PAUSED mode or CHECKPOINT",
	Long: `Resume the Liza system by setting config.mode to RUNNING and sprint.status to IN_PROGRESS.

This command can be used when:
- System is in PAUSED mode (sets mode to RUNNING)
- Sprint is at CHECKPOINT status (sets status to IN_PROGRESS)
- Both (resumes from both states)

Agents will detect the status changes and resume normal operation at their next check.

If the system is STOPPED, agents must be restarted manually - resume
cannot be used to restart stopped agents.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		changedBy := resolveChangedBy(cmd)

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.ResumeCommand(projectRoot, changedBy)
	},
}

var sprintCheckpointCmd = &cobra.Command{
	Use:     "sprint-checkpoint",
	Aliases: []string{"checkpoint"},
	Short:   "Create a checkpoint for the sprint",
	Long: `Create a checkpoint by setting sprint.status to CHECKPOINT in state.yaml.

This pauses all agents and generates a sprint summary report with:
- Current task status and distribution
- Sprint metrics and progress
- Active agents
- Anomalies and circuit breaker status

The sprint summary is written to .liza/sprint_summary.md.

Agents will pause at their next check. After reviewing the summary,
use 'liza resume' to continue the sprint.

This is useful for:
- Sprint review meetings
- Progress assessment
- Decision points (continue vs pivot)
- Coordinated team synchronization`,
	RunE: func(cmd *cobra.Command, args []string) error {
		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.SprintCheckpointCommand(projectRoot)
	},
}

var getCmd = &cobra.Command{
	Use:   "get <query>",
	Short: "Query and get state data",
	Long: `Query and retrieve Liza state data with flexible formatting.

Query Types:
  Field queries:
    config.mode                    - Get a specific field value
    sprint.status                  - Direct field access
    sprint.metrics.tasks_done      - Nested field access
    sprint.elapsed                 - Computed field (time since started)

  Entity queries:
    tasks                          - List all tasks
    tasks <task-id>                - Show specific task
    agents                         - List all agents
    agents <agent-id>              - Show specific agent
    metrics                        - Show sprint metrics
    anomalies                      - List all anomalies
    traceability                   - Show requirements → tasks → tests matrix

  ID shorthand:
    <task-id>                      - Show specific task (any ID format, e.g., task-1, fix-auth-bug)
    <agent-id>                     - Show specific agent (e.g., coder-1, code-reviewer-1, planner-1, auditor-1)

Formats:
  --format json       - JSON output
  --format yaml       - YAML output
  --format table      - Table format (for lists)
  --format value      - Key-value pairs (default for fields)

Examples:
  liza get config.mode
  liza get sprint.elapsed
  liza get tasks --format table
  liza get tasks task-1 --format json
  liza get task-1                  # Shorthand for tasks task-1
  liza get fix-auth-bug            # Shorthand for tasks fix-auth-bug (any task ID)
  liza get coder-1                 # Shorthand for agents coder-1
  liza get code-reviewer-1         # Shorthand for agents code-reviewer-1
  liza get agents --format yaml
  liza get metrics
  liza get anomalies`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, _ := cmd.Flags().GetString("format")

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		opts := commands.InspectOptions{
			Format:      format,
			ProjectRoot: projectRoot,
		}

		result, err := commands.InspectCommand(args, opts)
		if err != nil {
			return err
		}

		cmd.Println(result)
		return nil
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show system and task status at a glance",
	Long: `Display a comprehensive overview of the Liza system state including:
- Goal and sprint progress
- System mode (running, paused, stopped)
- Task distribution and availability
- Active agents and their health
- Planner wake triggers
- Work queue status for each role

Formats:
  --format json       - JSON output
  --format yaml       - YAML output
  (default)           - Dashboard format

Use --detailed to include anomalies and circuit breaker status.

Examples:
  liza status
  liza status --format json
  liza status --format yaml
  liza status --detailed`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		format, _ := cmd.Flags().GetString("format")
		detailed, _ := cmd.Flags().GetBool("detailed")

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		opts := commands.StatusOptions{
			Format:      format,
			Detailed:    detailed,
			ProjectRoot: projectRoot,
		}

		result, err := commands.StatusCommand(opts)
		if err != nil {
			return err
		}

		cmd.Println(result)
		return nil
	},
}

var agentCmd = &cobra.Command{
	Use:   "agent <role> [initial-task-id]",
	Short: "Run agent supervisor loop",
	Long: `Start an agent supervisor for a specific role.

The supervisor:
- Registers the agent with collision detection
- Polls for role-specific work (coder/reviewer/planner/auditor)
- Claims tasks (coder/reviewer only)
- Builds and executes prompts with the specified CLI
- Manages heartbeats to keep lease alive
- Handles restarts on exit code 42
- Loops until work is exhausted or ABORT signal

Roles:
  coder          - Claims and implements tasks
  code-reviewer  - Reviews and approves/rejects tasks
  planner        - Creates and manages task breakdown
  auditor        - Audits code quality, architecture, and compliance

Example:
  # Using --agent-id flag (recommended)
  liza agent coder --agent-id coder-1
  liza agent code-reviewer --agent-id code-reviewer-1 --cli claude
  liza agent planner --agent-id planner-1 --interactive

  # Skip all CLI permission prompts (auto-approve everything)
  liza agent coder --agent-id coder-1 --auto-approve

  # Save agent output to .liza/agent-outputs/
  liza agent coder --agent-id coder-1 --log

  # Using LIZA_AGENT_ID environment variable
  LIZA_AGENT_ID=coder-1 liza agent coder
  LIZA_AGENT_ID=code-reviewer-1 liza agent code-reviewer --cli claude

  # Using Copilot CLI backend (default model: gpt-5-mini)
  liza agent coder --agent-id coder-1 --cli copilot --auto-approve

  # Copilot with explicit model override
  liza agent coder --agent-id coder-1 --cli copilot --model claude-opus-4.6`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		role := args[0]

		agentID, err := requireAgentID(cmd)
		if err != nil {
			return err
		}

		if err := identity.ValidateRole(agentID, role); err != nil {
			return err
		}

		initialTask := ""
		if len(args) == 2 {
			initialTask = args[1]
		}

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		if !slices.Contains(roles.AllRuntime(), role) {
			return fmt.Errorf("invalid role: %s (must be one of: %s)", role, strings.Join(roles.AllRuntime(), ", "))
		}

		cliName, _ := cmd.Flags().GetString("cli")
		interactive, _ := cmd.Flags().GetBool("interactive")
		logOutput, _ := cmd.Flags().GetBool("log")
		autoApprove, _ := cmd.Flags().GetBool("auto-approve")
		maxLoops, _ := cmd.Flags().GetInt("max-loops")
		copilotModel, _ := cmd.Flags().GetString("model")

		if !slices.Contains([]string{"claude", "codex", "gemini", "mistral", "kimi", "copilot"}, cliName) {
			return fmt.Errorf("invalid CLI: %s (must be claude, codex, gemini, mistral, kimi, or copilot)", cliName)
		}

		// --model is only valid with --cli copilot
		if copilotModel != "" && cliName != "copilot" {
			return fmt.Errorf("--model is only supported with --cli copilot")
		}

		// --log is incompatible with -i (interactive mode)
		if logOutput && interactive {
			return fmt.Errorf("--log is incompatible with -i (interactive mode)")
		}

		specsDir := os.Getenv("LIZA_SPECS")
		if specsDir == "" {
			specsDir = filepath.Join(projectRoot, "specs")
		}

		// Set up paths for agent outputs if --log is enabled
		var outputsDir string
		if logOutput {
			lizaPaths := paths.New(projectRoot)
			outputsDir = lizaPaths.AgentOutputsDir()
		}

		// Resolve Copilot model configuration if using copilot backend
		var executor *agent.DefaultCLIExecutor
		if cliName == "copilot" {
			statePath := filepath.Join(projectRoot, ".liza", "state.yaml")
			cfg := models.Config{}
			if bb := db.For(statePath); bb != nil {
				if state, readErr := bb.Read(); readErr == nil {
					cfg = state.Config
				}
			}
			copilotCfg, resolveErr := agent.ResolveCopilotModel(cfg, copilotModel)
			if resolveErr != nil {
				return resolveErr
			}
			if copilotCfg.WasFallback {
				fmt.Fprintf(os.Stderr, "Note: Copilot default model %q unavailable, using fallback %q\n",
					cfg.CopilotDefaultModel, copilotCfg.Model)
			}
			executor = agent.NewDefaultCLIExecutorWithCopilot(outputsDir, copilotCfg)
		} else {
			executor = agent.NewDefaultCLIExecutor(outputsDir)
		}
		executor.SetRole(role)

		config := agent.SupervisorConfig{
			AgentID:      agentID,
			Role:         role,
			ProjectRoot:  projectRoot,
			StatePath:    filepath.Join(projectRoot, ".liza", "state.yaml"),
			LogPath:      filepath.Join(projectRoot, ".liza", "log.yaml"),
			SpecsDir:     specsDir,
			CLIName:      cliName,
			Interactive:  interactive,
			AutoApprove:  autoApprove,
			InitialTask:  initialTask,
			Executor:     executor,
			MaxLoops:     maxLoops,
			CopilotModel: copilotModel,
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return agent.RunSupervisor(ctx, config)
	},
}

var addTaskCmd = &cobra.Command{
	Use:   "add-task",
	Short: "Add a new task to the state",
	Long: `Add a new task to state.yaml with the specified properties.

Task details can be provided via CLI flags or loaded from a YAML file using --file.
When using --file, CLI flags can override specific fields from the file.

Updates sprint.scope.planned, goal.alignment_history, and logs the action.
Runs validation after adding the task.

Example YAML file format:
  id: task-1
  description: Implement feature X
  spec_ref: specs/vision.md
  done_when: Feature X is implemented and tested
  scope: Add feature X to the codebase
  priority: 1
  depends_on:
    - task-0`,
	RunE: func(cmd *cobra.Command, args []string) error {
		statePath, _ := cmd.Flags().GetString("state")
		logPath, _ := cmd.Flags().GetString("log")

		if statePath == "" && logPath == "" {
			statePath = filepath.Join(paths.LizaDirName, paths.StateFileName)
			logPath = filepath.Join(paths.LizaDirName, paths.LogFileName)
		} else if statePath != "" && logPath == "" {
			return fmt.Errorf("if --state is provided, --log must also be provided")
		} else if statePath == "" && logPath != "" {
			return fmt.Errorf("if --log is provided, --state must also be provided")
		}

		filePath, _ := cmd.Flags().GetString("file")
		var input *commands.TaskInput

		if filePath != "" {
			var err error
			input, err = commands.LoadTaskInputFromFile(filePath)
			if err != nil {
				return err
			}
		} else {
			input = &commands.TaskInput{}
		}

		if cmd.Flags().Changed("id") {
			input.ID, _ = cmd.Flags().GetString("id")
		}
		if cmd.Flags().Changed("desc") {
			input.Description, _ = cmd.Flags().GetString("desc")
		}
		if cmd.Flags().Changed("spec") {
			specVal, _ := cmd.Flags().GetString("spec")
			absSpec, err := filepath.Abs(specVal)
			if err != nil {
				return fmt.Errorf("failed to resolve spec path: %w", err)
			}
			input.SpecRef = absSpec
		}
		if cmd.Flags().Changed("done") {
			input.DoneWhen, _ = cmd.Flags().GetString("done")
		}
		if cmd.Flags().Changed("scope") {
			input.Scope, _ = cmd.Flags().GetString("scope")
		}
		if cmd.Flags().Changed("priority") {
			input.Priority, _ = cmd.Flags().GetInt("priority")
		}
		if cmd.Flags().Changed("depends") {
			dependsStr, _ := cmd.Flags().GetString("depends")
			if dependsStr != "" {
				input.DependsOn = strings.Split(dependsStr, ",")
			} else {
				input.DependsOn = []string{}
			}
		}
		if cmd.Flags().Changed("type") {
			input.Type, _ = cmd.Flags().GetString("type")
		}
		if cmd.Flags().Changed("acceptance-criteria") {
			acStr, _ := cmd.Flags().GetString("acceptance-criteria")
			if acStr != "" {
				input.AcceptanceCriteria = strings.Split(acStr, ",")
			}
		}
		if cmd.Flags().Changed("verify-commands") {
			vcStr, _ := cmd.Flags().GetString("verify-commands")
			if vcStr != "" {
				input.VerifyCommands = strings.Split(vcStr, ",")
			}
		}
		if cmd.Flags().Changed("requirement-refs") {
			rrStr, _ := cmd.Flags().GetString("requirement-refs")
			if rrStr != "" {
				input.RequirementRefs = strings.Split(rrStr, ",")
			}
		}
		if cmd.Flags().Changed("error-behavior") {
			input.ErrorBehavior, _ = cmd.Flags().GetString("error-behavior")
		}
		if cmd.Flags().Changed("origin-finding-id") {
			input.OriginFindingID, _ = cmd.Flags().GetString("origin-finding-id")
		}

		if input.Priority == 0 {
			input.Priority = 1
		}

		flagValue, _ := cmd.Flags().GetString("agent-id")
		plannerID, _ := identity.Resolve(identity.Config{
			FlagValue:    flagValue,
			DefaultValue: "planner-1",
			Required:     false,
		})

		return commands.AddTaskCommand(statePath, logPath, input, plannerID)
	},
}

var supersedeTaskCmd = &cobra.Command{
	Use:   "supersede-task <task-id> <replacement-task-ids> <rescope-reason>",
	Short: "Mark a task as SUPERSEDED by replacement tasks",
	Long: `Mark a task as SUPERSEDED when it needs to be replaced by new task(s).

Used by planner when rescoping blocked, rejected, or problematic tasks.

Requirements:
  - Task must be in BLOCKED, REJECTED, or READY status
  - At least one replacement task ID must be provided
  - Rescope reason must explain why the task is being superseded

The replacement task IDs should be comma-separated.

Example:
  liza supersede-task task-3 task-4,task-5 "Split into smaller tasks due to complexity"`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		replacementIDsStr := args[1]
		reason := args[2]

		replacementIDs := strings.Split(replacementIDsStr, ",")
		for i := range replacementIDs {
			replacementIDs[i] = strings.TrimSpace(replacementIDs[i])
		}

		flagValue, _ := cmd.Flags().GetString("agent-id")
		agentID, _ := identity.Resolve(identity.Config{
			FlagValue:    flagValue,
			DefaultValue: "planner-1",
			Required:     false,
		})

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.SupersedeTaskCommand(projectRoot, taskID, replacementIDs, reason, agentID)
	},
}

// recover-task command
var recoverTaskCmd = &cobra.Command{
	Use:   "recover-task <task-id>",
	Short: "Recover a task (release claims, remove worktree and branch)",
	Long: `Recover a task by performing full cleanup:

- Release agent claims (coder and/or reviewer)
- Remove git worktree and branch
- Recover the claiming agent from state

Normal mode (no --force): requires the task to exist in state. Refuses if the
claiming agent's PID is still alive.

Force mode (--force): cleans up git artifacts (worktree + branch) even if the
task is not in state. Use this when state is already clean but orphaned git
artifacts remain after a hard crash.

Idempotent: safe to run multiple times.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		force, _ := cmd.Flags().GetBool("force")
		reason, _ := cmd.Flags().GetString("reason")
		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}
		return commands.RecoverTaskCommand(projectRoot, taskID, force, reason)
	},
}

// recover-agent command
var recoverAgentCmd = &cobra.Command{
	Use:   "recover-agent <agent-id>",
	Short: "Recover a crashed agent (release claims, remove worktree, delete agent)",
	Long: `Recover a crashed agent by performing full cleanup:

- Release task claims (IMPLEMENTING → READY for coders, REVIEWING → READY_FOR_REVIEW for reviewers)
- Remove git worktree and branch (coders only)
- Delete agent from state

Idempotent: safe to run multiple times (no error if agent already gone).
By default, refuses to recover agents whose PID is still alive.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentID := args[0]
		force, _ := cmd.Flags().GetBool("force")
		cli, _ := cmd.Flags().GetString("cli")
		reason, _ := cmd.Flags().GetString("reason")
		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}
		return commands.RecoverAgentCommand(projectRoot, agentID, force, cli, reason)
	},
}

// Parent delete command
var deleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete agents or tasks from the state database",
	Long:  `Delete agents that crashed or tasks that are no longer needed.`,
}

// delete agent subcommand
var deleteAgentCmd = &cobra.Command{
	Use:   "agent <agent-id>",
	Short: "Delete an agent from the state database",
	Long: `Remove an agent from the state database.

Useful when an agent has crashed or shutdown uncleanly and needs to be removed
from the system. By default, refuses to delete agents with active leases or
current tasks.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentID := args[0]
		force, _ := cmd.Flags().GetBool("force")
		reason, _ := cmd.Flags().GetString("reason")
		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}
		return commands.DeleteAgentCommand(projectRoot, agentID, force, reason, os.Stdin)
	},
}

// delete task subcommand
var deleteTaskCmd = &cobra.Command{
	Use:   "task <task-id>",
	Short: "Delete a task from the state database",
	Long: `Remove a task from the state database.

Useful for removing tasks that were created but are no longer needed. Tasks
in MERGED state cannot be deleted by default (as they represent integrated work).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		force, _ := cmd.Flags().GetBool("force")
		deleteWorktree, _ := cmd.Flags().GetBool("delete-worktree")
		reason, _ := cmd.Flags().GetString("reason")
		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}
		return commands.DeleteTaskCommand(projectRoot, taskID, force, deleteWorktree, reason, os.Stdin)
	},
}

func requireProjectRoot() (string, error) {
	projectRoot, err := paths.GetProjectRoot()
	if err != nil {
		return "", fmt.Errorf("failed to detect project root: %w", err)
	}
	return projectRoot, nil
}

// inferSpecType auto-detects spec type from filename.
// Files containing "vision" → vision, "delivery" → delivery, otherwise delivery.
func inferSpecType(specPath string) string {
	lower := strings.ToLower(filepath.Base(specPath))
	if strings.Contains(lower, "vision") {
		return "vision"
	}
	return "delivery"
}

func requireAgentID(cmd *cobra.Command) (string, error) {
	flagValue, _ := cmd.Flags().GetString("agent-id")
	agentID, err := identity.Resolve(identity.Config{
		FlagValue: flagValue,
		Required:  true,
	})
	if err != nil {
		return "", fmt.Errorf("agent ID required (use --agent-id flag or LIZA_AGENT_ID env var): %w", err)
	}
	return agentID, nil
}

func resolveChangedBy(cmd *cobra.Command) string {
	flagValue, _ := cmd.Flags().GetString("changed-by")
	changedBy, _ := identity.Resolve(identity.Config{
		FlagValue:    flagValue,
		DefaultValue: "human",
		Required:     false,
	})
	return changedBy
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(addTaskCmd)
	rootCmd.AddCommand(supersedeTaskCmd)
	rootCmd.AddCommand(getCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(claimTaskCmd)
	rootCmd.AddCommand(claimReviewCmd)
	rootCmd.AddCommand(submitForReviewCmd)
	rootCmd.AddCommand(writeCheckpointCmd)
	rootCmd.AddCommand(handoffCmd)
	rootCmd.AddCommand(submitVerdictCmd)
	rootCmd.AddCommand(submitAuditFindingCmd)
	rootCmd.AddCommand(markBlockedCmd)
	rootCmd.AddCommand(releaseClaimCmd)
	rootCmd.AddCommand(wtCreateCmd)
	rootCmd.AddCommand(wtDeleteCmd)
	rootCmd.AddCommand(wtMergeCmd)
	rootCmd.AddCommand(analyzeCmd)
	rootCmd.AddCommand(updateSprintMetricsCmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(clearStaleReviewClaimsCmd)
	rootCmd.AddCommand(pauseCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(resumeCmd)
	rootCmd.AddCommand(sprintCheckpointCmd)
	rootCmd.AddCommand(agentCmd)
	rootCmd.AddCommand(recoverTaskCmd)
	rootCmd.AddCommand(recoverAgentCmd)
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(validateSpecCmd)

	deleteCmd.AddCommand(deleteAgentCmd)
	deleteCmd.AddCommand(deleteTaskCmd)

	// Validate-spec command flags
	validateSpecCmd.Flags().String("type", "", "spec type: vision or delivery (auto-detected from filename if omitted)")

	// Setup command flags
	setupCmd.Flags().Bool("force", false, "overwrite existing global config")

	// Init command flags
	initCmd.Flags().String("spec", "specs/vision.md", "path to goal spec file")

	// Validate command flags
	validateCmd.Flags().Bool("skip-spec-check", false, "skip spec file existence check")

	// Get command flags
	getCmd.Flags().String("format", "", "output format: json, yaml, table, value (default varies by query type)")

	// Status command flags
	statusCmd.Flags().String("format", "", "output format: json, yaml, or dashboard (default)")
	statusCmd.Flags().Bool("detailed", false, "include anomalies and circuit breaker status")

	// Mark-blocked command flags
	markBlockedCmd.Flags().String("reason", "", "reason why the task is blocked (required)")
	markBlockedCmd.Flags().StringSlice("questions", nil, "clarifying questions (1-3 required)")
	markBlockedCmd.Flags().String("agent-id", "", "agent ID marking the task as blocked")
	markBlockedCmd.MarkFlagRequired("reason")
	markBlockedCmd.MarkFlagRequired("questions")

	// Release-claim command flags
	releaseClaimCmd.Flags().String("role", "code-reviewer", "role to release (code-reviewer, coder, both)")
	releaseClaimCmd.Flags().Bool("full", false, "release both reviewer and coder claims (alias for --role both)")
	releaseClaimCmd.Flags().Bool("force", false, "force release even if lease is still valid")
	releaseClaimCmd.Flags().String("reason", "manual release", "reason for releasing the claim")

	// Add-task command flags
	addTaskCmd.Flags().String("file", "", "path to YAML file containing task details")
	addTaskCmd.Flags().String("id", "", "task ID (required unless using --file)")
	addTaskCmd.Flags().String("desc", "", "task description (required unless using --file)")
	addTaskCmd.Flags().String("spec", "", "spec reference (required unless using --file)")
	addTaskCmd.Flags().String("done", "", "done-when criteria (required unless using --file)")
	addTaskCmd.Flags().String("scope", "", "task scope (required unless using --file)")
	addTaskCmd.Flags().Int("priority", 0, "task priority (default: 1, overrides file value)")
	addTaskCmd.Flags().String("depends", "", "comma-separated list of task IDs this task depends on (overrides file value)")
	addTaskCmd.Flags().String("type", "", "task type determining role workflow (default: coding)")
	addTaskCmd.Flags().String("acceptance-criteria", "", "comma-separated acceptance criteria")
	addTaskCmd.Flags().String("verify-commands", "", "comma-separated verification commands")
	addTaskCmd.Flags().String("requirement-refs", "", "comma-separated requirement references (R1,R2,...)")
	addTaskCmd.Flags().String("error-behavior", "", "expected error handling behavior")
	addTaskCmd.Flags().String("origin-finding-id", "", "audit finding ID that originated this task")
	addTaskCmd.Flags().String("state", "", "path to state.yaml (default: .liza/state.yaml)")
	addTaskCmd.Flags().String("log", "", "path to log.yaml (default: .liza/log.yaml)")

	// Note: Required flags are validated in RunE based on whether --file is provided

	// Write-checkpoint command flags
	writeCheckpointCmd.Flags().String("intent", "", "description of what the coder intends to implement (required)")
	writeCheckpointCmd.Flags().String("validation-plan", "", "how the coder plans to validate the work (required)")
	writeCheckpointCmd.Flags().String("files", "", "comma-separated list of files to modify (required)")

	// Wt-create command flags
	wtCreateCmd.Flags().Bool("fresh", false, "delete existing worktree before creating (for task reassignment)")

	// Pause command flags
	pauseCmd.Flags().String("reason", "", "reason for pausing the system")

	// Stop command flags
	stopCmd.Flags().String("reason", "", "reason for stopping the system")

	// Start command flags
	startCmd.Flags().String("reason", "", "optional reason for starting the system")

	// Watch command flags
	watchCmd.Flags().Int("interval", 10, "check interval in seconds")

	// Agent command flags
	agentCmd.Flags().String("cli", "claude", "CLI to use (claude, codex, gemini, mistral, kimi, copilot)")
	agentCmd.Flags().String("model", "", "Copilot model override (only with --cli copilot; e.g. gpt-5-mini, claude-opus-4.6)")
	agentCmd.Flags().BoolP("interactive", "i", false, "Print prompt location, don't execute CLI")
	agentCmd.Flags().Bool("log", false, "Save agent output to .liza/agent-outputs/ (incompatible with -i)")
	agentCmd.Flags().Int("max-loops", 0, "Maximum supervisor loop iterations before self-terminating (0 = unlimited)")
	agentCmd.Flags().Bool("auto-approve", false, "Skip CLI permission prompts (passes --dangerously-skip-permissions / --allow-all-tools to the underlying CLI)")

	// Recover-task command flags
	recoverTaskCmd.Flags().Bool("force", false, "clean up git artifacts even if task is not in state")
	recoverTaskCmd.Flags().String("reason", "task recovery", "reason for recovering the task")

	// Recover-agent command flags
	recoverAgentCmd.Flags().Bool("force", false, "override PID liveness check (refuse by default if process is alive)")
	recoverAgentCmd.Flags().String("cli", "", "respawn the agent after cleanup using this CLI (e.g., claude, codex)")
	recoverAgentCmd.Flags().String("reason", "agent recovery", "reason for recovering the agent")

	// Delete agent command flags
	deleteAgentCmd.Flags().Bool("force", false, "force deletion even if agent has active lease or current task")
	deleteAgentCmd.Flags().String("reason", "manual deletion", "reason for deleting the agent")

	// Delete task command flags
	deleteTaskCmd.Flags().Bool("force", false, "force deletion even if task has dependencies or is in restricted state")
	deleteTaskCmd.Flags().Bool("delete-worktree", false, "also delete the associated git worktree and branch")
	deleteTaskCmd.Flags().String("reason", "manual deletion", "reason for deleting the task")

	// Global flags
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "verbose output")
	rootCmd.PersistentFlags().String("state", "", "path to state.yaml (default: .liza/state.yaml)")
	rootCmd.PersistentFlags().String("agent-id", "", "agent identifier (overrides LIZA_AGENT_ID env var)")
	rootCmd.PersistentFlags().String("changed-by", "", "identifier for audit trail (overrides LIZA_AGENT_ID env var, defaults to 'human')")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
