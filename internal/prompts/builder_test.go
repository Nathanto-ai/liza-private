package prompts

import (
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestBuildBasePrompt(t *testing.T) {
	tests := []struct {
		name           string
		config         BasePromptConfig
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "basic base prompt with all required fields",
			config: BasePromptConfig{
				Role:        "code-coder",
				AgentID:     "coder-1",
				SpecsDir:    "/project/specs",
				ProjectRoot: "/project",
				StatePath:   "/project/.liza/state.yaml",
				GoalDesc:    "Build a web API",
				GoalSpecRef: "specs/vision.md",
			},
			wantContains: []string{
				"You are a Liza code-coder agent",
				"Agent ID: coder-1",
				"ROLE: code-coder",
				"PROJECT_SPECS: /project/specs",
				"PROJECT: /project",
				"BLACKBOARD: /project/.liza/state.yaml",
				"GOAL: Build a web API",
				"APPROVED: use MCP tools with escalated permissions",
				"TWO .liza/ directories exist",
				"~/.liza/ = installed contracts & skills",
				"/project/.liza/ = runtime state & blackboard",
				"You have FULL read access to both .liza/ directories",
				"For READING state: use liza_get MCP tool",
				"For MODIFYING state: use role-specific MCP tools",
				"Prefer MCP tools for atomicity and validation",
				"If a required operation has no MCP tool",
				"Execute all commands immediately",
				"DO NOT ask \"should I proceed?\"",
				"QUERY TOOLS",
				"liza_get",
				"liza_status",
				"liza_validate",
				"COMMUNICATION:",
				"FORBIDDEN:",
				"Do NOT attempt to claim tasks",
				"EXIT CODES:",
				"TIMESTAMPS:",
				"FIRST ACTIONS:",
				"Read the current blackboard state",
				"Read your assigned task's FULL entry",
				"Read the goal spec: specs/vision.md",
				"lessons/agents/README.md",
				"GUARDRAILS.md",
			},
			wantNotContain: []string{
				// Role-specific tools should NOT be in base prompt
				"liza_add_task",
				"liza_submit_for_review",
				"liza_submit_verdict",
				// shared_reference content should NOT be in base prompt
				"TASK STATE MACHINE:",
				"BLACKBOARD FIELDS:",
				"ANOMALY TYPES:",
				"LEASE MODEL:",
				"HELPER COMMANDS",
			},
		},
		{
			name: "role title formatting for multi-word roles",
			config: BasePromptConfig{
				Role:        "code-reviewer",
				AgentID:     "code-reviewer-1",
				SpecsDir:    "/specs",
				ProjectRoot: "/project",
				StatePath:   "/project/.liza/state.yaml",
				GoalDesc:    "Test goal",
				GoalSpecRef: "specs/test.md",
			},
			wantContains: []string{
				"You are a Liza code-reviewer agent",
				"QUERY TOOLS",
			},
		},
		{
			name: "planner role formatting",
			config: BasePromptConfig{
				Role:        "planner",
				AgentID:     "planner-1",
				SpecsDir:    "/specs",
				ProjectRoot: "/project",
				StatePath:   "/project/.liza/state.yaml",
				GoalDesc:    "Test",
				GoalSpecRef: "specs/vision.md",
			},
			wantContains: []string{
				"You are a Liza planner agent",
				"QUERY TOOLS",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := BuildBasePrompt(tt.config)
			if err != nil {
				t.Fatalf("BuildBasePrompt() error: %v", err)
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("BuildBasePrompt() missing expected content:\n%q", want)
				}
			}

			for _, notWant := range tt.wantNotContain {
				if strings.Contains(result, notWant) {
					t.Errorf("BuildBasePrompt() contains unexpected content:\n%q", notWant)
				}
			}
		})
	}
}

func TestBuildPlannerContext(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name         string
		state        *models.State
		config       PlannerContextConfig
		wantContains []string
	}{
		{
			name: "initial planning trigger (no tasks)",
			state: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Tasks = []models.Task{}
				return state
			}(),
			config: PlannerContextConfig{},
			wantContains: []string{
				"=== PLANNING CONTEXT ===",
				"WAKE TRIGGER: INITIAL_PLANNING",
				"SPRINT STATE:",
				"- Total tasks: 0",
				"- Merged: 0",
				"- In progress: 0",
				"- Unclaimed: 0",
				"- Blocked: 0",
				"- Integration failed: 0",
				"- Hypothesis exhausted: 0",
				"- Immediate discoveries: 0",
				"PLANNER COMMANDS:",
				"liza_add_task",
				"liza_supersede_task",
				"liza_wt_delete",
				"This is initial planning",
				"Decompose the goal into tasks",
				"TDD ENFORCEMENT (MANDATORY for code tasks)",
				"Use liza_add_task tool for each task with parameters:",
			},
		},
		{
			name: "blocked tasks trigger",
			state: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Tasks = []models.Task{
					testhelpers.BuildTaskByStatus("task-1", models.TaskStatusBlocked, now),
					testhelpers.BuildTaskByStatus("task-2", models.TaskStatusReady, now),
				}
				return state
			}(),
			config: PlannerContextConfig{},
			wantContains: []string{
				"WAKE TRIGGER: BLOCKED_TASKS",
				"- Total tasks: 2",
				"- Blocked: 1",
				"Tasks are BLOCKED. Analyze and resolve immediately:",
				"Read blocked tasks from blackboard",
			},
		},
		{
			name: "integration failed trigger",
			state: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Tasks = []models.Task{
					testhelpers.BuildTaskByStatus("task-1", models.TaskStatusIntegrationFailed, now),
				}
				return state
			}(),
			config: PlannerContextConfig{},
			wantContains: []string{
				"WAKE TRIGGER: INTEGRATION_FAILED",
				"- Integration failed: 1",
				"Integration failed detected. Act as backstop if needed:",
			},
		},
		{
			name: "hypothesis exhaustion trigger",
			state: func() *models.State {
				state := testhelpers.CreateValidState()
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReady, now)
				task.FailedBy = []string{"coder-1", "coder-2"}
				state.Tasks = []models.Task{task}
				return state
			}(),
			config: PlannerContextConfig{},
			wantContains: []string{
				"WAKE TRIGGER: HYPOTHESIS_EXHAUSTED",
				"- Hypothesis exhausted: 1",
				"Multiple coders failed on same task. Re-evaluate and act NOW:",
			},
		},
		{
			name: "immediate discovery trigger",
			state: func() *models.State {
				state := testhelpers.CreateValidState()
				// Need at least one task to avoid INITIAL_PLANNING trigger
				state.Tasks = []models.Task{
					testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReady, now),
				}
				state.Discovered = []models.Discovery{
					{
						ID:             "disc-1",
						By:             "coder-1",
						During:         "task-1",
						Description:    "Critical bug",
						Severity:       "critical",
						Urgency:        "immediate",
						Recommendation: "Fix immediately",
						Created:        now,
					},
				}
				return state
			}(),
			config: PlannerContextConfig{},
			wantContains: []string{
				"WAKE TRIGGER: IMMEDIATE_DISCOVERY",
				"- Immediate discoveries: 1",
				"Urgent discoveries need immediate action:",
			},
		},
		{
			name: "mixed task statuses (in progress calculation)",
			state: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Tasks = []models.Task{
					testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now),
					testhelpers.BuildTaskByStatus("task-2", models.TaskStatusReadyForReview, now),
					testhelpers.BuildTaskByStatus("task-3", models.TaskStatusApproved, now),
					testhelpers.BuildTaskByStatus("task-4", models.TaskStatusMerged, now),
					testhelpers.BuildTaskByStatus("task-5", models.TaskStatusReady, now),
				}
				return state
			}(),
			config: PlannerContextConfig{},
			wantContains: []string{
				"- Total tasks: 5",
				"- Merged: 1",
				"- In progress: 3", // IMPLEMENTING + READY_FOR_REVIEW + APPROVED
				"- Unclaimed: 1",
			},
		},
		{
			name: "sprint complete trigger",
			state: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Sprint.Scope.Planned = []string{"task-1", "task-2"}
				state.Tasks = []models.Task{
					testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
					testhelpers.BuildTaskByStatus("task-2", models.TaskStatusMerged, now),
				}
				return state
			}(),
			config: PlannerContextConfig{},
			wantContains: []string{
				"WAKE TRIGGER: SPRINT_COMPLETE",
				"- Total tasks: 2",
				"- Merged: 2",
				"All planned sprint tasks have reached terminal state",
				"liza_sprint_checkpoint",
				"liza_update_sprint_metrics",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := BuildPlannerContext(tt.state, tt.config)
			if err != nil {
				t.Fatalf("BuildPlannerContext() error: %v", err)
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("BuildPlannerContext() missing expected content:\n%q", want)
				}
			}
		})
	}
}

// TestBuildPlannerContext_AuditRemediationTrigger is a regression test ensuring
// that REMEDIATE_WITH_TASK audit findings trigger the remediation wake
// instruction and include the findings in the prompt context.
func TestBuildPlannerContext_AuditRemediationTrigger(t *testing.T) {
	now := time.Now().UTC()

	state := testhelpers.CreateValidState()
	state.Tasks = []models.Task{
		testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now),
	}
	state.Sprint.Scope.Planned = []string{"task-1"}
	state.AuditFindings = []models.AuditFinding{
		{
			ID:                "cli-001",
			TaskID:            "task-1",
			Severity:          "MEDIUM",
			Type:              "QUALITY_ISSUE",
			Phase:             "post_merge",
			Classification:    "REMEDIATE_WITH_TASK",
			Evidence:          "uses err == instead of errors.Is()",
			RecommendedAction: "Create task to fix error comparison",
			Created:           now,
			Resolved:          false,
			LinkedTaskID:      "",
		},
	}

	config := PlannerContextConfig{}
	result, err := BuildPlannerContext(state, config)
	if err != nil {
		t.Fatalf("BuildPlannerContext() error: %v", err)
	}

	wantContains := []string{
		"WAKE TRIGGER: AUDIT_REMEDIATION_NEEDED",
		"AUDIT FINDINGS",
		"cli-001",
		"REMEDIATE_WITH_TASK",
		"uses err == instead of errors.Is()",
		"Create task to fix error comparison",
		"unresolved REMEDIATE_WITH_TASK",
		"Do NOT create a sprint checkpoint",
	}

	for _, want := range wantContains {
		if !strings.Contains(result, want) {
			t.Errorf("BuildPlannerContext() missing expected content: %q\n\nFull output:\n%s", want, result)
		}
	}

	// Must NOT contain SPRINT_COMPLETE
	if strings.Contains(result, "SPRINT_COMPLETE") {
		t.Error("BuildPlannerContext() should NOT contain SPRINT_COMPLETE when audit remediation is needed")
	}
}

func TestBuildCoderContext(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name         string
		task         *models.Task
		config       CoderContextConfig
		wantContains []string
	}{
		{
			name: "first iteration without rejection",
			task: func() *models.Task {
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
				task.Description = "Implement authentication"
				task.DoneWhen = "Users can login and logout"
				task.Scope = "Add auth module to backend"
				task.Iteration = 1
				worktree := ".worktrees/task-1"
				task.Worktree = &worktree
				return &task
			}(),
			config: CoderContextConfig{
				ProjectRoot: "/project",
				AgentID:     "coder-1",
			},
			wantContains: []string{
				"=== ASSIGNED TASK ===",
				"TASK ID: task-1",
				"WORKTREE: /project/.worktrees/task-1",
				"ITERATION: 1",
				"DESCRIPTION: Implement authentication",
				"DONE WHEN:",
				"Users can login and logout",
				"SCOPE:",
				"Add auth module to backend",
				"CODER STATE TRANSITIONS:",
				"IMPLEMENTING → READY_FOR_REVIEW",
				"IMPLEMENTING → BLOCKED",
				"CODER TOOLS:",
				"liza_submit_for_review",
				"liza_handoff",
				"liza_mark_blocked",
				"ANOMALY LOGGING:",
				"If context exhaustion is near (~90%)",
				"--- IMPLEMENTATION PHASE ---",
				"The task is already IMPLEMENTING for you",
				"Do NOT run liza claim-task",
				"Work ONLY in the worktree directory: cd /project/.worktrees/task-1",
				"TDD (code tasks): Write tests FIRST",
				"Tests are MANDATORY for code tasks",
				"--- SUBMISSION (MANDATORY - DO NOT SKIP) ---",
				"you MUST submit for review",
				"git -C /project/.worktrees/task-1 rev-parse HEAD",
				"liza_submit_for_review",
				"\"task_id\": \"task-1\"",
				"\"agent_id\": \"coder-1\"",
				"READY_FOR_REVIEW",
			},
		},
		{
			name: "handoff resume context is rendered",
			task: func() *models.Task {
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
				task.Description = "Continue parser hardening"
				task.DoneWhen = "All parser edge cases handled"
				task.Scope = "Parser module"
				task.Iteration = 2
				worktree := ".worktrees/task-1"
				task.Worktree = &worktree
				return &task
			}(),
			config: CoderContextConfig{
				ProjectRoot: "/project",
				AgentID:     "coder-1",
				HandoffNote: &models.HandoffNote{
					Agent:      "coder-1",
					Summary:    "Parser support added for nested objects",
					NextAction: "Add malformed payload tests",
				},
			},
			wantContains: []string{
				"=== HANDOFF RESUME CONTEXT ===",
				"FROM: coder-1",
				"SUMMARY: Parser support added for nested objects",
				"NEXT ACTION: Add malformed payload tests",
			},
		},
		{
			name: "second iteration with rejection feedback",
			task: func() *models.Task {
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
				task.Description = "Add validation"
				task.DoneWhen = "All inputs validated"
				task.Scope = "Add validation layer"
				task.Iteration = 2
				rejectionReason := "Missing edge case tests for empty strings"
				task.RejectionReason = &rejectionReason
				worktree := ".worktrees/task-1"
				task.Worktree = &worktree
				return &task
			}(),
			config: CoderContextConfig{
				ProjectRoot: "/project",
				AgentID:     "coder-1",
			},
			wantContains: []string{
				"ITERATION: 2",
				"=== PRIOR REJECTION FEEDBACK (MUST ADDRESS) ===",
				"Missing edge case tests for empty strings",
			},
		},
		{
			name: "first iteration should not show rejection section",
			task: func() *models.Task {
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
				task.Iteration = 1
				worktree := ".worktrees/task-1"
				task.Worktree = &worktree
				return &task
			}(),
			config: CoderContextConfig{
				ProjectRoot: "/project",
				AgentID:     "coder-1",
			},
			wantContains: []string{
				"ITERATION: 1",
			},
		},
		{
			name: "integration fix mode instructions",
			task: func() *models.Task {
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
				task.Description = "Fix merge conflicts in auth module"
				task.DoneWhen = "Auth module merges cleanly"
				task.Scope = "Resolve conflicts"
				task.Iteration = 1
				task.IntegrationFix = true
				worktree := ".worktrees/task-1"
				task.Worktree = &worktree
				return &task
			}(),
			config: CoderContextConfig{
				ProjectRoot:       "/project",
				AgentID:           "coder-1",
				IntegrationBranch: "integration",
			},
			wantContains: []string{
				"=== INTEGRATION FIX MODE ===",
				"This task previously failed to merge into the integration branch",
				"Your PRIMARY objective is to resolve the merge conflict",
				"INTEGRATION FIX WORKFLOW:",
				"1. ASSESS THE CONFLICT",
				"git status",
				"2. UPDATE LOCAL BRANCH",
				"git fetch /project integration",
				"3. REBASE ONTO INTEGRATION BRANCH",
				"git rebase integration",
				"4. RESOLVE CONFLICTS",
				"conflict markers (<<<<<<<, =======, >>>>>>>)",
				"git add <file>",
				"git rebase --continue",
				"5. IF CONFLICTS ARE UNRESOLVABLE",
				"git rebase --abort",
				"liza_mark_blocked tool",
				"6. VALIDATE IMPLEMENTATION",
				"Run all tests to ensure they still pass",
				"7. SUBMIT FOR REVIEW (SAME AS NORMAL)",
				"Use liza_submit_for_review tool with:",
				"\"task_id\": \"task-1\"",
				"\"agent_id\": \"coder-1\"",
				"CRITICAL: Do not skip the rebase step",
				"Merge conflicts mean the integration branch has diverged",
			},
		},
		{
			name: "integration fix with enhanced rebase instructions",
			task: func() *models.Task {
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
				task.Description = "Fix merge conflicts"
				task.DoneWhen = "Clean merge"
				task.Scope = "Resolve conflicts"
				task.Iteration = 1
				task.IntegrationFix = true
				worktree := ".worktrees/task-1"
				task.Worktree = &worktree
				return &task
			}(),
			config: CoderContextConfig{
				ProjectRoot:       "/project",
				AgentID:           "coder-1",
				IntegrationBranch: "integration",
			},
			wantContains: []string{
				// Existing checks
				"=== INTEGRATION FIX MODE ===",

				// NEW: Critical understanding section
				"CRITICAL UNDERSTANDING - How Rebase Solves Commit History Issues:",
				"Git automatically SKIPS commits that already exist in integration (same commit SHA)",
				"After rebasing onto integration, your branch will contain ONLY commits unique to your task",
				"Example: If your branch has [A, B, C] and integration already has [A, B], rebase leaves only [C]",
				"Verify this with: git log --oneline integration..HEAD",

				// NEW: Verification step after rebase
				"After rebase completes successfully, verify your branch state:",
				"git log --oneline integration..HEAD",
				"Expected result: Should show ONLY commits unique to your task",

				// NEW: Fallback for different SHAs
				"IF AUTO-SKIP DIDN'T WORK",
				"This means they're different commit objects with similar content",
				"git rebase -i integration",
				"Mark out-of-scope commits as \"drop\"",
				"Keep only your task's commits as \"pick\"",

				// NEW: Nuclear option
				"Option A: Mark as blocked if design conflict",
				"Option B: Start completely fresh if commit history is wrong",
				"git diff HEAD > /tmp/my-changes.patch",
				"liza_wt_delete tool",
				"git apply /tmp/my-changes.patch",
			},
		},
		{
			name: "integration fix uses MCP tool syntax not CLI commands",
			task: func() *models.Task {
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusImplementing, now)
				task.IntegrationFix = true
				worktree := ".worktrees/task-1"
				task.Worktree = &worktree
				return &task
			}(),
			config: CoderContextConfig{
				ProjectRoot:       "/project",
				AgentID:           "coder-1",
				IntegrationBranch: "integration",
			},
			wantContains: []string{
				// JSON params syntax for mark-blocked
				"liza_mark_blocked tool",
				"\"task_id\":",
				"\"agent_id\":",
				"\"reason\":",
				"\"questions\":",
				// JSON params syntax for worktree operations
				"liza_wt_delete tool",
				"liza_wt_create tool",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := BuildCoderContext(tt.task, tt.config)
			if err != nil {
				t.Fatalf("BuildCoderContext() error: %v", err)
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("BuildCoderContext() missing expected content:\n%q", want)
				}
			}

			// For first iteration, verify rejection section is NOT present
			if tt.task.Iteration == 1 && !tt.task.IntegrationFix {
				if strings.Contains(result, "PRIOR REJECTION FEEDBACK") {
					t.Errorf("BuildCoderContext() should not contain rejection feedback for iteration 1")
				}
			}

			// Verify integration fix section only appears when IntegrationFix=true
			if tt.task.IntegrationFix {
				if !strings.Contains(result, "=== INTEGRATION FIX MODE ===") {
					t.Errorf("BuildCoderContext() should contain integration fix instructions when IntegrationFix=true")
				}
			} else {
				if strings.Contains(result, "=== INTEGRATION FIX MODE ===") {
					t.Errorf("BuildCoderContext() should NOT contain integration fix instructions when IntegrationFix=false")
				}
			}
		})
	}
}

func TestBuildReviewerContext(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name         string
		task         *models.Task
		config       ReviewerContextConfig
		wantContains []string
	}{
		{
			name: "first iteration review",
			task: func() *models.Task {
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReadyForReview, now)
				task.Description = "Add authentication"
				task.DoneWhen = "Users can login"
				assignedTo := "coder-1"
				task.AssignedTo = &assignedTo
				baseCommit := "abc123"
				task.BaseCommit = &baseCommit
				reviewCommit := "def456"
				task.ReviewCommit = &reviewCommit
				task.Iteration = 1
				worktree := ".worktrees/task-1"
				task.Worktree = &worktree
				return &task
			}(),
			config: ReviewerContextConfig{
				ProjectRoot: "/project",
				AgentID:     "code-reviewer-1",
			},
			wantContains: []string{
				"=== REVIEW TASK ===",
				"TASK ID: task-1",
				"WORKTREE: /project/.worktrees/task-1",
				"BASE COMMIT: abc123",
				"REVIEW COMMIT: def456",
				"AUTHOR: coder-1",
				"ITERATION: 1",
				"DESCRIPTION: Add authentication",
				"DONE WHEN:",
				"Users can login",
				"REVIEWER STATE TRANSITIONS:",
				"REVIEWING → APPROVED",
				"REVIEWING → REJECTED",
				"REVIEWER TOOL:",
				"liza_submit_verdict",
				"ANOMALY LOGGING:",
				"INSTRUCTIONS:",
				"Early drift check",
				"Review ALL changes",
				"Apply the code-review skill",
				"TDD QUALITY (code tasks): Tests must cover done_when criteria",
				"REJECTION FORMAT (if rejecting):",
				"Blockers:",
				"Concerns:",
				"--- VERDICT SUBMISSION (MANDATORY - DO NOT SKIP) ---",
				"You have FULL autonomy to execute liza_submit_verdict",
				"You MUST execute liza_submit_verdict IN THIS SAME SESSION",
				"{\"task_id\": \"task-1\", \"verdict\": \"APPROVED\", \"agent_id\": \"code-reviewer-1\"}",
				"{\"task_id\": \"task-1\", \"verdict\": \"REJECTED\", \"agent_id\": \"code-reviewer-1\"",
			},
		},
		{
			name: "second iteration with prior rejection",
			task: func() *models.Task {
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReadyForReview, now)
				task.Description = "Add validation"
				task.DoneWhen = "All inputs validated"
				assignedTo := "coder-1"
				task.AssignedTo = &assignedTo
				baseCommit := "abc123"
				task.BaseCommit = &baseCommit
				reviewCommit := "def456"
				task.ReviewCommit = &reviewCommit
				task.Iteration = 2
				rejectionReason := "Tests incomplete"
				task.RejectionReason = &rejectionReason
				worktree := ".worktrees/task-1"
				task.Worktree = &worktree
				return &task
			}(),
			config: ReviewerContextConfig{
				ProjectRoot: "/project",
				AgentID:     "code-reviewer-1",
			},
			wantContains: []string{
				"ITERATION: 2",
				"=== PRIOR REJECTION (iteration 1) ===",
				"Tests incomplete",
				"PRIOR FEEDBACK REVIEW (MANDATORY for iteration 2+):",
				"Which prior issues are now RESOLVED?",
				"Which prior issues are STILL PRESENT?",
				"Prior Feedback Status:",
				"- RESOLVED:",
				"- STILL PRESENT:",
				"- PARTIAL:",
			},
		},
		{
			name: "third iteration shows correct prior iteration number",
			task: func() *models.Task {
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReadyForReview, now)
				assignedTo := "coder-1"
				task.AssignedTo = &assignedTo
				baseCommit := "abc123"
				task.BaseCommit = &baseCommit
				reviewCommit := "def456"
				task.ReviewCommit = &reviewCommit
				task.Iteration = 3
				rejectionReason := "Still missing tests"
				task.RejectionReason = &rejectionReason
				worktree := ".worktrees/task-1"
				task.Worktree = &worktree
				return &task
			}(),
			config: ReviewerContextConfig{
				ProjectRoot: "/project",
				AgentID:     "code-reviewer-1",
			},
			wantContains: []string{
				"ITERATION: 3",
				"=== PRIOR REJECTION (iteration 2) ===",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := BuildReviewerContext(tt.task, tt.config)
			if err != nil {
				t.Fatalf("BuildReviewerContext() error: %v", err)
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("BuildReviewerContext() missing expected content:\n%q", want)
				}
			}

			// For first iteration, verify prior rejection section is NOT present
			if tt.task.Iteration == 1 {
				if strings.Contains(result, "PRIOR REJECTION") {
					t.Errorf("BuildReviewerContext() should not contain prior rejection for iteration 1")
				}
				if strings.Contains(result, "PRIOR FEEDBACK REVIEW") {
					t.Errorf("BuildReviewerContext() should not contain prior feedback review for iteration 1")
				}
			}
		})
	}
}

func TestPlannerPromptHasAutonomyGuidance(t *testing.T) {
	state := &models.State{
		Tasks: []models.Task{},
		Goal:  models.Goal{SpecRef: ".liza/specs/goal.md", Description: "Test goal"},
	}

	config := PlannerContextConfig{}
	prompt, err := BuildPlannerContext(state, config)
	if err != nil {
		t.Fatalf("BuildPlannerContext() error: %v", err)
	}

	// Verify autonomy banner present
	if !strings.Contains(prompt, "AUTONOMY NOTICE") {
		t.Error("Expected AUTONOMY NOTICE in prompt")
	}

	// Verify success criteria present
	if !strings.Contains(prompt, "SUCCESS CRITERIA") {
		t.Error("Expected SUCCESS CRITERIA in prompt")
	}

	// Verify examples present
	if !strings.Contains(prompt, "EXPECTED BEHAVIOR EXAMPLES") {
		t.Error("Expected EXPECTED BEHAVIOR EXAMPLES in prompt")
	}

	// Verify execution phase marker
	if !strings.Contains(prompt, "--- PLANNING PHASE COMPLETE. NOW EXECUTE. ---") {
		t.Error("Expected planning phase completion marker in prompt")
	}

	// Verify mandatory execution language
	if !strings.Contains(prompt, "EXECUTION PHASE (MANDATORY - DO NOT SKIP)") {
		t.Error("Expected mandatory execution phase header in prompt")
	}

	// Verify no passive language in instructions (outside of negative examples)
	// Split at "INCORRECT" example to check only the instructional part
	parts := strings.Split(prompt, "❌ INCORRECT (DO NOT DO THIS):")
	if len(parts) < 1 {
		t.Error("Expected to find INCORRECT example section in prompt")
	}
	instructionalPart := parts[0]

	passivePhrases := []string{
		"waiting for approval",
		"once approved",
		"should be added",
		"may need to",
	}
	lowerInstructions := strings.ToLower(instructionalPart)
	for _, phrase := range passivePhrases {
		if strings.Contains(lowerInstructions, phrase) {
			t.Errorf("Prompt should not contain passive phrase in instructions: %s", phrase)
		}
	}
}

func TestBasePromptHasStrongAutonomy(t *testing.T) {
	config := BasePromptConfig{
		Role:        "planner",
		AgentID:     "test-agent",
		SpecsDir:    ".liza/specs",
		ProjectRoot: "/tmp/test",
		StatePath:   ".liza/state.yaml",
		GoalDesc:    "test goal",
		GoalSpecRef: ".liza/specs/goal.md",
	}

	prompt, err := BuildBasePrompt(config)
	if err != nil {
		t.Fatalf("BuildBasePrompt() error: %v", err)
	}

	// Verify strong autonomy language
	requiredPhrases := []string{
		"Execute all commands immediately",
		"Your authority is pre-approved",
		"DO NOT ask \"should I proceed?\"",
	}

	for _, phrase := range requiredPhrases {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("Expected autonomy phrase not found: %s", phrase)
		}
	}
}

func TestReviewerPromptHasAutonomyGuidance(t *testing.T) {
	now := time.Now().UTC()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReadyForReview, now)
	task.Description = "Add authentication"
	task.DoneWhen = "Users can login"
	assignedTo := "coder-1"
	task.AssignedTo = &assignedTo
	baseCommit := "abc123"
	task.BaseCommit = &baseCommit
	reviewCommit := "def456"
	task.ReviewCommit = &reviewCommit
	task.Iteration = 1
	worktree := ".worktrees/task-1"
	task.Worktree = &worktree

	config := ReviewerContextConfig{
		ProjectRoot: "/project",
		AgentID:     "code-reviewer-1",
	}
	prompt, err := BuildReviewerContext(&task, config)
	if err != nil {
		t.Fatalf("BuildReviewerContext() error: %v", err)
	}

	// Verify strong autonomy language — preamble and verdict section
	requiredPhrases := []string{
		// Preamble: tool access assertion
		"YOU HAVE FULL TOOL ACCESS",
		"You CAN and MUST execute Bash commands",
		"Permission prompts are AUTOMATIC",
		// Verdict section: mandatory submission
		"--- VERDICT SUBMISSION (MANDATORY - DO NOT SKIP) ---",
		"You have FULL autonomy to execute liza_submit_verdict",
		"Your authority is PRE-GRANTED",
		"Do NOT wait for permission",
		"Do NOT rationalize waiting",
		"You MUST execute liza_submit_verdict IN THIS SAME SESSION",
		"After submitting verdict, EXIT immediately",
		"FAILURE MODE:",
		"The tool call IS the deliverable",
	}

	for _, phrase := range requiredPhrases {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("Expected autonomy phrase not found in reviewer prompt: %s", phrase)
		}
	}

	// Verify no passive language suggesting waiting
	passivePhrases := []string{
		"waiting for approval",
		"once approved",
		"pending approval",
	}
	for _, phrase := range passivePhrases {
		if strings.Contains(strings.ToLower(prompt), phrase) {
			t.Errorf("Reviewer prompt should not contain passive phrase: %s", phrase)
		}
	}
}

func TestPlannerPromptAutonomyForAllWakeTriggers(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name         string
		state        *models.State
		wantTrigger  string
		wantContains []string
	}{
		{
			name: "BLOCKED_TASKS has immediate action language",
			state: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Tasks = []models.Task{
					testhelpers.BuildTaskByStatus("task-1", models.TaskStatusBlocked, now),
				}
				return state
			}(),
			wantTrigger: "BLOCKED_TASKS",
			wantContains: []string{
				"Analyze and resolve immediately",
				"execute liza_add_task tool NOW",
				"fallback state edit + liza_validate",
				"execute tools NOW",
				"Execute all state-modifying tools in this session",
				"Do NOT defer",
			},
		},
		{
			name: "INTEGRATION_FAILED has backstop behavior language",
			state: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Tasks = []models.Task{
					testhelpers.BuildTaskByStatus("task-1", models.TaskStatusIntegrationFailed, now),
				}
				return state
			}(),
			wantTrigger: "INTEGRATION_FAILED",
			wantContains: []string{
				"Act as backstop if needed",
				"If task already IMPLEMENTING",
				"No action needed — coder is handling it",
				"Exit normally — this is the expected flow",
				"Coders claim INTEGRATION_FAILED tasks directly",
			},
		},
		{
			name: "HYPOTHESIS_EXHAUSTED has immediate action language",
			state: func() *models.State {
				state := testhelpers.CreateValidState()
				task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReady, now)
				task.FailedBy = []string{"coder-1", "coder-2"}
				state.Tasks = []models.Task{task}
				return state
			}(),
			wantTrigger: "HYPOTHESIS_EXHAUSTED",
			wantContains: []string{
				"Re-evaluate and act NOW",
				"execute NOW",
				"update NOW",
				"Execute changes",
				"create them all in this session",
				"All state modifications must be executed before you exit",
			},
		},
		{
			name: "IMMEDIATE_DISCOVERY has immediate action language",
			state: func() *models.State {
				state := testhelpers.CreateValidState()
				state.Tasks = []models.Task{
					testhelpers.BuildTaskByStatus("task-1", models.TaskStatusReady, now),
				}
				state.Discovered = []models.Discovery{
					{
						ID:             "disc-1",
						By:             "coder-1",
						During:         "task-1",
						Description:    "Critical issue",
						Severity:       "critical",
						Urgency:        "immediate",
						Recommendation: "Fix now",
						Created:        now,
					},
				}
				return state
			}(),
			wantTrigger: "IMMEDIATE_DISCOVERY",
			wantContains: []string{
				"Urgent discoveries need immediate action",
				"execute decision NOW",
				"execute liza_add_task tool NOW",
				"fallback state edit + liza_validate",
				"All discovered items must be processed and all tools executed in this session",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := PlannerContextConfig{}
			prompt, err := BuildPlannerContext(tt.state, config)
			if err != nil {
				t.Fatalf("BuildPlannerContext() error: %v", err)
			}

			// Verify correct trigger
			if !strings.Contains(prompt, "WAKE TRIGGER: "+tt.wantTrigger) {
				t.Errorf("Expected wake trigger %s not found", tt.wantTrigger)
			}

			// Verify all required action-oriented phrases
			for _, phrase := range tt.wantContains {
				if !strings.Contains(prompt, phrase) {
					t.Errorf("Missing expected action-oriented phrase: %s", phrase)
				}
			}
		})
	}
}
