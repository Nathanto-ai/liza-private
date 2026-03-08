package commands

import (
	"fmt"
	"slices"
	"strings"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
)

// InspectOptions contains options for the inspect command
type InspectOptions struct {
	Format      string // Output format: json, yaml, table, value
	ProjectRoot string // Project root directory
	Internal    bool   // If true, return structured data for composition (not formatted string)
}

// Validate checks if the inspect options are valid
func (opts *InspectOptions) Validate() error {
	validFormats := []string{"json", "yaml", "table", "value", ""}
	valid := false
	for _, f := range validFormats {
		if opts.Format == f {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid format: %s (must be json, yaml, table, or value)", opts.Format)
	}
	return nil
}

// InspectCommand is the main entry point for liza inspect
// Routes to appropriate handlers based on the query
func InspectCommand(args []string, opts InspectOptions) (string, error) {
	if err := opts.Validate(); err != nil {
		return "", err
	}

	if len(args) == 0 {
		return "", fmt.Errorf("no field or entity specified")
	}

	// Read state
	statePath := paths.New(opts.ProjectRoot).StatePath()
	blackboard := db.For(statePath)
	state, err := blackboard.Read()
	if err != nil {
		return "", fmt.Errorf("failed to read state: %w", err)
	}

	// Parse the query
	query := args[0]

	// Determine if this is a field query or entity query
	// Field queries contain dots (e.g., "config.mode", "sprint.status")
	// Entity queries are single words (e.g., "tasks", "agents", "summary")
	if strings.Contains(query, ".") {
		// Field query - direct or computed
		return handleFieldQuery(state, query, opts)
	}

	// Check if query is a known entity type
	if isKnownEntityType(query) {
		// Entity query
		return handleEntityQuery(state, query, args[1:], opts)
	}

	// Check if query looks like an agent ID pattern
	if isAgentIDPattern(query) {
		// Route to agent handler with the ID
		return handleEntityQuery(state, "agents", []string{query}, opts)
	}

	// Otherwise, assume it's a task ID and try to look it up
	return handleEntityQuery(state, "tasks", []string{query}, opts)
}

// isKnownEntityType returns true if the query is a known entity type
func isKnownEntityType(query string) bool {
	knownTypes := []string{"config", "sprint", "tasks", "agents", "metrics", "anomalies", "traceability"}
	return slices.Contains(knownTypes, query)
}

// isAgentIDPattern returns true if the query looks like an agent ID
func isAgentIDPattern(query string) bool {
	// Agent ID patterns based on role types
	return strings.HasPrefix(query, "coder-") ||
		strings.HasPrefix(query, "code-reviewer-") ||
		strings.HasPrefix(query, "planner-")
}

// handleFieldQuery handles queries for specific fields (direct or computed)
func handleFieldQuery(state *models.State, fieldPath string, opts InspectOptions) (string, error) {
	// Try direct field access first
	value, err := getField(state, fieldPath)
	if err == nil {
		return formatOutput(value, opts.Format)
	}

	// If direct access failed with NotFoundError, try computed field
	if errors.IsNotFound(err) {
		value, err = getComputedField(state, fieldPath)
		if err != nil {
			return "", err
		}
		return formatOutput(value, opts.Format)
	}

	return "", err
}

// handleEntityQuery handles queries for entities (tasks, agents, etc.)
func handleEntityQuery(state *models.State, entity string, args []string, opts InspectOptions) (string, error) {
	switch entity {
	case "config":
		// Show all config fields
		return formatOutput(state.Config, opts.Format)
	case "sprint":
		// Show all sprint fields
		return formatOutput(state.Sprint, opts.Format)
	case "tasks":
		// List all tasks or filter by criteria
		// If there are additional args, treat first arg as task ID
		if len(args) > 0 {
			taskOpts := inspectTasksOptions{Format: opts.Format}
			result, err := inspectTask(state, args[0], taskOpts)
			if err != nil {
				return "", err
			}
			return result.(string), nil
		}
		// Otherwise list all tasks
		taskOpts := inspectTasksOptions{Format: opts.Format}
		result, err := inspectTasks(state, taskOpts)
		if err != nil {
			return "", err
		}
		return result.(string), nil
	case "agents":
		// List all agents or show specific agent
		if len(args) > 0 {
			agentOpts := inspectAgentsOptions{Format: opts.Format}
			result, err := inspectAgent(state, args[0], agentOpts)
			if err != nil {
				return "", err
			}
			return result.(string), nil
		}
		// Otherwise list all agents
		agentOpts := inspectAgentsOptions{Format: opts.Format}
		result, err := inspectAgents(state, agentOpts)
		if err != nil {
			return "", err
		}
		return result.(string), nil
	case "metrics":
		// Show sprint metrics or per-agent metrics
		metricsOpts := inspectMetricsOptions{Format: opts.Format}
		result, err := inspectMetrics(state, metricsOpts)
		if err != nil {
			return "", err
		}
		return result.(string), nil
	case "anomalies":
		// List all anomalies or filter by criteria
		anomaliesOpts := inspectAnomaliesOptions{Format: opts.Format}
		result, err := inspectAnomalies(state, anomaliesOpts)
		if err != nil {
			return "", err
		}
		return result.(string), nil
	case "traceability":
		// Show requirements → tasks → verification traceability matrix
		return inspectTraceability(state, opts.Format)
	default:
		return "", &errors.NotFoundError{Entity: entity}
	}
}

// formatOutput formats the output based on the requested format
func formatOutput(data any, format string) (string, error) {
	switch format {
	case "json":
		return formatJSON(data)
	case "yaml":
		return formatYAML(data)
	case "value":
		return formatValue(data)
	case "table":
		// Table format requires specific structure
		return "", fmt.Errorf("table format requires entity query (e.g., 'liza inspect tasks')")
	default:
		return formatValue(data)
	}
}
