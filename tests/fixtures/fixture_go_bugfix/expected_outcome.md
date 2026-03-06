# Expected Outcome: fixture_go_bugfix

## Scenario: S1 — Happy Path + S2 — Spec Mismatch

### Initial state
- `go test ./...` fails (2 of 3 tests fail: TestGetUser_InvalidID_Zero, TestGetUser_NotFound)
- Delivery spec is complete and unambiguous

### Expected system behavior
1. Planner decomposes spec into task(s) to fix the handler
2. Coder identifies the 3 bugs (missing 400 for id<=0, missing 404 for not-found, missing 400 for non-numeric)
3. Verifier runs `go test ./...` — initially FAIL
4. After fix: verifier runs `go test ./...` — PASS
5. Auditor reviews and may flag AC coverage gaps (AC-2 boundary conditions)
6. System reaches PASS

### Expected end state
- All 3 tests pass
- Task status: MERGED
- No NEEDS_HUMAN_DECISION (spec is clear)
