use chrono::{DateTime, Utc};

/// A `Ready` task waiting to be released to a worker. Deliberately narrower
/// than `beethoven_core::Task` (just the fields a scheduling policy needs),
/// matching `DelegationTask`'s reason for existing: this crate never has to
/// depend back on `beethoven-core`.
#[derive(Debug, Clone, PartialEq)]
pub struct QueuedTask {
    pub task_id: String,
    pub priority: i64,
    pub queued_at: DateTime<Utc>,
}

/// The set of `Ready` tasks eligible for release, for one scheduling pass.
/// Holds no state of its own beyond what its caller hands it -- `Runtime`
/// rebuilds it from `RuntimeState.tasks` on every tick, so it is durable for
/// free: every `Task.status`/`created_at` it reads is already replayed from
/// the event log on startup, which is what "the queue survives restart"
/// means for a view with nothing to persist independently.
#[derive(Debug, Default, Clone)]
pub struct TaskQueue {
    entries: Vec<QueuedTask>,
}

impl TaskQueue {
    pub fn new(entries: Vec<QueuedTask>) -> Self {
        Self { entries }
    }

    pub fn len(&self) -> usize {
        self.entries.len()
    }

    pub fn is_empty(&self) -> bool {
        self.entries.is_empty()
    }

    pub fn entries(&self) -> &[QueuedTask] {
        &self.entries
    }
}

/// Decides which queued tasks to release next. The only call site is
/// `Runtime::run_lead_delegation`, which rebuilds the `TaskQueue` from
/// current state and asks the scheduler what to release -- the scheduler
/// itself never touches `RuntimeState` or persistence, so a smarter policy
/// (e.g. one that respects task dependencies) can replace `FifoScheduler`
/// later with zero orchestration change.
pub trait Scheduler {
    fn next_tasks(&self, queue: &TaskQueue, max_tasks: usize) -> Vec<String>;
}

/// The scheduling policy: release whichever ready tasks have been queued
/// longest, oldest first, up to `max_tasks`. `priority` is carried on
/// `QueuedTask` for a future policy to use but is ignored here -- no
/// prioritization beyond arrival order yet, by design ("keep it simple").
#[derive(Debug, Default, Clone, Copy)]
pub struct FifoScheduler;

impl Scheduler for FifoScheduler {
    fn next_tasks(&self, queue: &TaskQueue, max_tasks: usize) -> Vec<String> {
        let mut ordered: Vec<&QueuedTask> = queue.entries().iter().collect();
        ordered.sort_by_key(|t| t.queued_at);
        ordered.into_iter().take(max_tasks).map(|t| t.task_id.clone()).collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::Duration;

    fn task_at(id: &str, offset_seconds: i64) -> QueuedTask {
        QueuedTask {
            task_id: id.to_string(),
            priority: 0,
            queued_at: DateTime::UNIX_EPOCH + Duration::seconds(offset_seconds),
        }
    }

    #[test]
    fn releases_the_oldest_queued_tasks_first() {
        let queue = TaskQueue::new(vec![task_at("c", 3), task_at("a", 1), task_at("b", 2)]);
        assert_eq!(FifoScheduler.next_tasks(&queue, 2), vec!["a".to_string(), "b".to_string()]);
    }

    #[test]
    fn never_releases_more_than_max_tasks() {
        let queue = TaskQueue::new(vec![task_at("a", 1), task_at("b", 2), task_at("c", 3)]);
        assert_eq!(FifoScheduler.next_tasks(&queue, 1).len(), 1);
        assert_eq!(FifoScheduler.next_tasks(&queue, 10).len(), 3);
    }

    #[test]
    fn an_empty_queue_releases_nothing() {
        let queue = TaskQueue::new(Vec::new());
        assert_eq!(FifoScheduler.next_tasks(&queue, 5), Vec::<String>::new());
    }

    #[test]
    fn ignores_priority_and_orders_purely_by_queued_at() {
        let mut high_priority_but_newer = task_at("newer", 2);
        high_priority_but_newer.priority = 100;
        let older_low_priority = task_at("older", 1);

        let queue = TaskQueue::new(vec![high_priority_but_newer, older_low_priority]);
        assert_eq!(FifoScheduler.next_tasks(&queue, 1), vec!["older".to_string()]);
    }
}
