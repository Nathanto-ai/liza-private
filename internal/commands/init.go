// Package commands implements Liza CLI commands.
package commands

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/embedded"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/specvalidate"
)

// InitCommand initializes a new Liza workspace.
// It creates the .liza directory structure, generates initial state.yaml,
// validates the spec file exists, and creates the integration branch.
//
// Prerequisite: 'liza setup' must have been run to populate ~/.liza/.
// The stdin parameter allows for injected input in tests; pass os.Stdin for CLI usage.
func InitCommand(description string, specRef string, stdin io.Reader) error {
	if stdin == nil {
		stdin = os.Stdin
	}
	// Get project paths
	lizaPaths, err := paths.LizaPathsFromGit()
	if err != nil {
		return fmt.Errorf("failed to setup paths: %w", err)
	}

	// Validate .liza doesn't already exist
	if _, err := os.Stat(lizaPaths.LizaDir()); !os.IsNotExist(err) {
		return fmt.Errorf(".liza already exists at %s, remove or use existing", lizaPaths.LizaDir())
	}

	// Resolve spec file relative to cwd (where user ran the command), not project root
	specPath, err := filepath.Abs(specRef)
	if err != nil {
		return fmt.Errorf("failed to resolve spec path: %w", err)
	}
	if _, err := os.Stat(specPath); os.IsNotExist(err) {
		return fmt.Errorf("spec file does not exist: %s\nCreate spec document first. See templates/vision-template.md", specRef)
	}

	// Validate spec content against required sections
	specContent, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("failed to read spec file %s: %w", specRef, err)
	}
	specType := specvalidate.InferSpecType(specRef)
	result := specvalidate.ValidateSpecFile(string(specContent), specType)
	if len(result.Warnings) > 0 {
		for _, w := range result.Warnings {
			fmt.Fprintf(os.Stderr, "WARNING: %s\n", w)
		}
	}
	if !result.Valid {
		return fmt.Errorf("spec validation failed (detected type %q): missing sections: %v\nFix the spec or use 'liza validate-spec' to check", specType, result.Missing)
	}

	// Validate global config exists (liza setup must have been run)
	globalDir, err := paths.GlobalLizaDir()
	if err != nil {
		return fmt.Errorf("failed to determine global config path: %w", err)
	}
	globalCoreFile := filepath.Join(globalDir, "CORE.md")
	if _, err := os.Stat(globalCoreFile); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("global config not found at %s\nRun 'liza setup' first to install contracts and skills", globalDir)
		}
		return fmt.Errorf("cannot access global config at %s: %w\nCheck permissions on %s", globalCoreFile, err, globalDir)
	}

	// Create directory structure
	if err := os.MkdirAll(lizaPaths.LizaDir(), 0755); err != nil {
		return fmt.Errorf("failed to create .liza directory: %w", err)
	}

	archiveDir := lizaPaths.ArchiveDir()
	if err := os.Mkdir(archiveDir, 0755); err != nil {
		return fmt.Errorf("failed to create archive directory: %w", err)
	}

	cleanupInit := func() {
		os.RemoveAll(lizaPaths.LizaDir())
	}

	// Write/merge Claude Code settings to .claude/
	// This is non-fatal - if it fails, just warn
	// Note: This may prompt user for input if settings file exists
	if err := embedded.WriteClaudeSettings(lizaPaths.ProjectRoot(), stdin); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write claude-settings.json: %v\n", err)
	}

	// Write/merge MCP server configuration to .mcp.json
	// This is non-fatal - if it fails, just warn
	// Note: This may prompt user for input if settings file exists
	if err := embedded.WriteMCPSettings(lizaPaths.ProjectRoot(), stdin); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write .mcp.json: %v\n", err)
	}

	// Create contract symlinks pointing to the global ~/.liza/CORE.md.
	contractTarget := filepath.Join(globalDir, "CORE.md")
	var reader *bufio.Reader
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md"} {
		linkPath := filepath.Join(lizaPaths.ProjectRoot(), name)

		fi, lstatErr := os.Lstat(linkPath)
		if lstatErr != nil {
			if !os.IsNotExist(lstatErr) {
				fmt.Fprintf(os.Stderr, "Warning: cannot stat %s: %v\n", name, lstatErr)
				continue
			}
			// File doesn't exist — fall through to create symlink.
		} else {
			// Already a correct symlink — nothing to do.
			if fi.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(linkPath)
				if err == nil && target == contractTarget {
					continue
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: cannot read symlink %s: %v\n", name, err)
				}
			}

			// Exists but is not the correct symlink — ask permission.
			if reader == nil {
				reader = bufio.NewReader(stdin)
			}
			fmt.Fprintf(os.Stderr, "Warning: %s already exists but does not point to %s.\n", name, contractTarget)
			fmt.Fprintf(os.Stderr, "Without this symlink, liza agents will not use liza's contracts.\n")
			fmt.Fprintf(os.Stderr, "Overwrite %s with symlink to %s? (y/n): ", name, contractTarget)

			response, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to read input, skipping %s\n", name)
				continue
			}
			response = strings.TrimSpace(strings.ToLower(response))
			if response != "y" && response != "yes" {
				continue
			}

			if err := os.Remove(linkPath); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to remove existing %s: %v\n", name, err)
				continue
			}
		}

		if err := os.Symlink(contractTarget, linkPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create %s symlink: %v\n", name, err)
		}
	}

	// Create .github/copilot-instructions.md symlink for Copilot CLI contract loading.
	// Copilot CLI reads custom instructions from .github/copilot-instructions.md.
	copilotDir := filepath.Join(lizaPaths.ProjectRoot(), ".github")
	copilotInstructionsLink := filepath.Join(copilotDir, "copilot-instructions.md")
	if fi, err := os.Lstat(copilotInstructionsLink); os.IsNotExist(err) {
		if mkErr := os.MkdirAll(copilotDir, 0755); mkErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create .github directory: %v\n", mkErr)
		} else if symErr := os.Symlink(contractTarget, copilotInstructionsLink); symErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create .github/copilot-instructions.md symlink: %v\n", symErr)
		}
	} else if err == nil {
		// Already exists — check if it's already the correct symlink.
		if fi.Mode()&os.ModeSymlink == 0 {
			fmt.Fprintf(os.Stderr, "Warning: .github/copilot-instructions.md exists but is not a symlink, skipping\n")
		} else if target, readErr := os.Readlink(copilotInstructionsLink); readErr != nil || target != contractTarget {
			fmt.Fprintf(os.Stderr, "Warning: .github/copilot-instructions.md exists but points elsewhere, skipping\n")
		}
	}

	// Write GUARDRAILS.md template to project root (non-fatal, like claude-settings)
	if err := embedded.WriteGuardrails(lizaPaths.ProjectRoot()); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write GUARDRAILS.md: %v\n", err)
	}

	// Generate IDs and timestamps
	timestamp := time.Now().UTC()
	goalID := fmt.Sprintf("goal-%d", timestamp.Unix())

	// Create initial state
	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          goalID,
			Description: description,
			SpecRef:     specPath,
			Created:     timestamp,
			Status:      models.GoalStatusInProgress,
			AlignmentHistory: []models.AlignmentHistory{
				{
					Timestamp: timestamp,
					Event:     "initialization",
					Summary:   "Initial goal. No tasks defined yet.",
				},
			},
		},
		Tasks:       []models.Task{},
		Agents:      make(map[string]models.Agent),
		Discovered:  []models.Discovery{},
		Handoff:     make(map[string]models.HandoffNote),
		HumanNotes:  []models.HumanNote{},
		SpecChanges: []models.SpecChange{},
		Anomalies:   []models.Anomaly{},
		Sprint: models.Sprint{
			ID:      "sprint-1",
			Number:  1,
			GoalRef: goalID,
			Scope: models.SprintScope{
				Planned: []string{},
				Stretch: []string{},
			},
			Timeline: models.SprintTimeline{
				Started:      timestamp,
				Deadline:     time.Time{}, // zero value for null
				CheckpointAt: nil,
				Ended:        nil,
			},
			Status: models.SprintStatusInProgress,
			Metrics: models.SprintMetrics{
				TasksDone:         0,
				TasksInProgress:   0,
				TasksBlocked:      0,
				IterationsTotal:   0,
				ReviewCyclesTotal: 0,
			},
			Retrospective: nil,
		},
		CircuitBreaker: models.CircuitBreaker{
			LastCheck:      time.Time{}, // zero value for null
			Status:         "OK",
			CurrentTrigger: nil,
			History:        []models.CircuitBreakerHistory{},
		},
		Config: models.Config{
			MaxCoderIterations:   10,
			MaxReviewCycles:      5,
			HeartbeatInterval:    60,
			LeaseDuration:        1800,
			CoderPollInterval:    30,
			CoderMaxWait:         1800,
			PlannerPollInterval:  60,
			PlannerMaxWait:       1800,
			ReviewerPollInterval: 30,
			ReviewerMaxWait:      1800,
			AuditorPollInterval:  60,
			AuditorMaxWait:       1800,
			IntegrationBranch:    "integration",
			EscalationWebhook:    nil,
			Mode:                 models.SystemModeRunning,
		},
	}

	// Write state file
	bb := db.For(lizaPaths.StatePath())
	if err := bb.Write(state); err != nil {
		cleanupInit()
		return fmt.Errorf("failed to write state file: %w", err)
	}

	// Create log file
	// Note: Using simple file write for log since it's not managed by blackboard
	logPath := lizaPaths.LogPath()
	logContent := fmt.Sprintf(`- timestamp: %s
  agent: system
  action: initialized
  detail: %s
`, timestamp.Format(time.RFC3339), description)

	if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
		cleanupInit()
		return fmt.Errorf("failed to write log file: %w", err)
	}

	// Create supporting files
	alertsPath := lizaPaths.AlertsLogPath()
	if err := os.WriteFile(alertsPath, []byte{}, 0644); err != nil {
		cleanupInit()
		return fmt.Errorf("failed to create alerts.log: %w", err)
	}

	// Create lock file
	if err := os.WriteFile(lizaPaths.LockPath(), []byte{}, 0644); err != nil {
		cleanupInit()
		return fmt.Errorf("failed to create lock file: %w", err)
	}

	// Ensure .liza/ and .worktrees/ are in .gitignore so runtime
	// artifacts don't participate in review/merge diffs (Issue #4).
	if err := ensureGitignoreEntries(lizaPaths.ProjectRoot()); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to update .gitignore: %v\n", err)
	}

	// Create integration branch if it doesn't exist
	if err := createIntegrationBranch(); err != nil {
		// Don't fail the entire init if branch creation fails
		// Just log the error - this is what bash version does
		fmt.Fprintf(os.Stderr, "Warning: failed to create integration branch: %v\n", err)
	}

	fmt.Printf("Liza initialized at %s\n", lizaPaths.LizaDir())
	fmt.Println("Integration branch: integration")
	fmt.Println("\nNote: MCP tools and personal permissions belong in ~/.claude/settings.json (global).")
	fmt.Println("See: contracts/contract-activation.md § Global settings")

	return nil
}

// createIntegrationBranch creates the integration branch from the current
// branch's HEAD. It resolves the current branch name explicitly so the
// integration branch is always based on the correct commit, even if HEAD
// later moves (e.g., in a worktree or detached state).
func createIntegrationBranch() error {
	// Check if integration branch exists
	cmd := exec.Command("git", "rev-parse", "--verify", "integration")
	if err := cmd.Run(); err == nil {
		// Branch already exists
		return nil
	}

	// Resolve the current branch name to use as base
	cmd = exec.Command("git", "symbolic-ref", "--short", "HEAD")
	branchOut, err := cmd.Output()
	if err != nil {
		// Detached HEAD or error — fall back to HEAD
		cmd = exec.Command("git", "branch", "integration", "HEAD")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git branch failed: %w: %s", err, string(output))
		}
		return nil
	}

	baseBranch := strings.TrimSpace(string(branchOut))

	// Create integration branch from the resolved branch tip
	cmd = exec.Command("git", "branch", "integration", baseBranch)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git branch failed: %w: %s", err, string(output))
	}

	return nil
}

// ensureGitignoreEntries ensures .liza/ and .worktrees/ are listed in
// the project's .gitignore so runtime artifacts stay out of version control.
// It creates the file if it doesn't exist and appends only missing entries.
func ensureGitignoreEntries(projectRoot string) error {
	gitignorePath := filepath.Join(projectRoot, ".gitignore")

	required := []string{".liza/", ".worktrees/"}

	existing := make(map[string]bool)
	content, err := os.ReadFile(gitignorePath)
	if err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			existing[strings.TrimSpace(line)] = true
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	var toAdd []string
	for _, entry := range required {
		if !existing[entry] {
			toAdd = append(toAdd, entry)
		}
	}

	if len(toAdd) == 0 {
		return nil
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// If file exists and doesn't end with newline, add one first
	if len(content) > 0 && content[len(content)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	for _, entry := range toAdd {
		if _, err := f.WriteString(entry + "\n"); err != nil {
			return err
		}
	}

	return nil
}
