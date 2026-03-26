package pipeline

import (
	"fmt"
	"slices"
	"time"

	"github.com/liza-mas/liza/internal/models"
)

// Resolver wraps a PipelineConfig for state resolution queries.
type Resolver struct {
	config            *PipelineConfig
	transitionSources map[string]bool // lazy-init cache for TransitionSourcePairs
}

// NewResolver creates a Resolver from a validated PipelineConfig.
func NewResolver(config *PipelineConfig) *Resolver {
	return &Resolver{config: config}
}

func (r *Resolver) lookupStates(rolePair string) (*RolePairStates, error) {
	rp, ok := r.config.Pipeline.RolePairs[rolePair]
	if !ok {
		return nil, fmt.Errorf("unknown role-pair %q", rolePair)
	}
	return &rp.States, nil
}

// InitialStatus returns the initial state for the given role-pair.
func (r *Resolver) InitialStatus(rolePair string) (models.TaskStatus, error) {
	s, err := r.lookupStates(rolePair)
	if err != nil {
		return "", err
	}
	return models.TaskStatus(s.Initial), nil
}

// ExecutingStatus returns the executing state for the given role-pair.
func (r *Resolver) ExecutingStatus(rolePair string) (models.TaskStatus, error) {
	s, err := r.lookupStates(rolePair)
	if err != nil {
		return "", err
	}
	return models.TaskStatus(s.Executing), nil
}

// SubmittedStatus returns the submitted state for the given role-pair.
func (r *Resolver) SubmittedStatus(rolePair string) (models.TaskStatus, error) {
	s, err := r.lookupStates(rolePair)
	if err != nil {
		return "", err
	}
	return models.TaskStatus(s.Submitted), nil
}

// ReviewingStatus returns the reviewing state for the given role-pair.
func (r *Resolver) ReviewingStatus(rolePair string) (models.TaskStatus, error) {
	s, err := r.lookupStates(rolePair)
	if err != nil {
		return "", err
	}
	return models.TaskStatus(s.Reviewing), nil
}

// ApprovedStatus returns the approved state for the given role-pair.
func (r *Resolver) ApprovedStatus(rolePair string) (models.TaskStatus, error) {
	s, err := r.lookupStates(rolePair)
	if err != nil {
		return "", err
	}
	return models.TaskStatus(s.Approved), nil
}

// RejectedStatus returns the rejected state for the given role-pair.
func (r *Resolver) RejectedStatus(rolePair string) (models.TaskStatus, error) {
	s, err := r.lookupStates(rolePair)
	if err != nil {
		return "", err
	}
	return models.TaskStatus(s.Rejected), nil
}

// PartiallyApprovedStatus returns the partially-approved state for the given role-pair.
// Returns an error if the role-pair does not declare a partially-approved state.
func (r *Resolver) PartiallyApprovedStatus(rolePair string) (models.TaskStatus, error) {
	s, err := r.lookupStates(rolePair)
	if err != nil {
		return "", err
	}
	if s.PartiallyApproved == "" {
		return "", fmt.Errorf("role-pair %q has no partially-approved state declared", rolePair)
	}
	return models.TaskStatus(s.PartiallyApproved), nil
}

// Reviewing2Status returns the reviewing-2 state for the given role-pair.
// Returns an error if the role-pair does not declare a reviewing-2 state.
func (r *Resolver) Reviewing2Status(rolePair string) (models.TaskStatus, error) {
	s, err := r.lookupStates(rolePair)
	if err != nil {
		return "", err
	}
	if s.Reviewing2 == "" {
		return "", fmt.Errorf("role-pair %q has no reviewing-2 state declared", rolePair)
	}
	return models.TaskStatus(s.Reviewing2), nil
}

// ResolvedTimeouts holds parsed timeout durations for a role.
type ResolvedTimeouts struct {
	Execution    time.Duration
	PollInterval time.Duration
	MaxWait      time.Duration
}

// legacyRoleAliases maps legacy runtime role names to pipeline role names.
var legacyRoleAliases = map[string]string{
	"planner": "orchestrator",
}

// resolveRoleName resolves a role name, trying legacy aliases if direct lookup fails.
func (r *Resolver) resolveRoleName(name string) (string, bool) {
	if _, ok := r.config.Pipeline.Roles[name]; ok {
		return name, true
	}
	if alias, ok := legacyRoleAliases[name]; ok {
		if _, ok := r.config.Pipeline.Roles[alias]; ok {
			return alias, true
		}
	}
	return name, false
}

// RoleType returns the type (doer, reviewer, orchestrator) for the named role.
func (r *Resolver) RoleType(name string) (string, error) {
	resolved, ok := r.resolveRoleName(name)
	if !ok {
		return "", fmt.Errorf("unknown role %q", name)
	}
	return r.config.Pipeline.Roles[resolved].Type, nil
}

// DoerRoleNames returns the sorted names of all roles with type "doer".
func (r *Resolver) DoerRoleNames() []string {
	return r.roleNamesByType("doer")
}

// ReviewerRoleNames returns the sorted names of all roles with type "reviewer".
func (r *Resolver) ReviewerRoleNames() []string {
	return r.roleNamesByType("reviewer")
}

// AllRoleNames returns the sorted names of all declared roles.
func (r *Resolver) AllRoleNames() []string {
	names := make([]string, 0, len(r.config.Pipeline.Roles))
	for name := range r.config.Pipeline.Roles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// AllowedOperations returns the allowed-operations list for the named role.
func (r *Resolver) AllowedOperations(name string) ([]string, error) {
	resolved, ok := r.resolveRoleName(name)
	if !ok {
		return nil, fmt.Errorf("unknown role %q", name)
	}
	return r.config.Pipeline.Roles[resolved].AllowedOperations, nil
}

// RoleTimeouts returns the parsed timeout durations for the named role.
func (r *Resolver) RoleTimeouts(name string) (*ResolvedTimeouts, error) {
	resolved, ok := r.resolveRoleName(name)
	if !ok {
		return nil, fmt.Errorf("unknown role %q", name)
	}
	role := r.config.Pipeline.Roles[resolved]
	if role.Timeouts == nil {
		return nil, fmt.Errorf("role %q has no timeouts defined", name)
	}
	exec, err := time.ParseDuration(role.Timeouts.Execution)
	if err != nil {
		return nil, fmt.Errorf("role %q: parsing execution timeout: %w", name, err)
	}
	poll, err := time.ParseDuration(role.Timeouts.PollInterval)
	if err != nil {
		return nil, fmt.Errorf("role %q: parsing poll-interval: %w", name, err)
	}
	maxWait, err := time.ParseDuration(role.Timeouts.MaxWait)
	if err != nil {
		return nil, fmt.Errorf("role %q: parsing max-wait: %w", name, err)
	}
	return &ResolvedTimeouts{
		Execution:    exec,
		PollInterval: poll,
		MaxWait:      maxWait,
	}, nil
}

// MaxInstances returns the max-instances for the named role.
// Orchestrator roles always return 1 regardless of YAML value (spec invariant).
// Returns 0 (unlimited) if the field is unset for non-orchestrator roles.
func (r *Resolver) MaxInstances(name string) (int, error) {
	resolved, ok := r.resolveRoleName(name)
	if !ok {
		return 0, fmt.Errorf("unknown role %q", name)
	}
	role := r.config.Pipeline.Roles[resolved]
	if role.Type == "orchestrator" {
		return 1, nil
	}
	return role.MaxInstances, nil
}

// RoleDisplayName returns the display-name for the named role.
// Returns the role key itself if the role is not found or has no display-name.
func (r *Resolver) RoleDisplayName(name string) string {
	resolved, ok := r.resolveRoleName(name)
	if !ok || r.config.Pipeline.Roles[resolved].DisplayName == "" {
		return name
	}
	return r.config.Pipeline.Roles[resolved].DisplayName
}

// roleNamesByType returns the sorted names of all roles matching the given type.
func (r *Resolver) roleNamesByType(roleType string) []string {
	var names []string
	for name, role := range r.config.Pipeline.Roles {
		if role.Type == roleType {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// ContextSections returns the context-sections list for the named role.
func (r *Resolver) ContextSections(name string) ([]string, error) {
	resolved, ok := r.resolveRoleName(name)
	if !ok {
		return nil, fmt.Errorf("unknown role %q", name)
	}
	return r.config.Pipeline.Roles[resolved].ContextSections, nil
}

// Skills returns the skills list for the named role.
func (r *Resolver) Skills(name string) ([]string, error) {
	resolved, ok := r.resolveRoleName(name)
	if !ok {
		return nil, fmt.Errorf("unknown role %q", name)
	}
	return r.config.Pipeline.Roles[resolved].Skills, nil
}

// MandatoryDocs returns the mandatory-docs list for the named role.
func (r *Resolver) MandatoryDocs(name string) ([]string, error) {
	resolved, ok := r.resolveRoleName(name)
	if !ok {
		return nil, fmt.Errorf("unknown role %q", name)
	}
	return r.config.Pipeline.Roles[resolved].MandatoryDocs, nil
}

// ReviewPolicy returns the review-policy for the named role-pair.
// Returns nil (no error) if the role-pair has no review-policy configured.
func (r *Resolver) ReviewPolicy(rolePair string) (*ReviewPolicyDef, error) {
	rp, ok := r.config.Pipeline.RolePairs[rolePair]
	if !ok {
		return nil, fmt.Errorf("unknown role-pair %q", rolePair)
	}
	return rp.ReviewPolicy, nil
}

// EffectiveQuorum returns the effective quorum for the given role-pair and impact level.
// Impact values: "standard" (or empty) uses the base quorum, "significant" uses the
// significant-change override, "architecture" uses the architecture-impact override.
// Falls back to the base quorum when no matching override exists.
// Returns 1 when no review-policy is configured.
func (r *Resolver) EffectiveQuorum(rolePair, impact string) (int, error) {
	rp, ok := r.config.Pipeline.RolePairs[rolePair]
	if !ok {
		return 0, fmt.Errorf("unknown role-pair %q", rolePair)
	}
	if rp.ReviewPolicy == nil {
		return 1, nil
	}
	switch impact {
	case "significant":
		if rp.ReviewPolicy.SignificantChange != nil {
			return rp.ReviewPolicy.SignificantChange.Quorum, nil
		}
	case "architecture":
		if rp.ReviewPolicy.ArchitectureImpact != nil {
			return rp.ReviewPolicy.ArchitectureImpact.Quorum, nil
		}
	}
	return rp.ReviewPolicy.Quorum, nil
}

// ProviderDiversity returns the provider-diversity setting for the given
// role-pair at the specified impact level.
// Override levels ("significant", "architecture") take precedence when configured;
// otherwise falls through to the base-level review-policy value.
// Returns "" when no diversity is configured.
func (r *Resolver) ProviderDiversity(rolePair, impact string) (string, error) {
	rp, ok := r.config.Pipeline.RolePairs[rolePair]
	if !ok {
		return "", fmt.Errorf("unknown role-pair %q", rolePair)
	}
	if rp.ReviewPolicy == nil {
		return "", nil
	}
	switch impact {
	case "significant":
		if rp.ReviewPolicy.SignificantChange != nil && rp.ReviewPolicy.SignificantChange.ProviderDiversity != "" {
			return rp.ReviewPolicy.SignificantChange.ProviderDiversity, nil
		}
	case "architecture":
		if rp.ReviewPolicy.ArchitectureImpact != nil && rp.ReviewPolicy.ArchitectureImpact.ProviderDiversity != "" {
			return rp.ReviewPolicy.ArchitectureImpact.ProviderDiversity, nil
		}
	}
	return rp.ReviewPolicy.ProviderDiversity, nil
}

// TransitionMap generates the intra-pair transition map for all role-pairs.
// The fixed intra-pair flow is: initial→executing→submitted→reviewing→approved|rejected,
// with rejected→initial. When quorum states are declared, reviewing also transitions to
// partially-approved, partially-approved→reviewing-2, and reviewing-2→approved|rejected.
// Cross-cutting meta-states (BLOCKED, ABANDONED, etc.) are not included.
func (r *Resolver) TransitionMap() map[models.TaskStatus][]models.TaskStatus {
	tm := make(map[models.TaskStatus][]models.TaskStatus)
	for _, rp := range r.config.Pipeline.RolePairs {
		s := rp.States
		initial := models.TaskStatus(s.Initial)
		executing := models.TaskStatus(s.Executing)
		submitted := models.TaskStatus(s.Submitted)
		reviewing := models.TaskStatus(s.Reviewing)
		approved := models.TaskStatus(s.Approved)
		rejected := models.TaskStatus(s.Rejected)

		tm[initial] = []models.TaskStatus{executing}
		tm[executing] = []models.TaskStatus{submitted}
		tm[submitted] = []models.TaskStatus{reviewing}
		tm[reviewing] = []models.TaskStatus{approved, rejected}
		tm[rejected] = []models.TaskStatus{initial}
		tm[approved] = []models.TaskStatus{} // terminal within the pair

		// Quorum state transitions when partially-approved and reviewing-2 are declared.
		if s.PartiallyApproved != "" && s.Reviewing2 != "" {
			partiallyApproved := models.TaskStatus(s.PartiallyApproved)
			reviewing2 := models.TaskStatus(s.Reviewing2)

			tm[reviewing] = append(tm[reviewing], partiallyApproved)
			tm[partiallyApproved] = []models.TaskStatus{reviewing2}
			tm[reviewing2] = []models.TaskStatus{approved, rejected}
		}
	}
	return tm
}

// AllDeclaredStates returns all state names declared across all role-pairs.
func (r *Resolver) AllDeclaredStates() []models.TaskStatus {
	var states []models.TaskStatus
	for _, rp := range r.config.Pipeline.RolePairs {
		s := rp.States
		states = append(states,
			models.TaskStatus(s.Initial),
			models.TaskStatus(s.Executing),
			models.TaskStatus(s.Submitted),
			models.TaskStatus(s.Reviewing),
			models.TaskStatus(s.Approved),
			models.TaskStatus(s.Rejected),
		)
		if s.PartiallyApproved != "" {
			states = append(states, models.TaskStatus(s.PartiallyApproved))
		}
		if s.Reviewing2 != "" {
			states = append(states, models.TaskStatus(s.Reviewing2))
		}
	}
	return states
}

// SprintTerminalStates returns the states at which a task is considered complete
// for sprint purposes. For role-pairs whose approved state is the source of a
// transition (i.e., they feed into the next pipeline stage), the approved state
// is sprint-terminal. MERGED is always included as the universal terminal for
// role-pairs at the end of the pipeline.
func (r *Resolver) SprintTerminalStates() []models.TaskStatus {
	sources := r.TransitionSourcePairs()

	var states []models.TaskStatus
	for name, rp := range r.config.Pipeline.RolePairs {
		if sources[name] {
			states = append(states, models.TaskStatus(rp.States.Approved))
		}
	}

	states = append(states, models.TaskStatusMerged)

	slices.Sort(states)
	return states
}

// RolePairNames returns the sorted names of all role-pairs in the config.
func (r *Resolver) RolePairNames() []string {
	names := make([]string, 0, len(r.config.Pipeline.RolePairs))
	for name := range r.config.Pipeline.RolePairs {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// RolePair returns the definition for the named role-pair.
func (r *Resolver) RolePair(name string) (*RolePairDef, error) {
	rp, ok := r.config.Pipeline.RolePairs[name]
	if !ok {
		return nil, fmt.Errorf("unknown role-pair %q", name)
	}
	return &rp, nil
}

// DoerRole returns the doer agent-role key for the given role-pair.
func (r *Resolver) DoerRole(rolePair string) (string, error) {
	rp, ok := r.config.Pipeline.RolePairs[rolePair]
	if !ok {
		return "", fmt.Errorf("unknown role-pair %q", rolePair)
	}
	return rp.Doer, nil
}

// ReviewerRole returns the reviewer agent-role key for the given role-pair.
func (r *Resolver) ReviewerRole(rolePair string) (string, error) {
	rp, ok := r.config.Pipeline.RolePairs[rolePair]
	if !ok {
		return "", fmt.Errorf("unknown role-pair %q", rolePair)
	}
	return rp.Reviewer, nil
}

// Transition returns the transition definition for the given name.
// It searches across all sub-pipelines and pipeline-transitions.
func (r *Resolver) Transition(name string) (*TransitionDef, error) {
	for _, sp := range r.config.Pipeline.SubPipelines {
		for i := range sp.Transitions {
			if sp.Transitions[i].Name == name {
				return &sp.Transitions[i], nil
			}
		}
	}
	for i := range r.config.Pipeline.PipelineTransitions {
		if r.config.Pipeline.PipelineTransitions[i].Name == name {
			return &r.config.Pipeline.PipelineTransitions[i], nil
		}
	}
	return nil, fmt.Errorf("unknown transition %q", name)
}

// AvailableTransitions returns transition names available for a task at the given
// status, excluding transitions already executed.
func (r *Resolver) AvailableTransitions(status models.TaskStatus, transitionsExecuted map[string]bool) []string {
	var available []string
	for _, sp := range r.config.Pipeline.SubPipelines {
		for _, t := range sp.Transitions {
			if t.Trigger != "manual" {
				continue
			}
			if transitionsExecuted[t.Name] {
				continue
			}
			fromPair, fromPhase, err := parseRef(t.From)
			if err != nil {
				continue
			}
			if r.resolvePhase(fromPair, fromPhase) == status {
				available = append(available, t.Name)
			}
		}
	}
	for _, t := range r.config.Pipeline.PipelineTransitions {
		if t.Trigger != "manual" {
			continue
		}
		if transitionsExecuted[t.Name] {
			continue
		}
		if r.resolve3PartPhase(t.From) == status {
			available = append(available, t.Name)
		}
	}
	slices.Sort(available)
	return available
}

// TransitionTargetRolePair returns the target role-pair name for a transition.
// Handles both 2-part refs (sub-pipeline transitions) and 3-part refs (pipeline-transitions).
func (r *Resolver) TransitionTargetRolePair(transitionName string) (string, error) {
	t, err := r.Transition(transitionName)
	if err != nil {
		return "", err
	}
	// Try 3-part ref first (pipeline-transitions use sub-pipeline.role-pair.phase).
	_, toPair, _, err3 := parse3PartRef(t.To)
	if err3 == nil {
		return toPair, nil
	}
	// Fall back to 2-part ref (sub-pipeline transitions use role-pair.phase).
	toPair2, _, err2 := parseRef(t.To)
	if err2 != nil {
		return "", fmt.Errorf("transition %q: invalid to reference: %w", transitionName, err2)
	}
	return toPair2, nil
}

// resolvePhase returns the concrete status for a role-pair + phase combination.
func (r *Resolver) resolvePhase(rolePair, phase string) models.TaskStatus {
	rp, ok := r.config.Pipeline.RolePairs[rolePair]
	if !ok {
		return ""
	}
	switch phase {
	case "initial":
		return models.TaskStatus(rp.States.Initial)
	case "executing":
		return models.TaskStatus(rp.States.Executing)
	case "submitted":
		return models.TaskStatus(rp.States.Submitted)
	case "reviewing":
		return models.TaskStatus(rp.States.Reviewing)
	case "approved":
		return models.TaskStatus(rp.States.Approved)
	case "rejected":
		return models.TaskStatus(rp.States.Rejected)
	case "partially-approved":
		return models.TaskStatus(rp.States.PartiallyApproved)
	case "reviewing-2":
		return models.TaskStatus(rp.States.Reviewing2)
	default:
		return ""
	}
}

// resolve3PartPhase parses a 3-part ref (sub-pipeline.role-pair.phase) and
// returns the concrete status. The sub-pipeline prefix is stripped since
// role-pair names are globally unique.
func (r *Resolver) resolve3PartPhase(ref string) models.TaskStatus {
	_, rolePair, phase, err := parse3PartRef(ref)
	if err != nil {
		return ""
	}
	return r.resolvePhase(rolePair, phase)
}

// IsDeclaredState checks if a status is declared in the pipeline config.
func (r *Resolver) IsDeclaredState(status models.TaskStatus) bool {
	return slices.Contains(r.AllDeclaredStates(), status)
}

// IsTransitionSourcePair checks if a role-pair is the "from" side of any
// transition (sub-pipeline or pipeline-transitions). Such role-pairs produce
// output that feeds the next pipeline stage (e.g. planning → coding).
func (r *Resolver) IsTransitionSourcePair(rolePair string) bool {
	return r.TransitionSourcePairs()[rolePair]
}

// TransitionSourcePairs returns the set of role-pair names that are
// the "from" side of any transition. The result is cached after the first call.
func (r *Resolver) TransitionSourcePairs() map[string]bool {
	if r.transitionSources != nil {
		return r.transitionSources
	}
	sources := make(map[string]bool)
	for _, sp := range r.config.Pipeline.SubPipelines {
		for _, t := range sp.Transitions {
			fromPair, _, _ := parseRef(t.From)
			sources[fromPair] = true
		}
	}
	for _, t := range r.config.Pipeline.PipelineTransitions {
		_, fromPair, _, err := parse3PartRef(t.From)
		if err == nil {
			sources[fromPair] = true
		}
	}
	r.transitionSources = sources
	return sources
}
