package commands

import (
	"fmt"
	"strings"

	"github.com/liza-mas/liza/internal/identity"
	"github.com/liza-mas/liza/internal/roles"
)

// RequireRole validates that the given agentID has one of the allowed roles.
// Returns nil if the role is allowed, or an error describing the violation.
//
// When agentID is empty (human / manual CLI usage), the check is skipped
// to preserve backward compatibility for operator-driven commands.
func RequireRole(agentID string, allowedRoles ...string) error {
	if agentID == "" {
		return nil // manual / human usage — no restriction
	}

	role, err := identity.ExtractRole(agentID)
	if err != nil {
		return fmt.Errorf("cannot determine role from agent ID %q: %w", agentID, err)
	}

	for _, allowed := range allowedRoles {
		if role == allowed {
			return nil
		}
	}

	return fmt.Errorf(
		"role %q (agent %s) is not allowed for this command; allowed roles: %s",
		role, agentID, strings.Join(allowedRoles, ", "))
}

// MutationRoles defines the per-command role allowlists matching the MCP server's
// roleAllowedTools() function so that CLI and MCP enforcement stay in sync.
var MutationRoles = map[string][]string{
	"claim-task":               {roles.RuntimeCoder},
	"submit-for-review":        {roles.RuntimeCoder},
	"handoff":                  {roles.RuntimeCoder},
	"write-checkpoint":         {roles.RuntimeCoder},
	"wt-create":                {roles.RuntimeCoder, roles.RuntimeCodeReviewer},
	"wt-delete":                {roles.RuntimeCoder, roles.RuntimeCodeReviewer},
	"wt-merge":                 {roles.RuntimeCodeReviewer},
	"submit-verdict":           {roles.RuntimeCodeReviewer},
	"claim-review":             {roles.RuntimeCodeReviewer},
	"clear-stale-review-claims": {roles.RuntimeCodeReviewer},
	"add-task":                 {roles.RuntimePlanner},
	"supersede-task":           {roles.RuntimePlanner},
	"sprint-checkpoint":        {roles.RuntimePlanner},
	"delete-agent":             {roles.RuntimePlanner},
	"update-sprint-metrics":    {roles.RuntimePlanner},
	"submit-audit-finding":     {roles.RuntimeAuditor},
	"mark-blocked":             {roles.RuntimeCoder, roles.RuntimeCodeReviewer, roles.RuntimeAuditor},
	"release-claim":            {roles.RuntimeCoder, roles.RuntimeCodeReviewer},
	"analyze":                  {roles.RuntimePlanner, roles.RuntimeAuditor},
}
