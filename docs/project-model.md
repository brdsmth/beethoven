# Beethoven Project Model Specification

## Purpose

Introduce Projects as a first-class domain object.

Projects become the root organizational boundary for:

* goals
* initiatives
* epics
* tasks
* decisions
* inbox items
* workspaces
* repositories
* events

This allows a single Beethoven installation to manage multiple software projects simultaneously.

---

# Motivation

Current hierarchy:

```text
Goal
  ↓
Task
```

This does not scale beyond a single project.

Desired hierarchy:

```text
Project
  ↓
Initiative
  ↓
Epic
  ↓
Task
  ↓
Subtask
```

All execution occurs within a Project context.

---

# Project Domain Model

```rust
pub struct Project {
    pub id: ProjectId,
    pub name: String,
    pub slug: String,

    pub repository_path: PathBuf,

    pub default_branch: String,

    pub status: ProjectStatus,

    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}
```

---

# Project Status

```rust
pub enum ProjectStatus {
    Active,
    Archived,
}
```

---

# Repository Mapping

Each Project maps to exactly one repository.

Examples:

```text
Punch
  -> ~/workspace/punch

Sailboat Marketplace
  -> ~/workspace/sailboat

Beethoven
  -> ~/workspace/beethoven
```

The repository path becomes the root for:

* git operations
* worktree creation
* project memory
* project artifacts

---

# Database Changes

## projects

```sql
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    repository_path TEXT NOT NULL,
    default_branch TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
```

---

# Project Foreign Keys

Add project_id to:

## goals

```sql
ALTER TABLE goals
ADD COLUMN project_id TEXT NOT NULL;
```

## tasks

```sql
ALTER TABLE tasks
ADD COLUMN project_id TEXT NOT NULL;
```

## inbox_items

```sql
ALTER TABLE inbox_items
ADD COLUMN project_id TEXT NOT NULL;
```

## workspaces

```sql
ALTER TABLE workspaces
ADD COLUMN project_id TEXT NOT NULL;
```

## events

```sql
ALTER TABLE events
ADD COLUMN project_id TEXT NOT NULL;
```

---

# Event Changes

Every event must be associated with a project.

Example:

```rust
pub struct Event {
    pub id: EventId,
    pub project_id: ProjectId,

    pub event_type: String,

    pub actor_id: Option<ActorId>,

    pub payload: Value,

    pub timestamp: DateTime<Utc>,
}
```

---

# Workspace Changes

Current:

```text
workspace/
```

New:

```text
~/.beethoven/projects/

├── punch/
│   ├── workspaces/
│   ├── memory/
│   └── artifacts/
│
├── sailboat/
│   ├── workspaces/
│   ├── memory/
│   └── artifacts/
│
└── beethoven/
```

Project becomes the root boundary.

---

# Git Worktree Changes

Worktrees must be created beneath the owning project.

Example:

```text
Punch

Repository:
~/workspace/punch

Worktrees:
~/.beethoven/projects/punch/workspaces/
```

Example branch:

```text
task/implement-auth
```

---

# Active Project Concept

Introduce current project state.

Location:

```text
~/.beethoven/current-project
```

Stores:

```text
project_id
```

This functions similarly to git's current branch.

---

# CLI Changes

## Create Project

```bash
beethoven project create \
  punch \
  --repo ~/workspace/punch \
  --default-branch main
```

---

## List Projects

```bash
beethoven project list
```

Example:

```text
* punch
  sailboat
  beethoven
```

---

## Show Project

```bash
beethoven project show punch
```

---

## Switch Active Project

```bash
beethoven project use punch
```

---

## Current Project

```bash
beethoven project current
```

Output:

```text
punch
```

---

# Command Context

Commands should default to the active project.

Example:

```bash
beethoven status
```

Equivalent to:

```bash
beethoven status --project punch
```

if punch is active.

---

# Explicit Project Override

Any command may override project context.

Example:

```bash
beethoven status --project sailboat

beethoven inbox --project punch

beethoven run --project beethoven
```

---

# Status Output

Example:

```text
Project: Punch

Goals: 4
Tasks: 17
Blocked: 2
Inbox: 1

Agents Active: 3
```

---

# Agent Behavior

Agents remain global.

Example:

```text
PM Agent

Backend Agent

Frontend Agent

QA Agent
```

Agents operate inside a Project context.

Example:

```bash
beethoven run punch
```

Runtime execution receives:

```rust
RunContext {
    project_id,
    repository_path,
}
```

Agents never execute without project context.

---

# Future Project Features

Phase 2:

* project-specific memories
* project-specific documentation
* project-specific knowledge graph

Phase 3:

* project dependencies
* cross-project initiatives
* shared libraries

---

# Migration Plan

1. Create projects table
2. Add project_id to existing entities
3. Create default project for existing installations
4. Associate all existing records with default project
5. Require project_id on all new events
6. Add project CLI commands
7. Add active project context

---

# Acceptance Criteria

A single Beethoven installation can manage:

```text
Punch
Sailboat Marketplace
Beethoven
Personal Website
```

without mixing:

* tasks
* goals
* inbox items
* workspaces
* events

between projects.

Projects become the primary organizational boundary of the system.
