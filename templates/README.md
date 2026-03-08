# Templates

Templates for documents used in Liza-managed projects.

## Available Templates

| Template | Purpose | Copy To |
|----------|---------|---------|
| [vision-template.md](vision-template.md) | Goal-level vision document | `specs/vision.md` |
| [spec_delivery.md](spec_delivery.md) | Delivery specification with ACs and verification | `specs/delivery-<feature>.md` |
| [ADR/TEMPLATE.md](../specs/architecture/ADR/TEMPLATE.md) | Architecture Decision Record | `specs/architecture/ADR/ADR-NNN-title.md` |

## Usage

1. Copy the template to the target location
2. Fill in all sections
3. Remove the template instruction block at the top

## Template Triggers

### Vision Document
- Required before goal decomposition
- Missing vision → Planner BLOCKED

### ADR
Create when:
- Circuit breaker fires with ARCHITECTURE_FLAW
- Reviewer rejects for architectural reason
- Planner rescopes due to technical constraint
- New external dependency added
- Performance approach chosen
