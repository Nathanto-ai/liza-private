package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/liza-mas/liza/internal/db"
	lizaerrors "github.com/liza-mas/liza/internal/errors"
	"github.com/liza-mas/liza/internal/mcp/protocol"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/paths"
)

// Server represents the MCP server
type Server struct {
	projectRoot string
	logPath     string
	bb          *db.Blackboard
	tools       map[string]protocol.Tool
	resources   map[string]protocol.Resource
	handlers    map[string]ToolHandler
}

// ToolHandler is a function that handles tool invocation
type ToolHandler func(params map[string]any) (any, error)

type runTransport interface {
	ReadRequest() (*protocol.JSONRPCRequest, error)
	WriteResponse(resp *protocol.JSONRPCResponse) error
	WriteError(id json.RawMessage, code int, message string, data any) error
}

// NewServer creates a new MCP server
func NewServer(projectRoot, logPath string) *Server {
	s := &Server{
		projectRoot: projectRoot,
		logPath:     logPath,
		bb:          db.For(paths.New(projectRoot).StatePath()),
		tools:       make(map[string]protocol.Tool),
		resources:   make(map[string]protocol.Resource),
		handlers:    make(map[string]ToolHandler),
	}

	s.registerReadOnlyTools()
	s.registerReadOnlyResources()
	s.registerMutationTools()
	s.registerComplexOperations()

	return s
}

// GetCapabilities returns the server capabilities
func (s *Server) GetCapabilities() map[string]any {
	return map[string]any{
		"tools": map[string]any{
			"listChanged": false,
		},
		"resources": map[string]any{
			"subscribe":   false,
			"listChanged": false,
		},
	}
}

// ListTools returns all registered tools
func (s *Server) ListTools() []protocol.Tool {
	tools := make([]protocol.Tool, 0, len(s.tools))
	for _, tool := range s.tools {
		tools = append(tools, tool)
	}
	return tools
}

// GetTool returns a specific tool by name
func (s *Server) GetTool(name string) (protocol.Tool, bool) {
	tool, ok := s.tools[name]
	return tool, ok
}

// GetHandler returns a specific handler by tool name
func (s *Server) GetHandler(name string) (ToolHandler, bool) {
	handler, ok := s.handlers[name]
	return handler, ok
}

// ToolNames returns all registered tool names
func (s *Server) ToolNames() []string {
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	return names
}

// ListResources returns all registered resources
func (s *Server) ListResources() []protocol.Resource {
	resources := make([]protocol.Resource, 0, len(s.resources))
	for _, resource := range s.resources {
		resources = append(resources, resource)
	}
	return resources
}

// HandleRequest handles a JSON-RPC request
func (s *Server) HandleRequest(req *protocol.JSONRPCRequest) *protocol.JSONRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolCall(req)
	case "resources/list":
		return s.handleResourcesList(req)
	case "resources/read":
		return s.handleResourceRead(req)
	default:
		return rpcError(req, protocol.NewMethodNotFoundError(req.Method))
	}
}

// rpcResult builds a successful JSON-RPC response.
func rpcResult(req *protocol.JSONRPCRequest, result any) *protocol.JSONRPCResponse {
	return &protocol.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

// rpcError builds an error JSON-RPC response.
func rpcError(req *protocol.JSONRPCRequest, jerr *protocol.JSONRPCError) *protocol.JSONRPCResponse {
	return &protocol.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: jerr}
}

// handleInitialize handles the initialize request
func (s *Server) handleInitialize(req *protocol.JSONRPCRequest) *protocol.JSONRPCResponse {
	return rpcResult(req, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    s.GetCapabilities(),
		"serverInfo": map[string]any{
			"name":    "liza-mcp",
			"version": Version,
		},
	})
}

// handleToolsList handles the tools/list request
func (s *Server) handleToolsList(req *protocol.JSONRPCRequest) *protocol.JSONRPCResponse {
	return rpcResult(req, map[string]any{"tools": s.ListTools()})
}

// handleToolCall handles the tools/call request
func (s *Server) handleToolCall(req *protocol.JSONRPCRequest) *protocol.JSONRPCResponse {
	params, ok := req.Params.(map[string]any)
	if !ok {
		return rpcError(req, protocol.NewInvalidParamsError("params must be an object"))
	}

	toolName, ok := params["name"].(string)
	if !ok {
		return rpcError(req, protocol.NewInvalidParamsError("name must be a string"))
	}

	handler, ok := s.handlers[toolName]
	if !ok {
		return rpcError(req, protocol.NewError(protocol.NotFound, "Tool not found: "+toolName, nil))
	}

	args, _ := params["arguments"].(map[string]any)
	if args == nil {
		args = make(map[string]any)
	}

	result, err := handler(args)
	if err != nil {
		return rpcError(req, s.classifyError(err))
	}

	return rpcResult(req, result)
}

// handleResourcesList handles the resources/list request
func (s *Server) handleResourcesList(req *protocol.JSONRPCRequest) *protocol.JSONRPCResponse {
	return rpcResult(req, map[string]any{"resources": s.ListResources()})
}

// handleResourceRead handles the resources/read request
func (s *Server) handleResourceRead(req *protocol.JSONRPCRequest) *protocol.JSONRPCResponse {
	params, ok := req.Params.(map[string]any)
	if !ok {
		return rpcError(req, protocol.NewInvalidParamsError("params must be an object"))
	}

	uri, ok := params["uri"].(string)
	if !ok {
		return rpcError(req, protocol.NewInvalidParamsError("uri must be a string"))
	}

	result, err := s.handleResourceReadInternal(uri)
	if err != nil {
		return rpcError(req, s.classifyError(err))
	}

	return rpcResult(req, result)
}

// classifyError converts Go errors to MCP error codes.
// Maps internal error patterns to JSON-RPC error codes for intelligent client handling.
// All branches use sanitized messages — raw err.Error() is never exposed to clients.
func (s *Server) classifyError(err error) *protocol.JSONRPCError {
	// Type-based checks first (preferred)
	var nfe *lizaerrors.NotFoundError
	if errors.As(err, &nfe) {
		return protocol.NewError(protocol.NotFound, "resource not found", nil)
	}
	var postWriteValidationErr *ops.PostWriteValidationError
	if errors.As(err, &postWriteValidationErr) {
		return protocol.NewError(protocol.ValidationError, "validation failed: precondition not met", nil)
	}
	var preconditionErr *ops.PreconditionError
	if errors.As(err, &preconditionErr) {
		return protocol.NewError(protocol.ValidationError, preconditionErr.Reason, nil)
	}

	msg := err.Error()

	// String fallback for errors from external packages (git, etc.)
	if strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist") {
		return protocol.NewError(protocol.NotFound, "resource not found", nil)
	}

	// Lock timeout errors
	if strings.Contains(msg, "lock") && (strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out")) {
		return protocol.NewError(protocol.LockTimeout, "lock acquisition timed out", nil)
	}

	// Race condition errors
	if strings.Contains(msg, "race condition") || strings.Contains(msg, "changed concurrently") {
		return protocol.NewError(protocol.RaceCondition, "state changed concurrently, retry", nil)
	}

	// Validation errors (status checks, preconditions)
	if strings.Contains(msg, "not IMPLEMENTING") || strings.Contains(msg, "not REVIEWING") || strings.Contains(msg, "not READY_FOR_REVIEW") ||
		strings.Contains(msg, "not APPROVED") || strings.Contains(msg, "must be") ||
		strings.Contains(msg, "is required") || strings.Contains(msg, "invalid task ID") ||
		strings.Contains(msg, "validation failed") ||
		strings.Contains(msg, "must include") || strings.Contains(msg, "mandatory") {
		return protocol.NewError(protocol.ValidationError, "validation failed: precondition not met", nil)
	}

	// Default: internal error without leaking implementation details
	return protocol.NewError(protocol.InternalError, "internal error", nil)
}

// handleNotification processes JSON-RPC notifications (no response sent).
// Per JSON-RPC 2.0 spec, notifications MUST NOT receive a response.
func (s *Server) handleNotification(req *protocol.JSONRPCRequest) {
	// Known MCP notifications — silently acknowledge
	switch req.Method {
	case "notifications/initialized", "notifications/cancelled":
		// Expected lifecycle notifications, no action needed
	default:
		// Unknown notification — log but don't error
		fmt.Fprintf(io.Discard, "unknown notification: %s\n", req.Method)
	}
}

// registerTool registers a tool with its handler
func (s *Server) registerTool(tool protocol.Tool, handler ToolHandler) {
	s.tools[tool.Name] = tool
	s.handlers[tool.Name] = handler
}

// registerResource registers a resource
func (s *Server) registerResource(resource protocol.Resource) {
	s.resources[resource.URI] = resource
}

// Run starts the MCP server with stdio transport
func (s *Server) Run() error {
	return s.runWithTransport(protocol.NewStdioTransport())
}

func (s *Server) runWithTransport(transport runTransport) error {
	for {
		req, err := transport.ReadRequest()
		if err != nil {
			// EOF means client disconnected, exit gracefully
			if errors.Is(err, io.EOF) || err.Error() == "EOF" {
				return nil
			}
			// Use appropriate error code: RequestTooLarge for size violations, ParseError for others
			errorCode := protocol.ParseError
			if errors.Is(err, protocol.ErrRequestTooLarge) {
				errorCode = protocol.RequestTooLarge
			}
			if writeErr := transport.WriteError(nil, errorCode, err.Error(), nil); writeErr != nil {
				return fmt.Errorf("failed to write error response: %w", writeErr)
			}
			continue
		}

		// JSON-RPC 2.0: requests without an "id" field are notifications.
		// The server MUST NOT reply to notifications.
		if req.ID == nil {
			s.handleNotification(req)
			continue
		}

		resp := s.HandleRequest(req)
		if err := transport.WriteResponse(resp); err != nil {
			return fmt.Errorf("failed to write response: %w", err)
		}
	}
}

// registerReadOnlyTools registers Phase 1 read-only tools
func (s *Server) registerReadOnlyTools() {
	// liza_get tool
	s.registerTool(protocol.Tool{
		Name:        "liza_get",
		Description: "Query Liza state (tasks, agents, logs, etc.)",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"query": {
					Type:        "string",
					Description: "Query path (e.g., 'tasks', 'tasks/<id>', 'agents', 'agents/<id>')",
				},
				"format": {
					Type:        "string",
					Description: "Output format (json, yaml, text)",
					Default:     "json",
				},
			},
			Required: []string{"query"},
		},
	}, s.handleGet)

	// liza_status tool
	s.registerTool(protocol.Tool{
		Name:        "liza_status",
		Description: "Get current workspace status summary",
		InputSchema: protocol.InputSchema{
			Type: "object",
		},
	}, s.handleStatus)

	// liza_validate tool
	s.registerTool(protocol.Tool{
		Name:        "liza_validate",
		Description: "Validate workspace state consistency",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"skip_spec_check": {
					Type:        "boolean",
					Description: "Skip validation of spec file existence",
					Default:     false,
				},
			},
		},
	}, s.handleValidate)

	// liza_version tool
	s.registerTool(protocol.Tool{
		Name:        "liza_version",
		Description: "Get Liza version information",
		InputSchema: protocol.InputSchema{
			Type: "object",
		},
	}, s.handleVersion)
}

// registerReadOnlyResources registers Phase 1 read-only resources
func (s *Server) registerReadOnlyResources() {
	s.registerResource(protocol.Resource{
		URI:         "liza://state",
		Name:        "Current State",
		Description: "Current workspace state (state.yaml)",
		MimeType:    "application/x-yaml",
	})

	s.registerResource(protocol.Resource{
		URI:         "liza://tasks",
		Name:        "Tasks",
		Description: "All tasks in the workspace",
		MimeType:    "application/json",
	})

	s.registerResource(protocol.Resource{
		URI:         "liza://agents",
		Name:        "Agents",
		Description: "All agents in the workspace",
		MimeType:    "application/json",
	})
}

// registerMutationTools registers Phase 2 mutation tools
func (s *Server) registerMutationTools() {
	// liza_add_task tool
	s.registerTool(protocol.Tool{
		Name:        "liza_add_task",
		Description: "Add a new task to the workspace. Requires planner role.",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"id": {
					Type:        "string",
					Description: "Unique task ID",
				},
				"desc": {
					Type:        "string",
					Description: "Task description",
				},
				"spec": {
					Type:        "string",
					Description: "Reference to specification file",
				},
				"done": {
					Type:        "string",
					Description: "Completion criteria",
				},
				"scope": {
					Type:        "string",
					Description: "Task scope description",
				},
				"priority": {
					Type:        "number",
					Description: "Task priority (default: 1)",
					Default:     1,
				},
				"depends": {
					Type:        "array",
					Description: "List of task IDs this task depends on",
				},
				"requirement_refs": {
					Type:        "array",
					Description: "Requirement references (e.g. R1, R2). Required when enforce_requirement_refs is enabled.",
				},
				"acceptance_criteria": {
					Type:        "array",
					Description: "Acceptance criteria references (e.g. AC-1, AC-2). Required for IMPLEMENTING gate.",
				},
				"verify_commands": {
					Type:        "array",
					Description: "Executable verification commands (e.g. pytest tests/test_foo.py). Required for IMPLEMENTING gate.",
				},
				"error_behavior": {
					Type:        "string",
					Description: "Expected error behavior definition (e.g. 'return typed error on invalid input, never panic')",
				},
				"type": {
					Type:        "string",
					Description: "Task type determining role workflow (default: coding)",
					Default:     "coding",
				},
				"agent_id": {
					Type:        "string",
					Description: "Agent ID performing the action (default: planner-1)",
					Default:     "planner-1",
				},
			},
			Required: []string{"id", "desc", "spec", "done", "scope"},
		},
	}, s.handleAddTask)

	// liza_claim_task tool
	s.registerTool(protocol.Tool{
		Name:        "liza_claim_task",
		Description: "Claim an unclaimed task for work. Requires coder role.",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"task_id": {
					Type:        "string",
					Description: "Task ID to claim",
				},
				"agent_id": {
					Type:        "string",
					Description: "Agent ID claiming the task",
				},
			},
			Required: []string{"task_id", "agent_id"},
		},
	}, s.handleClaimTask)

	// liza_submit_for_review tool
	s.registerTool(protocol.Tool{
		Name:        "liza_submit_for_review",
		Description: "Submit completed work for review after commit SHA validation. Requires coder role.",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"task_id": {
					Type:        "string",
					Description: "Task ID to submit",
				},
				"commit_sha": {
					Type:        "string",
					Description: "Current task worktree HEAD SHA before rebase (exact match required)",
				},
				"agent_id": {
					Type:        "string",
					Description: "Agent ID submitting the work",
				},
			},
			Required: []string{"task_id", "commit_sha", "agent_id"},
		},
	}, s.handleSubmitForReview)

	// liza_handoff tool
	s.registerTool(protocol.Tool{
		Name:        "liza_handoff",
		Description: "Initiate context-exhaustion handoff for a claimed task. Requires coder role.",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"task_id": {
					Type:        "string",
					Description: "Task ID to hand off",
				},
				"summary": {
					Type:        "string",
					Description: "Brief summary of completed work",
				},
				"next_action": {
					Type:        "string",
					Description: "Concrete next action for resume",
				},
				"agent_id": {
					Type:        "string",
					Description: "Coder agent ID initiating handoff",
				},
			},
			Required: []string{"task_id", "summary", "next_action", "agent_id"},
		},
	}, s.handleHandoff)

	// liza_submit_verdict tool
	s.registerTool(protocol.Tool{
		Name:        "liza_submit_verdict",
		Description: "Submit review verdict (APPROVED or REJECTED). Requires code-reviewer role.",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"task_id": {
					Type:        "string",
					Description: "Task ID being reviewed",
				},
				"verdict": {
					Type:        "string",
					Description: "Review verdict (APPROVED or REJECTED)",
					Enum:        []string{"APPROVED", "REJECTED"},
				},
				"agent_id": {
					Type:        "string",
					Description: "Reviewer agent ID",
				},
				"reason": {
					Type:        "string",
					Description: "Reason for verdict (required for REJECTED)",
				},
			},
			Required: []string{"task_id", "verdict", "agent_id"},
		},
	}, s.handleSubmitVerdict)

	// liza_mark_blocked tool
	s.registerTool(protocol.Tool{
		Name:        "liza_mark_blocked",
		Description: "Mark a task as BLOCKED when work cannot proceed due to unresolvable blocker (spec ambiguity, missing dependency, design conflict). Per blocking protocol: requires reason and 1-3 clarifying questions.",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"task_id": {
					Type:        "string",
					Description: "Task ID to mark as blocked",
				},
				"agent_id": {
					Type:        "string",
					Description: "Agent ID marking the task blocked (must match task's assigned_to)",
				},
				"reason": {
					Type:        "string",
					Description: "Specific reason for blocking (what is blocking progress)",
				},
				"questions": {
					Type:        "array",
					Description: "1-3 clarifying questions that would unblock if answered",
				},
			},
			Required: []string{"task_id", "agent_id", "reason", "questions"},
		},
	}, s.handleMarkBlocked)

	// liza_release_claim tool
	s.registerTool(protocol.Tool{
		Name:        "liza_release_claim",
		Description: "Release claim on a task",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"task_id": {
					Type:        "string",
					Description: "Task ID to release",
				},
				"role": {
					Type:        "string",
					Description: "Role releasing the claim (coder, code-reviewer)",
				},
				"agent_id": {
					Type:        "string",
					Description: "Agent ID releasing the claim",
				},
				"reason": {
					Type:        "string",
					Description: "Reason for releasing",
				},
				"force": {
					Type:        "boolean",
					Description: "Force release even in terminal states",
					Default:     false,
				},
			},
			Required: []string{"task_id", "role", "agent_id"},
		},
	}, s.handleReleaseClaim)

	// liza_supersede_task tool
	s.registerTool(protocol.Tool{
		Name:        "liza_supersede_task",
		Description: "Mark a task as superseded by replacement tasks. Requires planner role.",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"task_id": {
					Type:        "string",
					Description: "Task ID to supersede",
				},
				"replacement_ids": {
					Type:        "array",
					Description: "List of replacement task IDs",
				},
				"reason": {
					Type:        "string",
					Description: "Reason for superseding",
				},
				"agent_id": {
					Type:        "string",
					Description: "Agent ID performing the action",
				},
			},
			Required: []string{"task_id", "reason", "agent_id"},
		},
	}, s.handleSupersede)

	// liza_submit_audit_finding tool
	s.registerTool(protocol.Tool{
		Name:        "liza_submit_audit_finding",
		Description: "Submit an audit finding for a task. Requires auditor role. Findings are advisory — the supervisor retains deterministic control.",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"finding_id": {
					Type:        "string",
					Description: "Unique finding ID (e.g., finding-1)",
				},
				"task_id": {
					Type:        "string",
					Description: "Task ID this finding relates to",
				},
				"severity": {
					Type:        "string",
					Description: "Finding severity",
					Enum:        []string{"HIGH", "MEDIUM", "LOW"},
				},
				"type": {
					Type:        "string",
					Description: "Finding type",
					Enum:        []string{"SPEC_MISMATCH", "MISSING_TEST", "MISSING_EDGE_CASE", "QUALITY_ISSUE"},
				},
				"evidence": {
					Type:        "string",
					Description: "Concrete evidence supporting the finding",
				},
				"recommended_action": {
					Type:        "string",
					Description: "Suggested remediation action",
				},
				"spec_reference": {
					Type:        "string",
					Description: "Specific acceptance criterion or spec section reference",
				},
				"agent_id": {
					Type:        "string",
					Description: "Auditor agent ID submitting the finding",
				},
			},
			Required: []string{"finding_id", "task_id", "severity", "type", "evidence", "agent_id"},
		},
	}, s.handleSubmitAuditFinding)
}

// registerComplexOperations registers Phase 3 complex operation tools
func (s *Server) registerComplexOperations() {
	// liza_wt_create tool
	s.registerTool(protocol.Tool{
		Name:        "liza_wt_create",
		Description: "Create git worktree for a claimed task",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"task_id": {
					Type:        "string",
					Description: "Task ID to create worktree for",
				},
				"fresh": {
					Type:        "boolean",
					Description: "Delete and recreate existing worktree",
					Default:     false,
				},
			},
			Required: []string{"task_id"},
		},
	}, s.handleWtCreate)

	// liza_wt_delete tool
	s.registerTool(protocol.Tool{
		Name:        "liza_wt_delete",
		Description: "Delete git worktree for a task",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"task_id": {
					Type:        "string",
					Description: "Task ID to delete worktree for",
				},
			},
			Required: []string{"task_id"},
		},
	}, s.handleWtDelete)

	// liza_wt_merge tool
	s.registerTool(protocol.Tool{
		Name:        "liza_wt_merge",
		Description: "Merge approved task to integration branch. Requires code-reviewer role.",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"task_id": {
					Type:        "string",
					Description: "Task ID to merge",
				},
				"agent_id": {
					Type:        "string",
					Description: "Agent ID performing the merge",
				},
			},
			Required: []string{"task_id", "agent_id"},
		},
	}, s.handleWtMerge)

	// liza_analyze tool
	s.registerTool(protocol.Tool{
		Name:        "liza_analyze",
		Description: "Run circuit breaker analysis on task patterns",
		InputSchema: protocol.InputSchema{
			Type: "object",
		},
	}, s.handleAnalyze)

	// liza_update_sprint_metrics tool
	s.registerTool(protocol.Tool{
		Name:        "liza_update_sprint_metrics",
		Description: "Recompute sprint metrics from current state",
		InputSchema: protocol.InputSchema{
			Type: "object",
		},
	}, s.handleUpdateSprintMetrics)

	// liza_sprint_checkpoint tool
	s.registerTool(protocol.Tool{
		Name:        "liza_sprint_checkpoint",
		Description: "Create sprint checkpoint for human review. Pauses all agents and generates a sprint summary report.",
		InputSchema: protocol.InputSchema{
			Type: "object",
		},
	}, s.handleSprintCheckpoint)

	// liza_clear_stale_review_claims tool
	s.registerTool(protocol.Tool{
		Name:        "liza_clear_stale_review_claims",
		Description: "Clear expired review leases",
		InputSchema: protocol.InputSchema{
			Type: "object",
		},
	}, s.handleClearStaleReviews)

	// liza_write_checkpoint tool
	s.registerTool(protocol.Tool{
		Name:        "liza_write_checkpoint",
		Description: "Write a pre-execution checkpoint before implementing a task. Required before submission for review. Requires coder role.",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"task_id": {
					Type:        "string",
					Description: "Task ID to write checkpoint for",
				},
				"agent_id": {
					Type:        "string",
					Description: "Agent ID writing the checkpoint",
				},
				"intent": {
					Type:        "string",
					Description: "Specific, observable intent of the implementation",
				},
				"validation_plan": {
					Type:        "string",
					Description: "Concrete validation command and expected output",
				},
				"files_to_modify": {
					Type:        "array",
					Description: "List of files that will be modified",
				},
				"assumptions": {
					Type:        "array",
					Description: "Tagged assumptions (optional)",
				},
				"risks": {
					Type:        "string",
					Description: "Identified risks (optional)",
				},
				"tdd_not_required": {
					Type:        "string",
					Description: "Justification for why this task does not require new test files (e.g. cosmetic-only change, existing tests cover behavior). If omitted, TDD enforcement applies normally.",
				},
			},
			Required: []string{"task_id", "agent_id", "intent", "validation_plan", "files_to_modify"},
		},
	}, s.handleWriteCheckpoint)

	// liza_delete_agent tool
	s.registerTool(protocol.Tool{
		Name:        "liza_delete_agent",
		Description: "Delete an agent from the workspace",
		InputSchema: protocol.InputSchema{
			Type: "object",
			Properties: map[string]protocol.Property{
				"agent_id": {
					Type:        "string",
					Description: "Agent ID to delete",
				},
				"reason": {
					Type:        "string",
					Description: "Reason for deletion",
				},
				"force": {
					Type:        "boolean",
					Description: "Force deletion even if agent has active tasks",
					Default:     false,
				},
			},
			Required: []string{"agent_id", "reason"},
		},
	}, s.handleDeleteAgent)
}
