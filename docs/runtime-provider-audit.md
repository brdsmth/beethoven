# Runtime Provider Audit & Decoupling Plan

Audit performed against the plan at `beethoven-runtime-provider-audit-plan.md`. Scope: is Beethoven genuinely model-provider agnostic, and if not, what changes are needed so Claude, Codex, OpenAI, Ollama, vLLM, OpenRouter, etc. can be added without touching orchestration code.

**Crate name note:** the plan's audit targets (`beethoven-runtime`, `beethoven-orchestrator`) don't exist in this repo. The equivalent real crates are `beethoven-core` (orchestration / `Runtime` / tick loop / PM), `beethoven-agents` (the `ModelRuntime` trait, prompt building, review, schema parsing, and the one Claude adapter), and `beethoven-cli` / `apps/desktop/src-tauri` (the two process composition roots).

## Verdict

Beethoven's actual model-invocation path is already well decoupled. There is exactly **one** call site in the whole codebase that invokes a model (`Runtime::tick_projects`, via the `ModelRuntime` trait), and exactly **two** places that name a concrete provider type (`ClaudeCodeRuntime`), both at binary composition roots, not inside orchestration. Swapping the global provider today is a 2-line change with zero orchestration edits.

What's missing is the *selection* layer the plan asks for. There is no `RuntimeRegistry`, no `RuntimeProfile`, and no per-agent/per-task provider choice. The whole process is wired to exactly one `Arc<dyn ModelRuntime>` shared by every agent, task, and project. So today you can **switch** providers cheaply, but you cannot run two providers **side by side** (e.g. Claude for backend work, a cheap local Ollama model for QA) without code changes.

---

## Step 1: Provider Leakage

| Question | Answer |
|---|---|
| Does orchestration (`beethoven-core::Runtime`) call Claude directly? | No. It calls `self.model_runtime.execute(&request)` through the `ModelRuntime` trait object. Zero `Claude*` symbols appear anywhere in `beethoven-core`. |
| Does PM call Claude directly? | No — and PM planning doesn't call **any** model yet. `InitiativePlanningService::plan` / `PlanningService::plan` are deterministic stub heuristics (their own doc comments say "Stub planner" / "Stub PM planner"). `PromptBuilder::pm_planning` + `PM_PLANNING_JSON_SCHEMA` exist and are unit-tested in isolation but nothing in `runtime.rs` calls them yet. |
| Does review call Claude directly? | No, same story. `ReviewService::review` is a deterministic keyword/diff heuristic ("Stub reviewer... until a model runtime drives this too" — its own doc comment), not a model call. `PromptBuilder::review` + `REVIEW_JSON_SCHEMA` are dead code from `runtime.rs`'s perspective today. |
| Do task executors call Claude directly? | Only indirectly, at one call site: `crates/beethoven-core/src/runtime.rs:1034`, `model_runtime.execute(&request)` where `model_runtime: Arc<dyn ModelRuntime>`. |

Full `rg -i "claude\|anthropic"` hits across the Rust workspace, categorized:

- **Real coupling (concrete type named outside the adapter module):**
  - `crates/beethoven-cli/src/main.rs:205` — `Box::new(beethoven_agents::ClaudeCodeRuntime::default())`
  - `apps/desktop/src-tauri/src/lib.rs:21` — same hardcode, Tauri `AppState` construction
- **Correctly scoped (inside the adapter itself, `beethoven-agents/src/model.rs`):** `ClaudeCodeRuntime`, the private `ClaudeEnvelope` struct, `build_claude_command`.
- **Comments / test fixtures / docs only (no coupling):** `beethoven-workspaces/src/tmux.rs:45`, `beethoven-core/tests/runtime.rs:50,101`, `beethoven-tauri/src/derive.rs:718` (a `"provider": "claude"` JSON fixture).
- **Frontend (`apps/desktop/src/`):** zero matches.

---

## Step 2: Runtime Boundaries

| Role | Who does it | Matches target? |
|---|---|---|
| Creates prompts | `beethoven_agents::PromptBuilder` (`prompt.rs`) — pure string templating, zero provider awareness | ✅ |
| Selects models | **Nobody.** One model runtime per process, chosen once at startup. No registry, no per-agent/task selection | ❌ gap |
| Invokes models | `Runtime::tick_projects` → `self.model_runtime.execute(request)` through `ModelRuntime`, provider-blind | ✅ |
| Interprets responses | `ExecutionResult { status, summary, raw_response, structured_output, cost_usd }`, normalized inside `ClaudeCodeRuntime::execute` from Claude Code's own JSON envelope. `parse_structured_output` is the one place `structured_output: Value` becomes typed (`PmPlanResponse`/`ReviewResponse`) | ✅ shape matches, ⚠️ currently unreachable in production code (see Step 1 — review/planning don't call a model yet) |

---

## Step 3: Claude-Specific Types

- `ClaudeCodeRuntime` (`beethoven_agents::model`) — the only `ModelRuntime` implementation today. Correctly scoped.
- `ClaudeEnvelope` — `struct` (not `pub`), private to `model.rs`, never leaks past the module boundary. Exactly matches "only provider adapters know provider-specific types."
- `ClaudeRequest` / `ClaudeResponse` / `AnthropicClient` — none exist. `ExecutionRequest`/`ExecutionResult` are already the provider-neutral types used everywhere else.
- The literal strings `"claude"` / `"claude-code"` only appear as: `ClaudeCodeRuntime::provider()`'s display string (recorded into `ModelRun.provider` for audit/UI, never branched on), the literal `claude` CLI binary name inside `build_claude_command`, and test fixtures.

---

## Coupling Points (exhaustive)

1. `crates/beethoven-cli/src/main.rs:205` — hardcoded `ClaudeCodeRuntime::default()` passed into `Runtime::load`.
2. `apps/desktop/src-tauri/src/lib.rs:21` — same hardcode for the desktop app's `AppState`.
3. **Structural gap (not a leak, but the real blocker):** `Runtime` holds a single field `model_runtime: Arc<dyn ModelRuntime + Send + Sync>` (`crates/beethoven-core/src/runtime.rs:116`), set once in `Runtime::load`. There is no way for two agents in the same project — or the same agent on two different tasks — to use different providers or models. Today's design requires zero orchestration changes to *switch* the one global provider, but cannot run several providers *concurrently*, which is what "add Codex/Ollama without modifying orchestration" implies once you actually want more than one live.
4. **Latent (not yet wired):** `ReviewService::review` and `PlanningService`/`InitiativePlanningService::plan` bypass `ModelRuntime` entirely (deterministic stubs). When these are eventually upgraded to call a real model, that's two more call sites that need to go through the registry/profile system from day one rather than getting their own hardcoded path.

No other coupling points found. `beethoven-api` is an unimplemented placeholder crate (`//! Phase 2 ... Not implemented yet`), irrelevant here.

---

## Runtime Dependency Diagram

**Current:**

```
beethoven-cli (main.rs)  ──┐
                            ├──> ClaudeCodeRuntime::default()  ──┐
apps/desktop/src-tauri ────┘                                    │
                                                                  v
                                                     Runtime { model_runtime: Arc<dyn ModelRuntime> }
                                                                  │
                                                                  v
                                            tick_projects ──> start_task ──> model_runtime.execute(request)
                                                                  │
                                                                  v
                                                          ExecutionResult (provider-neutral)
                                                                  │
                                                                  v
                                                    finish_task ──> ReviewService (stub, no model call)
```

**Target:**

```
beethoven-cli / apps/desktop/src-tauri (composition root)
    │  registers N adapters by id
    v
RuntimeRegistry { "anthropic" -> ClaudeCodeRuntime, "ollama" -> OllamaRuntime, ... }
    ^
    │  resolve(profile.runtime_id)
    │
AgentTemplate.runtime_profile_id ──> RuntimeProfile { runtime_id, model } ──> registry.get(runtime_id)
    │
    v
Runtime::tick_projects ──> start_task ──> registry.get(...).execute(request) ──> ExecutionResult
```

---

## Refactor Plan

1. Add `RuntimeProfile` (new struct, see below) and a small `RuntimeRegistry` (new struct) to `beethoven-agents` — it already owns `ModelRuntime`, so adapter selection belongs next to it, not in `beethoven-core`.
2. Change `Runtime` (`beethoven-core::runtime`) to hold `registry: RuntimeRegistry` instead of the single `model_runtime: Arc<dyn ModelRuntime + Send + Sync>` field. `Runtime::load` takes a `RuntimeRegistry` instead of one boxed runtime.
3. Add `runtime_profile_id: String` to `AgentTemplate`, defaulted in seed data to today's single profile (e.g. `"claude-default"`) so existing behavior is unchanged on upgrade.
4. At the one call site (`runtime.rs:1034`), resolve `agent.template.runtime_profile_id` → `RuntimeProfile` → `registry.get(&profile.runtime_id)` instead of `self.model_runtime`.
5. Move `ClaudeCodeRuntime::default()` construction out of being *the* runtime and into *one registration* in each composition root (`beethoven-cli/src/main.rs`, `apps/desktop/src-tauri/src/lib.rs`): `registry.register("anthropic", Arc::new(ClaudeCodeRuntime::default()))`. The composition root stays the only place importing concrete adapter types — now plural instead of singular.
6. Defer: when `ReviewService`/`PlanningService` are upgraded to call a real model (separate, pre-existing gap — not caused by this refactor), route them through the same registry/profile resolution from the start.

This is additive and backwards-compatible: one profile pointing at one registered adapter reproduces exactly today's behavior.

---

## RuntimeProfile Design

```rust
// crates/beethoven-agents/src/profile.rs
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RuntimeProfile {
    pub id: String,         // e.g. "backend-default", "cheap-coder"
    pub runtime_id: String, // registry key, e.g. "anthropic", "ollama"
    pub model: String,      // e.g. "claude-sonnet-4-6", "qwen-coder"
}
```

Kept as a plain `String`-keyed struct (no `RuntimeId`/`ProfileId` newtypes) to match the codebase's existing convention — every other domain id (`Project.id`, `Task.id`, `AgentInstance.id`, ...) is a plain `String`.

`AgentTemplate` gains `pub runtime_profile_id: String`. Agents select profiles; profiles select providers — exactly the separation the plan asks for, and it slots into the existing template→instance flow with one new field, no new entity type needed at the instance level unless per-instance overrides are wanted later.

## RuntimeRegistry Design

```rust
// crates/beethoven-agents/src/registry.rs
pub struct RuntimeRegistry {
    runtimes: HashMap<String, Arc<dyn ModelRuntime + Send + Sync>>,
}

impl RuntimeRegistry {
    pub fn new() -> Self {
        Self { runtimes: HashMap::new() }
    }

    pub fn register(&mut self, runtime_id: impl Into<String>, runtime: Arc<dyn ModelRuntime + Send + Sync>) {
        self.runtimes.insert(runtime_id.into(), runtime);
    }

    pub fn get(&self, runtime_id: &str) -> Result<&Arc<dyn ModelRuntime + Send + Sync>, AgentsError> {
        self.runtimes
            .get(runtime_id)
            .ok_or_else(|| AgentsError::UnknownRuntime(runtime_id.to_string()))
    }
}
```

All execution goes through the registry; `Runtime` never sees a concrete adapter type.

---

## Provider Adapter Checklist

To add a new provider (e.g. Ollama):

1. Implement `ModelRuntime` for `OllamaRuntime` in `beethoven-agents` (new module, sibling to `model.rs`) — `provider()` returns `"ollama"`, `execute()` does whatever Ollama needs (HTTP call, or a CLI wrapper analogous to `ClaudeCodeRuntime`'s tmux approach).
2. Export it from `beethoven-agents::lib.rs`.
3. Register it at each composition root: `registry.register("ollama", Arc::new(OllamaRuntime::new(...)))`.
4. Add a `RuntimeProfile` with `runtime_id: "ollama"` and whatever `model` string it expects.
5. Point an `AgentTemplate.runtime_profile_id` at that profile.

Nothing in `tick_projects`, `start_task`, `finish_task`, `ReviewService`, `PromptBuilder`, or any Tauri command/UI needs to change. Matches the plan's stated success criteria exactly (create adapter → register adapter → create runtime profile, nothing else) once steps 1–5 above land.

**Caveat worth flagging:** `ExecutionRequest` currently requires `workspace: &WorkspaceState` (a real git worktree + tmux session) and an `artifacts_dir`. This fits CLI coding agents (Claude Code, Codex CLI, aider) cleanly. A pure HTTP API provider (raw Anthropic/OpenAI chat completions, Ollama's HTTP API, vLLM, OpenRouter) doesn't need a tmux session at all — its adapter would just ignore `workspace`/use `artifacts_dir` only to drop a response file for parity. Works without changing the trait, but it's a shape mismatch worth knowing about; if/when the first non-CLI adapter is built, consider whether `ExecutionRequest` should stop assuming a workspace is always present.

---

## Acceptance Test Status

**Already satisfied for the Execute path.** `crates/beethoven-core/tests/runtime.rs` already drives the full tick → start → execute → review → complete/escalate lifecycle exclusively through fake `ModelRuntime` implementations (`StubModelRuntime`, `SlowModelRuntime`, `GitClobberingModelRuntime`, `NoOpModelRuntime`) with zero orchestration changes. `cargo test --workspace` never touches a real `claude` process — the one test that does (`model.rs::claude_code_runtime_executes_a_real_prompt_and_parses_the_envelope`) is `#[ignore]`d by design.

**Not yet testable for Plan/Review**, but not because of provider coupling — `PlanningService`/`InitiativePlanningService`/`ReviewService` don't call `ModelRuntime` at all today (pre-existing stub gap, documented in their own doc comments). Once they're wired to a real model call, they'll automatically inherit the same swappability the Execute path already has, provided they're built to call through the registry from the start (Refactor Plan step 6).

---

## Summary

| Deliverable | Status |
|---|---|
| Architecture audit document | This document |
| List of coupling points | 2 hardcodes + 1 structural gap + 1 latent gap (above) |
| Runtime dependency diagram | Above |
| Refactor plan | Above — additive, backwards-compatible |
| RuntimeProfile design | Above |
| RuntimeRegistry design | Above |
| Provider adapter checklist | Above |
| Acceptance test (MockRuntime) | Already passing for Execute via existing test doubles; Plan/Review have no model call yet to test |

Beethoven's execution path is close to the target architecture already — the gap is narrow (one field, two call sites, one new struct, one registry) rather than a deep rewrite.
