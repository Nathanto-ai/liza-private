package ops

import (
	"fmt"
	"log"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/pipeline"
)

// loadResolver loads the frozen pipeline config for the given project root.
func loadResolver(projectRoot string) (*pipeline.Resolver, *pipeline.PipelineConfig, error) {
	cfg, err := pipeline.LoadFrozen(projectRoot)
	if err != nil {
		return nil, nil, err
	}
	return pipeline.NewResolver(cfg), cfg, nil
}

// warnSkipRolePair logs a warning when a role-pair is skipped due to a resolver
// error during transition map construction. Should not happen on validated configs.
func warnSkipRolePair(rpName string, err error) {
	log.Printf("WARNING: BuildPipelineTransitions: skipping role-pair %q: %v", rpName, err)
}

// lifecycleStatuses holds the resolved statuses for a single role-pair's lifecycle.
type lifecycleStatuses struct {
	initial   models.TaskStatus
	executing models.TaskStatus
	submitted models.TaskStatus
	reviewing models.TaskStatus
	rejected  models.TaskStatus
	approved  models.TaskStatus
}

// resolveLifecycleStatuses resolves all lifecycle statuses for a role-pair in one call.
func resolveLifecycleStatuses(r *pipeline.Resolver, rpName string) (lifecycleStatuses, error) {
	initial, err := r.InitialStatus(rpName)
	if err != nil {
		return lifecycleStatuses{}, err
	}
	executing, err := r.ExecutingStatus(rpName)
	if err != nil {
		return lifecycleStatuses{}, err
	}
	submitted, err := r.SubmittedStatus(rpName)
	if err != nil {
		return lifecycleStatuses{}, err
	}
	reviewing, err := r.ReviewingStatus(rpName)
	if err != nil {
		return lifecycleStatuses{}, err
	}
	rejected, err := r.RejectedStatus(rpName)
	if err != nil {
		return lifecycleStatuses{}, err
	}
	approved, err := r.ApprovedStatus(rpName)
	if err != nil {
		return lifecycleStatuses{}, err
	}
	return lifecycleStatuses{initial, executing, submitted, reviewing, rejected, approved}, nil
}

// BuildPipelineTransitions creates a complete transition map by merging the
// resolver's intra-pair transitions with cross-cutting meta-state transitions.
func BuildPipelineTransitions(r *pipeline.Resolver) map[models.TaskStatus][]models.TaskStatus {
	tm := r.TransitionMap()

	var executingStatuses []models.TaskStatus
	for _, rpName := range r.RolePairNames() {
		ls, err := resolveLifecycleStatuses(r, rpName)
		if err != nil {
			warnSkipRolePair(rpName, err)
			continue
		}
		executingStatuses = append(executingStatuses, ls.executing)

		// Cross-cutting additions per lifecycle phase:
		tm[ls.initial] = append(tm[ls.initial], models.TaskStatusAbandoned, models.TaskStatusSuperseded)
		tm[ls.executing] = append(tm[ls.executing], models.TaskStatusBlocked, ls.initial, models.TaskStatusIntegrationFailed)
		tm[ls.reviewing] = append(tm[ls.reviewing], ls.submitted)
		tm[ls.rejected] = append(tm[ls.rejected], ls.executing, models.TaskStatusBlocked, models.TaskStatusSuperseded, models.TaskStatusAbandoned)
		tm[ls.approved] = append(tm[ls.approved], models.TaskStatusMerged, models.TaskStatusIntegrationFailed)

		// Quorum state cross-cutting transitions.
		partiallyApproved, paErr := r.PartiallyApprovedStatus(rpName)
		reviewing2, r2Err := r.Reviewing2Status(rpName)
		if paErr == nil && r2Err == nil {
			tm[reviewing2] = append(tm[reviewing2], partiallyApproved) // stale revert
			tm[partiallyApproved] = append(tm[partiallyApproved], models.TaskStatusAbandoned, models.TaskStatusSuperseded)
		}
	}

	// Meta-state transitions
	tm[models.TaskStatusBlocked] = []models.TaskStatus{models.TaskStatusSuperseded, models.TaskStatusAbandoned}
	tm[models.TaskStatusIntegrationFailed] = append([]models.TaskStatus{models.TaskStatusAbandoned}, executingStatuses...)
	tm[models.TaskStatusMerged] = []models.TaskStatus{}
	tm[models.TaskStatusAbandoned] = []models.TaskStatus{}
	tm[models.TaskStatusSuperseded] = []models.TaskStatus{}

	return tm
}

// PipelineDetectionContext holds pipeline-derived data needed for orchestrator
// wake detection. Computed once from a single config load via LoadDetectionContext.
type PipelineDetectionContext struct {
	SprintTerminals []models.TaskStatus
	PlanningPairs   map[string]bool
	InitialStatuses []models.TaskStatus
}

// LoadDetectionContext loads pipeline config once and returns both sprint-terminal
// states and transition-source pairs.
func LoadDetectionContext(projectRoot string) (*PipelineDetectionContext, error) {
	resolver, _, err := loadResolver(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("loading pipeline config for detection context: %w", err)
	}
	initialStatuses := make([]models.TaskStatus, 0, len(resolver.RolePairNames()))
	for _, rpName := range resolver.RolePairNames() {
		initial, iErr := resolver.InitialStatus(rpName)
		if iErr == nil {
			initialStatuses = append(initialStatuses, initial)
		}
	}

	return &PipelineDetectionContext{
		SprintTerminals: resolver.SprintTerminalStates(),
		PlanningPairs:   resolver.TransitionSourcePairs(),
		InitialStatuses: initialStatuses,
	}, nil
}

// SprintTerminalStates returns pipeline-defined sprint-terminal states for a project.
func SprintTerminalStates(projectRoot string) ([]models.TaskStatus, error) {
	ctx, err := LoadDetectionContext(projectRoot)
	if err != nil {
		return nil, err
	}
	return ctx.SprintTerminals, nil
}

// TransitionSourcePairs returns the set of role-pair names that are transition
// sources in the pipeline config.
func TransitionSourcePairs(projectRoot string) (map[string]bool, error) {
	ctx, err := LoadDetectionContext(projectRoot)
	if err != nil {
		return nil, err
	}
	return ctx.PlanningPairs, nil
}

// IsPlanningPair reports whether a role-pair is a transition source ("planning pair").
// planningPairs is the set from TransitionSourcePairs / LoadDetectionContext.
// When planningPairs is nil (legacy projects without pipeline config), falls back
// to recognizing "code-planning-pair" as the only planning pair.
func IsPlanningPair(rolePair string, planningPairs map[string]bool) bool {
	if planningPairs == nil {
		return rolePair == "code-planning-pair"
	}
	return planningPairs[rolePair]
}

// allPlannedTasksTerminalForProject checks if all planned tasks are sprint-terminal.
func allPlannedTasksTerminalForProject(s *models.State, projectRoot string) (bool, error) {
	terminals, err := SprintTerminalStates(projectRoot)
	if err != nil {
		return false, err
	}
	return s.AllPlannedTasksTerminalWith(terminals), nil
}

// LoadResolverForModels loads the pipeline resolver as a models.PipelineResolver.
func LoadResolverForModels(projectRoot string) (models.PipelineResolver, error) {
	resolver, _, err := loadResolver(projectRoot)
	if err != nil {
		return nil, err
	}
	return resolver, nil
}

// pipelineBundle holds the resolver, interface, and transition map from a single config load.
// Used to avoid double-parsing pipeline.yaml within a single operation.
type pipelineBundle struct {
	pr          models.PipelineResolver
	resolver    *pipeline.Resolver // concrete resolver for policy queries (ProviderDiversity, etc.)
	transitions map[models.TaskStatus][]models.TaskStatus
}

// loadPipelineBundle loads the pipeline config once and returns the resolver interface
// and pre-built transition map.
func loadPipelineBundle(projectRoot string) (*pipelineBundle, error) {
	resolver, _, err := loadResolver(projectRoot)
	if err != nil {
		return nil, err
	}
	return &pipelineBundle{
		pr:          resolver,
		resolver:    resolver,
		transitions: BuildPipelineTransitions(resolver),
	}, nil
}

// LoadPipelineTransitions loads the pipeline config and builds the transition map.
// Exported for use by the agent package.
func LoadPipelineTransitions(projectRoot string) (map[models.TaskStatus][]models.TaskStatus, error) {
	resolver, _, err := loadResolver(projectRoot)
	if err != nil {
		return nil, err
	}
	return BuildPipelineTransitions(resolver), nil
}
