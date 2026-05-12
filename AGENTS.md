# Beethoven Agent Workflow

This repository is designed for structured AI-native development workflows.

---

# Core Principles

- Keep implementations simple
- Avoid unnecessary dependencies
- Prefer explicit scripts over hidden tooling
- Keep files small and understandable
- Do not modify unrelated files

---

# Runtime Rules

Agents must:

- Operate only inside the assigned worktree
- Read task instructions from run artifacts
- Save planning output to plan.md
- Save findings and critiques to review.md
- Append important commands and outputs to execution-log.md

---

# Repository Structure

## Source Code

```txt
src/
```

## Development Scripts

```txt
scripts/
```

## Prompt Templates

```txt
prompts/
```

## Workflow Definitions

```txt
workflows/
```

---

# Development Workflow

## Install Dependencies

```bash
pnpm install
```

## Start Development

```bash
pnpm dev
```

---

# Agent Constraints

Agents should:

- Avoid overengineering
- Avoid introducing frameworks unnecessarily
- Prefer deterministic implementations
- Make changes incrementally
- Keep workflows reproducible

---

# Artifact Expectations

Runs generate:

- prompt.md
- plan.md
- review.md
- execution-log.md
- metadata.json

Artifacts may also generate:

- screenshots
- reports
- previews

---

# Review Expectations

Agents should critique:

- maintainability
- simplicity
- edge cases
- unnecessary complexity
- missing documentation
