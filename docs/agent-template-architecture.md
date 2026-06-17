# Beethoven Architecture Amendment: Agent Templates and Project Organizations

## Decision

Adopt Option 3:

Agent Templates + Project Organizations

This replaces both:

- Shared Global Agents
- Hard-Coded Per-Project Agents

with a template-based architecture.

---

# Motivation

The original architecture left an important ambiguity:

Are agents:

1. Shared globally across every project?
2. Duplicated independently inside every project?

Neither option is ideal.

Shared agents risk memory contamination.

Fully duplicated agents make maintenance difficult.

Instead, Beethoven should separate:

- Agent Templates
- Agent Instances

---

# Conceptual Model

```text
Beethoven

├── Agent Templates
│
│   ├── PM Template
│   ├── Backend Template
│   ├── Frontend Template
│   ├── QA Template
│   ├── Research Template
│   └── DevOps Template
│
└── Projects
    │
    ├── Sample
    │
    ├── Sailboat
    │
    └── Beethoven
```

Templates define behavior.

Projects contain agent instances.

---

# Template vs Instance

## Template

Think:

```text
Rust Struct
```

Templates are reusable definitions.

Examples:

```text
Backend Engineer
Frontend Engineer
QA Engineer
Project Manager
```

A template defines:

- role
- prompt
- permissions
- tools
- execution policies

Templates are global.

---

## Instance

Think:

```text
Rust Struct Instance
```

Instances exist inside a project.

Examples:

```text
Sample Backend

Sample Frontend

Sailboat Backend

Sailboat Frontend
```

Instances own:

- memory
- task queues
- history
- metrics
- workspaces

Instances are project-scoped.

---

# Domain Model

## Agent Template

```rust
pub struct AgentTemplate {
    pub id: AgentTemplateId,

    pub role: String,

    pub prompt: String,

    pub permissions: Vec<Permission>,

    pub tools: Vec<Tool>,
}
```

---

## Agent Instance

```rust
pub struct AgentInstance {
    pub id: AgentInstanceId,

    pub project_id: ProjectId,

    pub template_id: AgentTemplateId,

    pub name: String,

    pub status: AgentStatus,
}
```

---

# Project Creation

When a project is created:

```bash
beethoven project create sample
```

Beethoven automatically creates:

```text
Sample

├── PM
├── Backend
├── Frontend
├── QA
└── Research
```

These are Agent Instances.

---

# Project Organization

Example:

```text
Sample

PM
├── Backend
├── Frontend
├── QA
└── Research
```

Every project receives its own organization.

Projects never share agent instances.

---

# Memory Isolation

This is the primary benefit.

Example:

```text
Sample Backend
```

remembers:

```text
RFQs
Procurement
Neon
Suppliers
```

while:

```text
Sailboat Backend
```

remembers:

```text
Escrow
Listings
Financing
Boats
```

No memory contamination occurs.

---

# Workspace Isolation

Every Agent Instance owns its own workspace.

Example:

```text
Sample

Backend
  Workspace A

Frontend
  Workspace B
```

Workspaces never cross project boundaries.

---

# Event Model

Agent lifecycle events become project-scoped.

Example:

```text
AgentAssigned
AgentExecuting
AgentBlocked
AgentCompleted
```

Scope:

```rust
EventScope::Project(project_id)
```

Template lifecycle events remain global.

Example:

```text
AgentTemplateCreated
AgentTemplateUpdated
```

Scope:

```rust
EventScope::Global
```

---

# Template Upgrades

Templates can evolve centrally.

Example:

```text
Backend Template
```

receives:

```text
new prompt
new tools
new permissions
```

All future agent instances inherit the update.

Existing instances may:

- inherit automatically
- inherit on restart
- require migration

Implementation choice is deferred.

---

# Runtime Model

Execution occurs against Agent Instances.

Example:

```bash
beethoven run sample
```

Runtime loads:

```text
Sample PM

Sample Backend

Sample Frontend

Sample QA

Sample Research
```

The runtime never executes templates.

Templates are metadata only.

---

# Navigation Model

Future UI:

```text
Beethoven

Projects
├── Sample
├── Sailboat
└── Beethoven

Templates
├── PM
├── Backend
├── Frontend
├── QA
└── Research
```

Users primarily interact with Projects.

Templates are administrative.

---

# Database Additions

## agent_templates

```sql
CREATE TABLE agent_templates (
    id TEXT PRIMARY KEY,
    role TEXT NOT NULL,
    prompt TEXT NOT NULL
);
```

---

## agent_instances

```sql
CREATE TABLE agent_instances (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    template_id TEXT NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL
);
```

---

# Recommended Default Templates

```text
Project Manager

Backend Engineer

Frontend Engineer

QA Engineer

Research Agent

DevOps Agent
```

---

# Architectural Rule

Templates are global.

Agent instances are project-scoped.

Projects own execution.

Templates define behavior.

---

# Benefits

1. Project isolation
2. Memory isolation
3. Workspace isolation
4. Easier upgrades
5. Cleaner multi-project support
6. Better long-term scalability
7. Consistent organizational model

---

# Recommendation

Adopt Agent Templates + Project Organizations before implementing real LLM execution.

This establishes the correct ownership model and prevents future migration work around agent identity, memory, workspace assignment, and project isolation.
