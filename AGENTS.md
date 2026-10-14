# AGENTS.md

Instructions for coding agents working in this repository.

## Committing

When you finish your work, commit it using [Conventional Commits](https://www.conventionalcommits.org/):

```
type(scope): description
```

- **type**: the kind of change: `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `build`, `ci`, `chore`,
  `style` or `revert`.
- **scope**: the part of the codebase it touches, usually a package or area: `session`, `sandbox`, `agent`,
  `store`, `api`, `tools`, `automation`, `github`, `cli`, `web`, `repo`.
- **description**: what the change does, in the imperative mood, lowercase, without a trailing period.

Examples:

```
feat(session): open pull requests per repository
fix(sandbox): detect missing containers on Docker 29
docs(repo): document environment secrets
```

Keep unrelated changes in separate commits, each with its own type and scope.
