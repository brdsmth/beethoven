# TODOs

Deferred items from the "Condensed Cards + Per-Task Execution Graph" design
review (`/plan-design-review`). Each was explicitly scoped out, not
forgotten -- revisit only if the noted trigger condition shows up.

## 1. True token-by-token streaming for live run responses

`TaskGraphView`'s Run nodes and drawer poll via the same React Query +
Tauri `state-changed` invalidation every other view uses. Real streaming
(token-by-token, as a run's response is generated) needs new runtime→UI
event infrastructure that doesn't exist anywhere in the app today.
Revisit if polling-driven staleness becomes a real complaint once the
graph is in daily use.

## 2. A global all-agents/all-projects graph canvas

The execution graph (`TaskGraphView`) is scoped to one task at a time. A
single canvas showing every agent across every project was considered and
rejected: unreadable past a handful of nodes, and would need new backend
aggregation plus virtualized rendering for no proportionate benefit on a
single-user tool. Revisit only if the per-task graph proves the
node-chain pattern out and there's a concrete need to see cross-task
relationships at a glance.

## 3. Keyboard navigation for graph nodes

`TaskGraphView`'s nodes are click-only, consistent with every other
custom widget in the app (`TaskTree`, `DependencyGraph`), none of which
have bespoke keyboard nav either. Revisit only if click-only proves to be
real friction in practice.
