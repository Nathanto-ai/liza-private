# Vision: User API Service

A minimal HTTP API that returns user data. The service must validate input, return proper JSON, and handle errors gracefully.

## Problem Statement
The GetUser endpoint currently does not validate input or return proper error codes, leading to 500 errors on invalid requests and missing users.

## Target Users
Backend API consumers (frontend apps, mobile clients) that call `GET /user?id=<id>`.

## MVP Scope
Fix the GetUser endpoint to: validate the user ID parameter (return 400), handle missing users (return 404), and return 200 with correct JSON for valid requests.

## Explicit Out of Scope
- Authentication / authorization
- Database integration (use in-memory data)
- Other endpoints besides GetUser

## Success Criteria
- All endpoints return valid JSON
- Input validation rejects invalid requests with 400
- Valid requests return 200 with correct schema

## Risks and Assumptions
- Assumes in-memory user store is sufficient for MVP.
- Risk: if user IDs become non-integer, the validation logic needs rework.
