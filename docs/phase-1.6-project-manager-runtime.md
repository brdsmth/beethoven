# Beethoven Phase 1.6: Project Manager Runtime

## Objective

Transform the Project Manager from a task generator into a true software project manager.

Current:

Project -> Goal -> Task -> Execution

Target:

Project
  -> Initiative
  -> Requirements
  -> Plan
  -> Epics
  -> Tasks
  -> Subtasks
  -> Execution

The PM becomes responsible for planning, decomposition, prioritization, dependency management, and decision management.

---

## Why This Phase Exists

Current Beethoven can execute work.

It cannot yet manage a project.

The PM effectively performs:

Goal -> Task List

A real PM must perform:

Discovery
-> Requirements
-> Planning
-> Prioritization
-> Delegation
-> Monitoring
-> Review

---

## Scope

### In Scope

- Initiatives
- Requirements
- Plans
- Epics
- Decisions
- PM lifecycle
- Planning workflows

### Out of Scope

- Real LLM execution
- HTTP API
- Tauri UI
- Agent memory

---

## New Domain Objects

### Initiative

Top-level objective within a project.

Example:

Build Gmail Agent

Fields:

- id
- project_id
- title
- description
- status

---

### Requirement

Business or technical requirement.

Examples:

- Support PDF attachments
- Route emails by company

Fields:

- id
- initiative_id
- description

---

### Plan

Generated execution strategy.

Examples:

- Backend
- Frontend
- Infrastructure

Fields:

- id
- initiative_id
- summary

---

### Epic

Large work stream.

Examples:

- Email Ingestion
- Attachment Processing
- Routing

Fields:

- id
- initiative_id
- title

---

### Decision

Tracks architectural and product decisions.

Examples:

- Use OAuth
- Use Neon
- Use pgvector

Fields:

- id
- initiative_id
- title
- context
- status

---

## New Hierarchy

Current:

Project
-> Goal
-> Task

New:

Project
-> Initiative
-> Requirement
-> Plan
-> Epic
-> Task
-> Subtask

---

## PM Lifecycle

Discovery
-> Planning
-> Delegating
-> Monitoring
-> Reviewing
-> Complete

---

## PM Responsibilities

### Discovery

Gather requirements.

Output:

- Requirements

### Planning

Generate:

- Plan
- Epics
- Tasks
- Dependencies

### Delegation

Assign work to:

- Backend
- Frontend
- QA
- Research

### Monitoring

Track:

- Progress
- Blockers
- Dependencies

### Review

Evaluate:

- Completed work
- Reviews
- Decisions

---

## Planning Workflow

### Step 1

Create Initiative

Example:

beethoven initiative create "Build Gmail Agent"

### Step 2

Add Requirements

Example:

beethoven requirement create --initiative gmail-agent "Support PDF attachments"

### Step 3

Generate Plan

Example:

beethoven initiative plan gmail-agent

Produces:

- Plan
- Epics
- Tasks

### Step 4

Assign Tasks

PM delegates work.

### Step 5

Monitor Progress

PM tracks execution.

### Step 6

Manage Decisions

PM owns all decision escalation.

Workers never contact humans directly.

---

## Decision Workflow

Worker
-> PM
-> Decision
-> Inbox
-> Human
-> Decision Resolved
-> PM
-> Worker

---

## Required Events

- InitiativeCreated
- InitiativePlanned
- InitiativeCompleted
- RequirementCreated
- RequirementUpdated
- PlanCreated
- EpicCreated
- DecisionCreated
- DecisionEscalated
- DecisionResolved
- PMPlanningStarted
- PMPlanningCompleted
- PMReviewStarted
- PMReviewCompleted

---

## Required Tables

### initiatives

- id
- project_id
- title
- description
- status
- created_at

### requirements

- id
- initiative_id
- description
- created_at

### plans

- id
- initiative_id
- summary
- created_at

### epics

- id
- initiative_id
- title
- created_at

### decisions

- id
- initiative_id
- title
- context
- status
- created_at

---

## CLI Commands

### Initiatives

beethoven initiative create
beethoven initiative list
beethoven initiative show
beethoven initiative plan

### Requirements

beethoven requirement create
beethoven requirement list

### Decisions

beethoven decision list
beethoven decision resolve

---

## Acceptance Criteria

The PM can:

1. Create initiatives
2. Collect requirements
3. Generate plans
4. Generate epics
5. Generate tasks
6. Track dependencies
7. Escalate decisions
8. Resolve decisions
9. Monitor progress
10. Drive initiatives to completion

At the end of Phase 1.6, Beethoven no longer merely manages tasks.

It manages projects.
