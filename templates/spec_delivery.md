# Delivery Spec: [Feature Name]

> Template for delivery specifications in Liza projects.
> Copy this file to `specs/delivery-<feature>.md` and fill in all sections.
> All sections are required unless marked (optional).

## Requirement References

> Each requirement gets a short ID (R1, R2, ...) used for traceability.
> Tasks created from this spec MUST cite at least one requirement ID in `requirement_refs`.
> The quality gate rejects tasks entering IMPLEMENTING without requirement refs (when `enforce_requirement_refs` is enabled).

| ID | Requirement |
|----|-------------|
| R1 | [Short description of requirement 1] |
| R2 | [Short description of requirement 2] |

## Definitions / Glossary

| Term | Definition |
|------|-----------|
| Term 1 | Definition of term 1 |

## User Stories

- As a [role], I want [capability], so that [benefit].

## Acceptance Criteria

> Use Given/When/Then format for each criterion.

### AC-1: [Criterion Name]

Given: [precondition]
When: [action]
Then: [expected result]

### AC-2: [Criterion Name]

Given: [precondition]
When: [action]
Then: [expected result]

## Data & Interfaces

Describe data models, API contracts, input/output formats.

## Error Behavior

Define expected error handling for each component. This maps to the `error_behavior` field on tasks.

- [Component]: [e.g., Return typed errors, never panic. Wrap with context.]
- [External calls]: [e.g., Retry 3x with backoff, then surface error to caller.]

## Constraints

- Performance: [e.g., P95 latency < 300ms]
- Security: [e.g., all inputs sanitized]
- Compatibility: [e.g., Go 1.25+, Windows + Linux]

## Verification Plan

How will correctness be verified? Each verification item should map to a `verify_commands` entry on the corresponding task.

- [ ] Unit tests for [component]
- [ ] Integration tests for [workflow]
- [ ] `go test ./...` passes
- [ ] `go vet ./...` clean

### Verify Commands (for tasks)

> These are the executable commands that Liza runs during merge to gate integration.
> Each task MUST have at least one `verify_commands` entry — the quality gate rejects tasks without them.
> Commands must be cross-platform or use platform-appropriate alternatives.

```yaml
# Example verify_commands for Go tasks:
verify_commands:
  - "go test ./path/to/package/..."
  - "go vet ./path/to/package/..."

# Example verify_commands for Python tasks:
verify_commands:
  - "pytest tests/test_feature.py -v"
```

## Non Goals

What is explicitly NOT in scope for this delivery.

- [Feature X] (deferred to next sprint)

## Open Questions

Unresolved questions that may affect implementation.

- [ ] Question 1?
- [ ] Question 2?

## Audit Traceability (optional)

If this spec was created to remediate an audit finding, record the traceability:

- Origin finding ID: [e.g., `finding-001`]
- Origin task ID: [e.g., `original-task-id`]
- Finding type: [SPEC_MISMATCH | MISSING_TEST | QUALITY_ISSUE | SECURITY_CONCERN]

---

**Rules:**
- Tasks referencing this spec cannot enter IMPLEMENTING without at least one acceptance criterion and one verification command (quality gate enforced).
- When `enforce_requirement_refs` is enabled in config, tasks also require at least one requirement reference.
- Remediation tasks MUST set `origin_finding_id` to link back to the audit finding.
