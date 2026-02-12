# Documentation Maintenance Guidelines

This document provides instructions for maintaining the Sherwood project documentation, ensuring it stays accurate and up-to-date as the codebase evolves.

## Overview

The `docs/` directory contains:

- **DESIGN.md** - Overall system design, architecture, and API reference
- **MAINTENANCE.md** - This file - guidelines for maintaining documentation
- **reviews/** - Historical codebase reviews and assessments
- **wiki/** - Public documentation including Roadmap, Completed Features, and Ideas

## Maintaining the Roadmap

The roadmap (`wiki/Roadmap.md`) is the primary backlog for Sherwood. Features are organized into three priority buckets:

| Bucket | Meaning | Typical Complexity |
| :----- | :------ | :----------------- |
| 🟢 **Now** | Ready to pick up immediately | Low – Medium |
| 🟡 **Soon** | Next up after Now items are cleared | Medium – Medium-High |
| 🔴 **Later** | Requires significant design/effort | High – Very High |

### When Adding Features to the Roadmap

1. **Bucket Assignment:** Choose the appropriate bucket (Now, Soon, Later) based on complexity and readiness
2. **Numbering:** Features are numbered sequentially across all buckets (1, 2, 3, ...)
3. **Required Sections:**
   - Complexity level
   - Description
   - Current state/limitation
   - Implementation requirements (detailed steps)
   - Edge cases to handle (if applicable)
   - Testing requirements
4. **Update the Overview:** Update the Mermaid diagram and summary table at the top of the Roadmap
5. **Renumber:** Renumber all features after insertion to maintain sequential order

### When Promoting Ideas to the Roadmap

1. **Refine the idea** into a full feature spec (see Required Sections above)
2. **Choose a bucket** based on complexity and readiness
3. **Add to `wiki/Roadmap.md`** under the appropriate bucket section
4. **Optionally note** in `wiki/Ideas.md` that the idea has been promoted

### When Implementing Features

1. **Copy to Completed:** Copy the entire feature section from `wiki/Roadmap.md` to `wiki/Completed-Features.md`
   - Add to the bottom of `wiki/Completed-Features.md` (chronological order, newest last)
   - Add completion date: `**Completed:** YYYY-MM-DD`
   - Add implementation notes if relevant
2. **Update Review Docs:** If the feature was identified in a codebase review, mark it as implemented in `docs/reviews/`
3. **Update DESIGN.md:** Add new endpoints, configuration options, or architecture changes to DESIGN.md
4. **Remove from Roadmap:** Delete the completed feature entirely from `wiki/Roadmap.md`
5. **Renumber & Update Overview:** Renumber remaining features and update the Mermaid diagram and summary table

### When Re-prioritizing Features

Features can be moved between buckets as priorities shift:

1. **Move** the feature section to the new bucket in `wiki/Roadmap.md`
2. **Renumber** all features to maintain sequential order
3. **Update** the Mermaid diagram and summary table at the top

### Example Workflow

```markdown
# In wiki/Roadmap.md - Before Implementation
## 🟢 Now
### 1. Edge Case Test Coverage
**Complexity:** Low-Medium
[... details ...]

# Step 1: Copy to wiki/Completed-Features.md (add to bottom)
## Edge Case Test Coverage
**Complexity:** Low-Medium
**Completed:** 2026-02-15
**Implementation Notes:** Added 45 new table-driven tests for validation edge cases.
[... original details ...]

# Step 2: Update review docs with ✅ IMPLEMENTED

# Step 3: Update DESIGN.md with any new architecture details

# Step 4: Remove from wiki/Roadmap.md entirely

# Step 5: Renumber remaining features, update Mermaid diagram and summary table
```

## Maintaining Ideas

The `wiki/Ideas.md` file captures brainstorms and explorations not yet committed to the roadmap.

- **Adding ideas:** Append new ideas at the bottom with date, description, and areas to explore
- **Promoting ideas:** When ready, refine into a full feature spec and add to the Roadmap (see above)
- **Clean up after promotion:** Remove the promoted idea from `wiki/Ideas.md` and renumber remaining ideas

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
| `wiki/Roadmap.md` | Primary backlog — Now / Soon / Later |
| `wiki/Completed-Features.md` | Chronological history of changes |
| `wiki/Ideas.md` | Early-stage brainstorms and explorations |
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

- ✅ After implementing a feature → Update `wiki/Roadmap.md`, `wiki/Completed-Features.md`, reviews, and DESIGN.md
- ✅ After adding a planned feature → Add to `wiki/Roadmap.md` in the appropriate bucket
- ✅ After brainstorming a new idea → Add to `wiki/Ideas.md`
- ✅ After major milestone → Consider creating new review document in `docs/reviews/`
- ✅ Before release → Verify all wiki documentation is current and accurate
