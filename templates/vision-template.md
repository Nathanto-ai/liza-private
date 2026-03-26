# Vision: [Goal Name]

> Template for goal-level vision documents in Liza projects.
> Copy this file to `specs/vision.md` and fill in the sections.

## Problem Statement

What problem are we solving? Evidence?

## Target Users

Who benefits? What are their needs?

## MVP Scope

What is IN the first deliverable?
- [ ] Capability 1
- [ ] Capability 2

## Explicit Out of Scope

What are we NOT building (yet)?
- Feature X (post-MVP)
- Integration Y (not needed for MVP)

## Success Criteria

How do we know we succeeded?

## Risks and Assumptions

What could go wrong? What are we assuming?

## Verification Strategy (optional)

> Define project-level verification commands that apply to all tasks.
> These are used as `verify_commands` on tasks and run automatically during merge.

```yaml
# Example:
verify_commands:
  - "go test ./..."
  - "go vet ./..."
```

---

**Rules:**
- Planner cannot decompose goal without vision document. Missing vision → BLOCKED at planning stage.
- Tasks created from this vision must cite requirement IDs from the delivery spec (when `enforce_requirement_refs` is enabled).
