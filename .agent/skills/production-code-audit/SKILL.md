---
name: Production Code Audit
description: A comprehensive audit skill to ensure the codebase is production-ready, secure, performant, and maintainable.
---

# Production Code Audit Skill

## Overview

This skill enables the agent to autonomously analyze the Sherwood codebase (Golang backend, React/TS frontend) to identify architectural flaws, security vulnerabilities, performance bottlenecks, and code quality issues. The goal is to elevate the codebase from a functional prototype to a robust, enterprise-grade production system.

## When to Use This Skill

- When the user asks to "audit the codebase".
- When preparing for a major release or deployment.
- When the user asks to "make this production-ready".
- When identifying technical debt or refactoring opportunities.
- When ensuring compliance with security and performance standards.

## The Process

### Phase 1: Autonomous Discovery

1. **Map the Codebase**:
    - Use `find_by_name` and `list_dir` to understand the project structure.
    - Identify key directories: `backend/`, `frontend/`, `docs/`, `.github/workflows/`.
2. **Analyze Dependencies**:
    - Review `go.mod` for backend dependencies.
    - Review `package.json` for frontend dependencies.
3. **Understand Architecture**:
    - Read `docs/DESIGN.md` to understand the intended architecture.
    - Trace the flow of data from API endpoints to database queries.

### Phase 2: Comprehensive Audit

#### 1. Architecture & Design

- **Separation of Concerns**: Ensure business logic is decoupled from API handlers and database access.
- **Circular Dependencies**: Identify and flag any circular imports in Go packages.
- **God Objects**: Detect large files (>500 lines) or structs with too many responsibilities.
- **Configuration**: Verify that no configuration is hardcoded; use environment variables or config files.

#### 2. Security

- **Secret Management**: Scan for hardcoded API keys, passwords, or tokens.
- **Input Validation**: Check for missing validation on API request structs (using `go-playground/validator`).
- **SQL Injection**: Ensure all database queries use parameterized statements (`sqlx`, `gorm`, or standard `database/sql` placeholders).
- **Authentication/Authorization**: Verify that sensitive endpoints are protected by middleware.
- **CORS**: Check for overly permissive CORS policies in the backend.

#### 3. Performance

- **Database Queries**: Identify N+1 query problems and missing indexes on foreign keys.
- **Concurrency**: Check for proper usage of Goroutines and Channels; avoid race conditions.
- **Frontend Optimization**: Look for large bundle sizes, unoptimized images, and excessive re-renders in React components.
- **Caching**: Identify opportunities for caching frequently accessed data (e.g., Redis or in-memory).

#### 4. Code Quality & Standards

- **Error Handling**: Ensure all errors are explicitly checked and handled (no `_` for errors).
- **Logging**: Verify that structured logging is used (e.g., `zap`, `logrus`) with appropriate levels.
- **Comments**: Check for Go-style docstrings on exported functions and types.
- **Linting**: Run `golangci-lint` or check for common linting errors.
- **Testing**:
  - verify test coverage is >80%.
  - check for the presence of integration tests.
  - identify flaky tests or tests with no assertions.

#### 5. Production Readiness

- **CI/CD**: Verify GitHub Actions workflows for testing, linting, and deployment.
- **Documentation**: Ensure `README.md`, `DESIGN.md`, and API docs are up-to-date.
- **Health Checks**: Check for the existence of `/health` or `/status` endpoints.
- **Graceful Shutdown**: Ensure the application handles termination signals (SIGINT, SIGTERM) gracefully.

### Phase 3: Reporting

1. **Generate Report**:
    - Create a new file in `docs/audits/audit_report_YYYY-MM-DD.md`.
    - Summarize findings by category (Critical, High, Medium, Low).
    - Provide specific file paths and line numbers for issues.
2. **Action Plan**:
    - Propose a prioritized list of remediation steps.
    - Estimate the effort required for each fix.

### Phase 4: Remediation (Interactive)

- Ask the user which issues they would like to address immediately.
- Use `replace_file_content` or `multi_replace_file_content` to apply fixes.
- Verification: Run tests after fixes to ensure no regressions.

## Production Audit Checklist

### Security

- [ ] No hardcoded secrets (API keys, DB creds) in code.
- [ ] Input validation applied to all public API endpoints.
- [ ] Parameterized SQL queries used everywhere.
- [ ] CORS configured correctly (allow specific origins).
- [ ] HTTPS enforcement (headers, config).

### Performance

- [ ] Database indexes on all foreign keys and frequently queried columns.
- [ ] No obvious N+1 query patterns in loops.
- [ ] React components utilize `useMemo` and `useCallback` appropriately.
- [ ] Static assets are optimized/compressed.

### Code Quality

- [ ] `gofmt` applied to all Go files.
- [ ] No ignored errors (`_` assignments for error returns).
- [ ] Exported functions have comments.
- [ ] Test coverage is adequate (>80%).
- [ ] No "magic numbers" or hardcoded strings; use constants.

### reliability

- [ ] Structured logging implemented.
- [ ] Graceful shutdown logic in place.
- [ ] Health check endpoint exists.
- [ ] CI/CD pipeline runs tests on every push.

## Example Usage

**User:** "Run a production audit on the backend."

**Agent:**

1. Scans `backend/` directory.
2. Checks `go.mod` and validation logic.
3. Identifies missing error checks in `backend/api/handlers.go`.
4. Flags a potential N+1 query in `backend/services/order_service.go`.
5. Creates `docs/audits/audit_report_2023-10-27.md` with findings.
6. Asks user: "I found 3 critical issues. Should I fix the missing error checks first?"
