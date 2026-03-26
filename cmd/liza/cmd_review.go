package main

import (
	"github.com/liza-mas/liza/internal/commands"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/roles"
	"github.com/spf13/cobra"
)

var submitForReviewCmd = &cobra.Command{
	Use:   "submit-for-review <task-id> <commit-sha>",
	Short: "Submit a task for review",
	Long: `Validate a task worktree commit and submit it for review.

Used by doer agents to submit completed work for review.

Requirements:
  - Agent ID must be provided (via --agent-id flag or LIZA_AGENT_ID env var)
  - Task must be in an executing status (resolved from pipeline config)
  - Task must be assigned to the submitting agent
  - <commit-sha> must exactly match current worktree HEAD before rebase

Updates:
  - status = role-pair's submitted status (e.g. CODE_READY_FOR_REVIEW, CODING_PLAN_TO_REVIEW)
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
		if err := commands.RequireRole(agentID, commands.MutationRoles["submit-for-review"]...); err != nil {
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
	Long: `Atomically initiate handoff when a doer agent is nearing context exhaustion.

Requirements:
  - Agent ID must be provided (via --agent-id flag or LIZA_AGENT_ID env var)
  - Task must be in an executing status (resolved from pipeline config)
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
		if err := commands.RequireRole(agentID, commands.MutationRoles["handoff"]...); err != nil {
			return err
		}

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.HandoffCommand(projectRoot, &ops.HandoffInput{
			TaskID:     taskID,
			Summary:    summary,
			NextAction: nextAction,
			AgentID:    agentID,
		})
	},
}

var submitVerdictCmd = &cobra.Command{
	Use:   "submit-verdict <task-id> <APPROVED|REJECTED> [rejection-reason]",
	Short: "Submit a review verdict",
	Long: `Atomically submit a review verdict (APPROVED or REJECTED) for a task.

Used by reviewer agents to approve or reject work.

Requirements:
  - Agent ID must be provided (via --agent-id flag or LIZA_AGENT_ID env var)
  - Task must be in a reviewing status (resolved from pipeline config)
  - For REJECTED verdicts, a rejection reason is required

For APPROVED verdict:
  - status = role-pair's approved status (e.g. CODE_APPROVED, CODING_PLAN_APPROVED)
  - approved_by = <agent-id>
  - Clear rejection_reason
  - Clear reviewing_by and review_lease_expires
  - Add history entry with event "approved"

For REJECTED verdict:
  - status = role-pair's rejected status (e.g. CODE_REJECTED, CODING_PLAN_REJECTED)
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
		if err := commands.RequireRole(agentID, commands.MutationRoles["submit-verdict"]...); err != nil {
			return err
		}

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		impact, _ := cmd.Flags().GetString("impact")

		return commands.SubmitVerdictCommand(projectRoot, taskID, verdict, reason, agentID, impact)
	},
}

var releaseClaimCmd = &cobra.Command{
	Use:   "release-claim <task-id>",
	Short: "Manually release claims on a task",
	Long: `Manually release claims on a task (doer, reviewer, or both).

Used to release task claims manually when needed, such as when an agent
crashes or a lease needs to be freed.

Claim types:
  - reviewer: Release review claim (reviewing_by, review_lease_expires). Works for any reviewer role.
  - doer: Release doer claim (assigned_to, lease_expires) and reset to initial state. Works for any doer role.
  - both: Release both reviewer and doer claims

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
			role = roles.ClaimBoth
		}

		agentID := resolveChangedBy(cmd)

		projectRoot, err := requireProjectRoot()
		if err != nil {
			return err
		}

		return commands.ReleaseClaimCommand(projectRoot, taskID, role, force, reason, agentID)
	},
}

func init() {
	rootCmd.AddCommand(submitForReviewCmd)
	rootCmd.AddCommand(handoffCmd)
	rootCmd.AddCommand(submitVerdictCmd)
	rootCmd.AddCommand(releaseClaimCmd)

	addAgentIDFlag(submitForReviewCmd)
	addAgentIDFlag(handoffCmd)
	addAgentIDFlag(submitVerdictCmd)
	addChangedByFlag(releaseClaimCmd)

	// Submit-verdict flags
	submitVerdictCmd.Flags().String("impact", "", "impact classification (standard, significant, architecture)")

	// Release-claim command flags
	releaseClaimCmd.Flags().String("role", roles.ClaimReviewer, "claim type to release (doer, reviewer, both)")
	releaseClaimCmd.Flags().Bool("full", false, "release both doer and reviewer claims (alias for --role both)")
	releaseClaimCmd.Flags().Bool("force", false, "force release even if lease is still valid")
	releaseClaimCmd.Flags().String("reason", "manual release", "reason for releasing the claim")
}
