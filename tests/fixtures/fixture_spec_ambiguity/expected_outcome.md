# Expected Outcome: fixture_spec_ambiguity

## Scenario: S3 — Ambiguous Spec

### Initial state
- Delivery spec has multiple open questions
- AC-2 and AC-3 are ambiguous ("fast enough", "standard format")
- No concrete verification plan
- No source code exists yet

### Expected system behavior
1. Spec validation should flag: ambiguous AC, vague verification plan
2. Auditor flags ambiguity (multiple open questions)
3. Planner creates a "clarify spec" task
4. System stops in NEEDS_HUMAN_DECISION

### Expected end state
- At least one task in NEEDS_HUMAN_DECISION status
- System does NOT proceed to implementation
- Findings logged explaining why human input is needed
- No code generated (spec is too ambiguous to implement)
