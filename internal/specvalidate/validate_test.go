package specvalidate

import (
	"testing"
)

func TestValidateSpecFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		content      string
		specType     SpecType
		wantValid    bool
		wantMissing  []string
		wantWarnings []string
	}{
		{
			name: "valid delivery spec",
			content: `# Delivery Spec

## Definitions / Glossary
| Term | Definition |
|------|-----------|
| API | Application Programming Interface |

## User Stories
- As a user, I want to search leads.

## Acceptance Criteria

### AC-1: Search
Given: a valid request
When: the search endpoint is called
Then: results are returned

## Data & Interfaces
JSON API with REST endpoints.

## Constraints
- P95 latency < 300ms

## Verification Plan
- Unit tests pass
- Integration tests pass

## Non Goals
- UI dashboard

## Open Questions
- None at this time
`,
			specType:  SpecTypeDelivery,
			wantValid: true,
		},
		{
			name: "delivery spec missing acceptance criteria",
			content: `# Delivery Spec

## Definitions / Glossary
Terms here.

## User Stories
Stories here.

## Data & Interfaces
Interfaces here.

## Constraints
Constraints here.

## Verification Plan
Plan here.

## Non Goals
None.

## Open Questions
None.
`,
			specType:    SpecTypeDelivery,
			wantValid:   false,
			wantMissing: []string{"Acceptance Criteria"},
		},
		{
			name: "delivery spec missing verification plan",
			content: `# Delivery Spec

## Definitions / Glossary
Terms.

## User Stories
Stories.

## Acceptance Criteria
Given: input
When: action
Then: output

## Data & Interfaces
Interfaces.

## Constraints
Constraints.

## Non Goals
None.

## Open Questions
None.
`,
			specType:    SpecTypeDelivery,
			wantValid:   false,
			wantMissing: []string{"Verification Plan"},
		},
		{
			name: "delivery spec with empty section warns",
			content: `# Delivery Spec

## Definitions / Glossary

## User Stories
Stories.

## Acceptance Criteria
Given: x
When: y
Then: z

## Data & Interfaces
Interfaces.

## Constraints
Constraints.

## Verification Plan
Plan.

## Non Goals
None.

## Open Questions
None.
`,
			specType:     SpecTypeDelivery,
			wantValid:    true,
			wantWarnings: []string{`section "Definitions / Glossary" is empty`},
		},
		{
			name: "delivery spec without given/when/then warns",
			content: `# Delivery Spec

## Definitions / Glossary
Terms.

## User Stories
Stories.

## Acceptance Criteria
The system should work correctly.

## Data & Interfaces
Interfaces.

## Constraints
Constraints.

## Verification Plan
Plan.

## Non Goals
None.

## Open Questions
None.
`,
			specType:     SpecTypeDelivery,
			wantValid:    true,
			wantWarnings: []string{"Acceptance Criteria section has no Given/When/Then blocks"},
		},
		{
			name: "valid vision spec",
			content: `# Vision

## Problem Statement
We need a better system.

## Target Users
Developers.

## MVP Scope
- Feature 1
- Feature 2

## Explicit Out of Scope
- Feature 3

## Success Criteria
Tests pass.

## Risks and Assumptions
Assumes Go 1.25+.
`,
			specType:  SpecTypeVision,
			wantValid: true,
		},
		{
			name:        "empty file fails",
			content:     "",
			specType:    SpecTypeDelivery,
			wantValid:   false,
			wantMissing: []string{"Definitions / Glossary", "User Stories", "Acceptance Criteria", "Data & Interfaces", "Constraints", "Verification Plan", "Non Goals", "Open Questions"},
		},
		{
			name: "vision spec missing problem statement",
			content: `# Vision

## Target Users
Developers.

## MVP Scope
Features.

## Explicit Out of Scope
None.

## Success Criteria
Tests pass.

## Risks and Assumptions
None.
`,
			specType:    SpecTypeVision,
			wantValid:   false,
			wantMissing: []string{"Problem Statement"},
		},
		{
			name: "delivery spec with requirement IDs",
			content: `# Delivery Spec

## Definitions / Glossary
Terms.

## Requirements
- R1: Must do X
- R2: Must do Y

## User Stories
Stories.

## Acceptance Criteria

### AC-1 (R1): Feature X
Given: x
When: y
Then: z

## Data & Interfaces
Interfaces.

## Constraints
Constraints.

## Verification Plan
` + "```bash\ngo test ./...\n```" + `

## Non Goals
None.

## Open Questions
- None at this time
`,
			specType:  SpecTypeDelivery,
			wantValid: true,
		},
		{
			name: "delivery spec requirement section without IDs warns",
			content: `# Delivery Spec

## Definitions / Glossary
Terms.

## Requirements
The system must be fast and reliable.

## User Stories
Stories.

## Acceptance Criteria
Given: x
When: y
Then: z

## Data & Interfaces
Interfaces.

## Constraints
Constraints.

## Verification Plan
` + "```bash\ngo test ./...\n```" + `

## Non Goals
None.

## Open Questions
None.
`,
			specType:     SpecTypeDelivery,
			wantValid:    true,
			wantWarnings: []string{"Requirements section has no requirement IDs (expected R1, R2, ...)"},
		},
		{
			name: "delivery spec AC IDs without requirement linkage warns",
			content: `# Delivery Spec

## Definitions / Glossary
Terms.

## Requirements
- R1: Feature X

## User Stories
Stories.

## Acceptance Criteria
AC-1: Feature X
Given: x
When: y
Then: z

## Data & Interfaces
Interfaces.

## Constraints
Constraints.

## Verification Plan
` + "```bash\ngo test ./...\n```" + `

## Non Goals
None.

## Open Questions
None.
`,
			specType:     SpecTypeDelivery,
			wantValid:    true,
			wantWarnings: []string{"Acceptance Criteria has AC IDs but no requirement linkage (expected AC-1 (R1) format)"},
		},
		{
			name: "delivery spec verification plan without commands warns",
			content: `# Delivery Spec

## Definitions / Glossary
Terms.

## Requirements
- R1: Feature

## User Stories
Stories.

## Acceptance Criteria

### AC-1 (R1): Feature
Given: x
When: y
Then: z

## Data & Interfaces
Interfaces.

## Constraints
Constraints.

## Verification Plan
Run the tests and check the output manually.

## Non Goals
None.

## Open Questions
None.
`,
			specType:     SpecTypeDelivery,
			wantValid:    true,
			wantWarnings: []string{"Verification Plan has no executable commands or code blocks (expected verify_commands)"},
		},
		{
			name: "delivery spec verification plan with go test accepted",
			content: `# Delivery Spec

## Definitions / Glossary
Terms.

## Requirements
- R1: Feature

## User Stories
Stories.

## Acceptance Criteria

### AC-1 (R1): Feature
Given: x
When: y
Then: z

## Data & Interfaces
Interfaces.

## Constraints
Constraints.

## Verification Plan
go test ./internal/foo/...

## Non Goals
None.

## Open Questions
None.
`,
			specType:  SpecTypeDelivery,
			wantValid: true,
		},
		{
			name: "delivery spec open questions with blocking item warns",
			content: `# Delivery Spec

## Definitions / Glossary
Terms.

## Requirements
- R1: Feature

## User Stories
Stories.

## Acceptance Criteria

### AC-1 (R1): Feature
Given: x
When: y
Then: z

## Data & Interfaces
Interfaces.

## Constraints
Constraints.

## Verification Plan
` + "```bash\ngo test ./...\n```" + `

## Non Goals
None.

## Open Questions
- This question is a BLOCKER for implementation
`,
			specType:     SpecTypeDelivery,
			wantValid:    true,
			wantWarnings: []string{"Open Questions section may contain blocking items — resolve before IMPLEMENTING"},
		},
		{
			name: "delivery spec open questions none is safe",
			content: `# Delivery Spec

## Definitions / Glossary
Terms.

## Requirements
- R1: Feature

## User Stories
Stories.

## Acceptance Criteria

### AC-1 (R1): Feature
Given: x
When: y
Then: z

## Data & Interfaces
Interfaces.

## Constraints
Constraints.

## Verification Plan
` + "```bash\ngo test ./...\n```" + `

## Non Goals
None.

## Open Questions
None blocking.
`,
			specType:  SpecTypeDelivery,
			wantValid: true,
		},
		{
			name: "no requirements section warns",
			content: `# Delivery Spec

## Definitions / Glossary
Terms.

## User Stories
Stories.

## Acceptance Criteria
Given: x
When: y
Then: z

## Data & Interfaces
Interfaces.

## Constraints
Constraints.

## Verification Plan
` + "```bash\ngo test ./...\n```" + `

## Non Goals
None.

## Open Questions
None.
`,
			specType:     SpecTypeDelivery,
			wantValid:    true,
			wantWarnings: []string{"no Requirements section found (recommended for traceability)"},
		},
		{
			name: "delivery spec multiple sections missing",
			content: `# Delivery Spec

## User Stories
Stories.

## Acceptance Criteria
Given: x
When: y
Then: z
`,
			specType:    SpecTypeDelivery,
			wantValid:   false,
			wantMissing: []string{"Definitions / Glossary", "Data & Interfaces", "Constraints", "Verification Plan", "Non Goals", "Open Questions"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := ValidateSpecFile(tt.content, tt.specType)

			if result.Valid != tt.wantValid {
				t.Errorf("Valid = %v, want %v (missing: %v)", result.Valid, tt.wantValid, result.Missing)
			}

			if tt.wantMissing != nil {
				if len(result.Missing) != len(tt.wantMissing) {
					t.Errorf("Missing sections = %v, want %v", result.Missing, tt.wantMissing)
				} else {
					for i, m := range tt.wantMissing {
						if result.Missing[i] != m {
							t.Errorf("Missing[%d] = %q, want %q", i, result.Missing[i], m)
						}
					}
				}
			}

			if tt.wantWarnings != nil {
				if len(result.Warnings) < len(tt.wantWarnings) {
					t.Errorf("Warnings = %v, want at least %v", result.Warnings, tt.wantWarnings)
				} else {
					for _, want := range tt.wantWarnings {
						found := false
						for _, got := range result.Warnings {
							if got == want {
								found = true
								break
							}
						}
						if !found {
							t.Errorf("expected warning %q not found in %v", want, result.Warnings)
						}
					}
				}
			}
		})
	}
}
