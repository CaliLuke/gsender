---
name: gemini-code-review
description: Guidance for running external code reviews with Gemini CLI using git diffs and structured prompts before commit.
---

# Gemini Code Review

Trigger a code review of your recent changes using Google Gemini CLI as an external reviewer. Use this after completing a task to get a second opinion before committing.

## When to Use

- After completing a multi-file fix or feature
- Before committing changes that touch core logic (graph layer, auth, transport)
- When the user says "review with gemini", "get a second opinion", or "code review"

## How to Invoke Gemini

The CLI is available as `gemini` (aliased to `npx @google/gemini-cli`). Pipe `git diff` into it with a `-p` prompt flag:

```bash
git diff HEAD | gemini -p "YOUR REVIEW PROMPT HERE" 2>&1
```

Run this via the Bash tool. It takes 30-90 seconds typically. Use `timeout` of 120000ms.

**Important:** Gemini outputs loading/extension messages to stderr before the actual review. The review content comes at the end of the output.

## Building the Review Prompt

The prompt must contain three sections:

### 1. Background (What was the bug/task?)

Explain the root cause concisely. Include enough domain context that a reviewer unfamiliar with the codebase can understand WHY the change was needed. Example:

> TypeDB 3.8 uses static type inference. When a match clause uses an abstract supertype, TypeDB considers ALL subtypes during validation. If a write clause references an attribute only owned by some subtypes, TypeDB rejects the query.

### 2. The Fix (What was changed and why?)

Enumerate the key changes as a numbered list. Focus on the design decisions, not line-by-line diffs (the diff itself is piped in). Example:

> 1. Added `ResolveAttrEntityType` — generated function that validates attribute ownership
> 2. Removed untyped wrappers that defaulted to the abstract supertype
> 3. Fixed `buildEdgeDelete` to require concrete types

### 3. What to Check (Specific review instructions)

Give Gemini concrete things to verify. This is the most important section — without it, you get generic feedback. Structure as a numbered checklist:

> 1. Search for any remaining `TypeArtifact` usage in write queries
> 2. Verify `ResolveAttrEntityType` binary search works on sorted lists
> 3. Check all callers of `buildEdgeDelete` resolve concrete types first
> 4. Verify removed type guards don't create new failure modes
> 5. Check if `_stub.go` files need updating

## Template

```bash
git diff HEAD | gemini -p "## Code Review: [TICKET] — [Short Description]

### Background
[2-4 sentences explaining the root cause and domain context]

### The Fix
[Numbered list of key changes and design decisions]

### What to Check
[Numbered checklist of specific things to verify — completeness, correctness, regressions, edge cases, test coverage, stub files]

Please review the diff thoroughly and report any issues, missed spots, or suggestions." 2>&1
```

## After the Review

1. Read Gemini's output and summarize findings to the user
2. Categorize findings as: **bugs** (must fix), **suggestions** (worth considering), **style** (optional)
3. If Gemini found real issues, fix them before committing
4. If all findings are minor/style, tell the user and let them decide
