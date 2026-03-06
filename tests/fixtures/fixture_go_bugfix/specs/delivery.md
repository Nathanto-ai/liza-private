# Delivery Spec: Fix GetUser Endpoint

## Definitions / Glossary
- **UserID**: A positive integer identifying a user.
- **GetUser**: The endpoint `GET /user?id=<UserID>`.

## User Stories
As an API consumer, I want to call `GET /user?id=123` and receive a JSON response with the user's name so that I can display it in my application.

## Acceptance Criteria
- AC-1: `GET /user?id=123` returns HTTP 200 with `{"id": 123, "name": "Alice"}`.
- AC-2: `GET /user?id=0` returns HTTP 400 with `{"error": "invalid user ID"}`.
- AC-3: `GET /user?id=999` returns HTTP 404 with `{"error": "user not found"}`.

## Data & Interfaces
- Input: HTTP GET with query parameter `id` (integer).
- Output: JSON object with fields `id` (int) and `name` (string), or `error` (string).

## Constraints
- Must not break existing endpoints.
- Response Content-Type must be `application/json`.

## Verification Plan
Run `go test ./...` in the project root and check exit code 0.

## Non Goals
- Authentication or authorization.
- Database integration (use in-memory map).

## Open Questions
None.
