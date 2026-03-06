# Delivery Spec: [Feature Name]

> Template for delivery specifications in Liza projects.
> Copy this file to `specs/delivery-<feature>.md` and fill in all sections.
> All sections are required unless marked (optional).

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

## Constraints

- Performance: [e.g., P95 latency < 300ms]
- Security: [e.g., all inputs sanitized]
- Compatibility: [e.g., Go 1.25+]

## Verification Plan

How will correctness be verified?

- [ ] Unit tests for [component]
- [ ] Integration tests for [workflow]
- [ ] `make test` passes
- [ ] `go vet ./...` clean

## Non Goals

What is explicitly NOT in scope for this delivery.

- [Feature X] (deferred to next sprint)

## Open Questions

Unresolved questions that may affect implementation.

- [ ] Question 1?
- [ ] Question 2?

---

**Rule:** Tasks referencing this spec cannot enter IMPLEMENTING without at least one acceptance criterion and one verification command.
