package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liza-mas/liza/internal/identity"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/pipeline"
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

// Parent delete command

var deleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete agents or tasks from the state database",
	Long:  `Delete agents that crashed or tasks that are no longer needed.`,
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

// resolveOrchestratorID resolves the orchestrator agent ID from flag, env var,
// or workspace state (the registered orchestrator). Used by commands that default
// to the orchestrator identity when no explicit agent ID is provided.
func resolveOrchestratorID(cmd *cobra.Command) (string, error) {
	flagValue, _ := cmd.Flags().GetString("agent-id")
	agentID, _ := identity.Resolve(identity.Config{
		FlagValue: flagValue,
		Required:  false,
	})
	if agentID != "" {
		return agentID, nil
	}

	projectRoot, err := requireProjectRoot()
	if err != nil {
		return "", err
	}

	// Load resolver for type-based orchestrator resolution.
	// If loading fails, pass nil to fall back to literal role-name match.
	var resolver *pipeline.Resolver
	if cfg, loadErr := pipeline.LoadFrozen(projectRoot); loadErr == nil {
		resolver = pipeline.NewResolver(cfg)
	}

	lp := paths.New(projectRoot)
	resolved, err := ops.ResolveOrchestratorFromState(lp.StatePath(), resolver)
	if err != nil {
		return "", fmt.Errorf("--agent-id not provided and auto-resolution failed: %w", err)
	}
	return resolved, nil
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

// defaultPipelineConfigPath returns ~/.liza/pipeline.yaml if it exists,
// or empty string otherwise (no global setup, or home dir unresolvable).
func defaultPipelineConfigPath() string {
	globalDir, err := paths.GlobalLizaDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(globalDir, "pipeline.yaml")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func init() {
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(validateSpecCmd)

	// Global flags
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "verbose output")
}

// addAgentIDFlag registers --agent-id on a specific command.
func addAgentIDFlag(cmd *cobra.Command) {
	cmd.Flags().String("agent-id", "", "agent identifier (overrides LIZA_AGENT_ID env var)")
}

// addChangedByFlag registers --changed-by on a specific command.
func addChangedByFlag(cmd *cobra.Command) {
	cmd.Flags().String("changed-by", "", "identifier for audit trail (overrides LIZA_AGENT_ID env var, defaults to 'human')")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
