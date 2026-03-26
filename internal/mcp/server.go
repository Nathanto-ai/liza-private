package mcp

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/mcp/protocol"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/pipeline"
	"github.com/liza-mas/liza/internal/roles"
)

// Server represents the MCP server.
type Server struct {
	projectRoot     string
	logPath         string
	role            string
	logger          *slog.Logger
	bb              *db.Blackboard
	resolver        *pipeline.Resolver
	pipelineLoadErr error
	tools           map[string]protocol.Tool
	resources       map[string]protocol.Resource
	handlers        map[string]ToolHandler
}

// NewServer creates a new MCP server.
func NewServer(projectRoot, logPath string, role ...string) *Server {
	selectedRole := ""
	if len(role) > 0 {
		selectedRole = role[0]
	}

	cfg, err := pipeline.LoadFrozen(projectRoot)
	if err != nil {
		slog.Error("mcp: failed to load pipeline config for operation authorization", "error", err)
	}

	var resolver *pipeline.Resolver
	if cfg != nil {
		resolver = pipeline.NewResolver(cfg)
	}

	s := &Server{
		projectRoot:     projectRoot,
		logPath:         logPath,
		role:            selectedRole,
		logger:          slog.New(slog.NewTextHandler(os.Stderr, nil)),
		bb:              db.For(paths.New(projectRoot).StatePath()),
		resolver:        resolver,
		pipelineLoadErr: err,
		tools:           make(map[string]protocol.Tool),
		resources:       make(map[string]protocol.Resource),
		handlers:        make(map[string]ToolHandler),
	}

	s.registerReadOnlyTools()
	s.registerReadOnlyResources()

	if selectedRole == "" {
		s.registerMutationTools()
		s.registerComplexOperations()
	} else {
		s.registerToolsForRole(selectedRole)
	}

	return s
}

// GetCapabilities returns the server capabilities.
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

// ListTools returns all registered tools.
func (s *Server) ListTools() []protocol.Tool {
	tools := make([]protocol.Tool, 0, len(s.tools))
	for _, tool := range s.tools {
		tools = append(tools, tool)
	}
	return tools
}

// GetTool returns a specific tool by name.
func (s *Server) GetTool(name string) (protocol.Tool, bool) {
	tool, ok := s.tools[name]
	return tool, ok
}

// GetHandler returns a specific handler by tool name.
func (s *Server) GetHandler(name string) (ToolHandler, bool) {
	handler, ok := s.handlers[name]
	return handler, ok
}

// ToolNames returns all registered tool names.
func (s *Server) ToolNames() []string {
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	return names
}

// ListResources returns all registered resources.
func (s *Server) ListResources() []protocol.Resource {
	resources := make([]protocol.Resource, 0, len(s.resources))
	for _, resource := range s.resources {
		resources = append(resources, resource)
	}
	return resources
}

// recordMCPActivity writes the current timestamp to the MCP activity file.
func (s *Server) recordMCPActivity() {
	activityPath := paths.New(s.projectRoot).MCPActivityPath()
	_ = os.WriteFile(activityPath, []byte(time.Now().UTC().Format(time.RFC3339)), 0644)
}

// Run starts the MCP server with stdio transport.
func (s *Server) Run() error {
	return s.runWithTransport(protocol.NewStdioTransport())
}

func (s *Server) runWithTransport(transport runTransport) error {
	for {
		req, err := transport.ReadRequest()
		if err != nil {
			if errors.Is(err, io.EOF) || err.Error() == "EOF" {
				return nil
			}

			errorCode := protocol.ParseError
			if errors.Is(err, protocol.ErrRequestTooLarge) {
				errorCode = protocol.RequestTooLarge
			}
			if writeErr := transport.WriteError(nil, errorCode, err.Error(), nil); writeErr != nil {
				return fmt.Errorf("failed to write error response: %w", writeErr)
			}
			continue
		}

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

// registerToolsForRole selectively registers only the mutation and complex
// operation tools that the given agent role is allowed to use.
func (s *Server) registerToolsForRole(role string) {
	full := &Server{
		projectRoot: s.projectRoot,
		logPath:     s.logPath,
		bb:          s.bb,
		logger:      s.logger,
		tools:       make(map[string]protocol.Tool),
		resources:   make(map[string]protocol.Resource),
		handlers:    make(map[string]ToolHandler),
	}
	full.registerMutationTools()
	full.registerComplexOperations()

	allowed := roleAllowedTools(role)
	for name := range allowed {
		if tool, ok := full.tools[name]; ok {
			s.tools[name] = tool
			s.handlers[name] = full.handlers[name]
		}
	}
}

// roleAllowedTools returns the set of mutation and complex tool names allowed
// for the given runtime role.
func roleAllowedTools(role string) map[string]bool {
	switch role {
	case roles.RuntimePlanner:
		return map[string]bool{
			"liza_add_task":              true,
			"liza_supersede_task":        true,
			"liza_update_sprint_metrics": true,
			"liza_sprint_checkpoint":     true,
			"liza_delete_agent":          true,
			"liza_analyze":               true,
		}
	case roles.RuntimeCoder:
		return map[string]bool{
			"liza_claim_task":        true,
			"liza_submit_for_review": true,
			"liza_handoff":           true,
			"liza_mark_blocked":      true,
			"liza_release_claim":     true,
			"liza_wt_create":         true,
			"liza_wt_delete":         true,
			"liza_write_checkpoint":  true,
			"liza_exec":              true,
		}
	case roles.RuntimeCodeReviewer:
		return map[string]bool{
			"liza_submit_verdict":            true,
			"liza_wt_merge":                  true,
			"liza_clear_stale_review_claims": true,
			"liza_release_claim":             true,
			"liza_mark_blocked":              true,
			"liza_wt_create":                 true,
			"liza_wt_delete":                 true,
			"liza_exec":                      true,
		}
	case roles.RuntimeAuditor:
		return map[string]bool{
			"liza_submit_audit_finding": true,
			"liza_analyze":              true,
			"liza_mark_blocked":         true,
			"liza_exec":                 true,
		}
	default:
		return map[string]bool{}
	}
}
