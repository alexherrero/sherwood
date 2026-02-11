# Documentation Maintenance Guidelines

This document provides instructions for maintaining the Sherwood project documentation, ensuring it stays accurate and up-to-date as the codebase evolves.

## Overview

The `docs/` directory contains:

- **DESIGN.md** - Overall system design, architecture, and API reference
- **MAINTENANCE.md** - This file - guidelines for maintaining documentation
- **reviews/** - Historical codebase reviews and assessments
- **wiki/** - Public documentation including Pending and Completed features

## Maintaining Wiki Features

### When Adding Features

1. **Complexity Assessment:** Rate each feature as Low, Low-Medium, Medium, Medium-High, High, or Very High
2. **Ordering:** Insert features in complexity order (simplest first, most complex last) in `wiki/Pending-Features.md`
3. **Numbering:** Renumber all features after insertion to maintain sequential order
4. **Required Sections:**
   - Complexity level
   - Description
   - Current state/limitation
   - Implementation requirements (detailed steps)
   - Edge cases to handle (if applicable)
   - Testing requirements

### When Implementing Features

1. **Copy to Completed:** Copy the entire feature section from `wiki/Pending-Features.md` to `wiki/Completed-Features.md`
   - Add to the bottom of `wiki/Completed-Features.md` (chronological order, newest last)
   - Add completion date: `**Completed:** YYYY-MM-DD`
   - Add implementation notes if relevant
2. **Update Review Docs:** If the feature was identified in a codebase review, mark it as implemented in `docs/reviews/`
3. **Update DESIGN.md:** Add new endpoints, configuration options, or architecture changes to DESIGN.md
4. **Remove from Pending:** Delete the completed feature entirely from `wiki/Pending-Features.md`
5. **Renumber:** Update numbering for remaining features in `wiki/Pending-Features.md`

### Example Workflow

```markdown
# In wiki/Pending-Features.md - Before Implementation
## 3. Database Persistence
**Complexity:** Low-Medium
[... details ...]

# Step 1: Copy to wiki/Completed-Features.md (add to bottom)
## 3. Database Persistence
**Complexity:** Low-Medium
**Completed:** 2026-02-09
**Implementation Notes:** Added SQLite persistence for orders and positions.
[... original details ...]

# Step 2: Update review docs with ✅ IMPLEMENTED

# Step 3: Update DESIGN.md with new database schema

# Step 4: Remove from wiki/Pending-Features.md entirely

# Step 5: Renumber remaining features (4 becomes 3, 5 becomes 4, etc.)
```

## Maintaining Review Documents

### When Creating Reviews

1. **Location:** Store in `docs/reviews/` directory
2. **Naming:** Use descriptive names with dates: `codebase_review_YYYY-MM-DD.md`
3. **Format:** Include:
   - Executive summary
   - Issues/gaps identified
   - Severity levels
   - Recommendations
   - Proposed roadmap

### When Addressing Review Items

1. **Mark Completion:** Add ✅ IMPLEMENTED next to completed items
2. **Add Status Notes:** Include brief description of implementation
3. **Update Roadmap:** Mark completed phases
4. **Link to Changes:** Reference relevant commits, PRs, or artifacts

### Example

```markdown
### 5. Hardcoded Data Provider ✅ IMPLEMENTED

**Severity:** Medium
- **Issue:** ...
- **Recommendation:** ...
- **Status:** Implemented in Phase 2. `DATA_PROVIDER` environment variable with factory pattern supports yahoo, tiingo, and binance.
```

## Maintaining DESIGN.md

### Updating Design Specs

Update the relevant sections:

- **Configuration** - New environment variables
- **API Endpoints** - New routes and handlers
- **Supported Strategies** - New trading strategies
- **Technical Stack** - New dependencies or tools

### Keep Current

- Mark implemented features with completion status
- Update code examples to match actual implementation
- Maintain accuracy of API documentation
- Update architecture diagrams if structure changes

## Maintaining Wiki

The `wiki/` directory acts as the **Source of Truth** for project documentation. These files are automatically published to the GitHub Wiki by the workflow.

### Key Files

| Wiki File | Purpose |
| :--- | :--- |
| `wiki/Pending-Features.md` | Primary backlog of future work |
| `wiki/Completed-Features.md` | Chronological history of changes |
| `wiki/Backend-Setup.md` | Guide for setting up the environment |
| `wiki/Home.md` | Landing page for the Wiki |

### Automation

A GitHub Action (`.github/workflows/deploy_wiki.yml`) automatically publishes changes pushed to the `wiki/` directory.

## Best Practices

1. **Consistency:** Use consistent formatting across all documentation
2. **Dates:** Include dates in review documents for historical tracking
3. **Cross-Reference:** Link between documents when relevant
4. **Completeness:** Provide enough detail to pick up work later
5. **Clarity:** Write for future maintainers who may not have full context
6. **Verification:** Test examples and code snippets to ensure accuracy

## Automation Reminders

When working on Sherwood:

- ✅ After implementing a feature → Update `wiki/Pending-Features.md`, `wiki/Completed-Features.md`, reviews, and DESIGN.md
- ✅ After adding a feature idea → Add to `wiki/Pending-Features.md` in complexity order
- ✅ After major milestone → Consider creating new review document in `docs/reviews/`
- ✅ Before release → Verify all wiki documentation is current and accurate
