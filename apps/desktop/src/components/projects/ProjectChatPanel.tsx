import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import { allTasks } from "../../lib/projectHealth";
import type { Agent, ProjectChatTurn, ProjectDetail as ProjectDetailData, SuggestedAction, Task } from "../../lib/types";
import { ActionButton } from "../ActionButton";
import { Panel } from "../Panel";

/// The interactive, project-scoped agent: ask a question informed by the
/// whole task graph (or one task and its siblings -- the direct fix for
/// "three Claude sessions in one graph that don't know about each other"),
/// get a grounded answer plus zero or more one-click suggested actions.
/// Advisory only: every suggested action still goes through the exact same
/// `useAction`/`ActionButton` confirm flow every other button in this app
/// already uses -- nothing here executes anything on its own. Backed by a
/// fresh one-shot model call per question, not a persistent session:
/// continuity comes from resending the recent transcript server-side, not
/// from a long-running process.
export function ProjectChatPanel({ project }: { project: ProjectDetailData }) {
  const [question, setQuestion] = useState("");
  const [focusTaskId, setFocusTaskId] = useState("");

  const { data: turns } = useQuery({
    queryKey: ["projectChat", project.id],
    queryFn: () => api.getProjectChatHistory(project.id),
  });
  const { data: agents } = useQuery({ queryKey: ["agents", project.id], queryFn: () => api.getAgents(project.id) });
  const tasks = allTasks(project);

  const ask = useAction(
    (vars: { question: string; focusTaskId: string | null }) =>
      api.askProjectChat(project.id, vars.question, vars.focusTaskId),
    { invalidateKeys: [["projectChat", project.id]] },
  );

  return (
    <Panel title="Project Chat">
      <div className="flex flex-col gap-4">
        {!turns || turns.length === 0 ? (
          <div className="text-sm text-slate-500">
            Ask anything about this project -- what's blocked, what each task in a graph has actually done, what to
            do next. Answers are advisory: nothing happens until you click a suggested action below it.
          </div>
        ) : (
          <div className="flex flex-col gap-4">
            {turns.map((turn) => (
              <ChatTurnView key={turn.id} turn={turn} projectId={project.id} agents={agents ?? []} tasks={tasks} />
            ))}
          </div>
        )}

        <div className="flex flex-col gap-2 border-t border-slate-800 pt-3">
          <select
            value={focusTaskId}
            onChange={(e) => setFocusTaskId(e.target.value)}
            className="self-start rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-300"
          >
            <option value="">Whole project</option>
            {tasks.map((t) => (
              <option key={t.id} value={t.id}>
                {t.title}
              </option>
            ))}
          </select>
          <div className="flex items-end gap-2">
            <textarea
              value={question}
              onChange={(e) => setQuestion(e.target.value)}
              placeholder="Ask the project…"
              rows={2}
              className="flex-1 resize-none rounded-md border border-slate-700 bg-slate-900 px-2 py-1.5 text-sm text-slate-200"
            />
            <ActionButton
              label="Send"
              disabled={!question.trim()}
              pending={ask.isPending}
              onClick={() =>
                ask.mutate({ question, focusTaskId: focusTaskId || null }, { onSuccess: () => setQuestion("") })
              }
            />
          </div>
        </div>
      </div>
    </Panel>
  );
}

function ChatTurnView({
  turn,
  projectId,
  agents,
  tasks,
}: {
  turn: ProjectChatTurn;
  projectId: string;
  agents: Agent[];
  tasks: Task[];
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="self-end rounded-lg bg-slate-800 px-3 py-1.5 text-sm text-slate-100">
        {turn.focus_task_title && (
          <div className="mb-1 text-[10px] uppercase tracking-wide text-slate-400">re: {turn.focus_task_title}</div>
        )}
        {turn.question}
      </div>
      <div className="rounded-lg border border-slate-800 bg-slate-900/60 px-3 py-1.5 text-sm text-slate-300">
        {turn.answer}
      </div>
      {turn.suggested_actions.length > 0 && (
        <div className="flex flex-wrap gap-2 pl-1">
          {turn.suggested_actions.map((action, i) => (
            <SuggestedActionButton key={i} action={action} projectId={projectId} agents={agents} tasks={tasks} />
          ))}
        </div>
      )}
    </div>
  );
}

function taskTitleFor(tasks: Task[], taskId: string): string | undefined {
  return tasks.find((t) => t.id === taskId)?.title;
}

function SuggestedActionButton({
  action,
  projectId,
  agents,
  tasks,
}: {
  action: SuggestedAction;
  projectId: string;
  agents: Agent[];
  tasks: Task[];
}) {
  switch (action.kind) {
    case "retry_task":
      return <RetryTaskAction projectId={projectId} taskId={action.task_id} taskTitle={taskTitleFor(tasks, action.task_id)} />;
    case "cancel_task":
      return <CancelTaskAction projectId={projectId} taskId={action.task_id} taskTitle={taskTitleFor(tasks, action.task_id)} />;
    case "reassign_task":
      return (
        <ReassignTaskAction
          projectId={projectId}
          taskId={action.task_id}
          newAgentId={action.new_agent_id}
          taskTitle={taskTitleFor(tasks, action.task_id)}
          agentName={agents.find((a) => a.id === action.new_agent_id)?.name}
        />
      );
    case "answer_inbox":
      return <AnswerInboxAction projectId={projectId} inboxId={action.inbox_id} response={action.response} />;
    case "resolve_decision":
      return <ResolveDecisionAction projectId={projectId} decisionId={action.decision_id} response={action.response} />;
    case "approve_review":
      return <ApproveReviewAction projectId={projectId} taskId={action.task_id} taskTitle={taskTitleFor(tasks, action.task_id)} />;
    case "request_review_changes":
      return (
        <RequestReviewChangesAction
          projectId={projectId}
          taskId={action.task_id}
          reason={action.reason}
          taskTitle={taskTitleFor(tasks, action.task_id)}
        />
      );
  }
}

function RetryTaskAction({ projectId, taskId, taskTitle }: { projectId: string; taskId: string; taskTitle?: string }) {
  const retry = useAction(api.retryTask, {
    successMessage: "Task retried",
    invalidateKeys: [["project", projectId], ["tasks", projectId]],
  });
  return (
    <ActionButton
      label={`Retry${taskTitle ? `: ${taskTitle}` : ""}`}
      pending={retry.isPending}
      onClick={() => retry.mutate(taskId)}
    />
  );
}

function CancelTaskAction({ projectId, taskId, taskTitle }: { projectId: string; taskId: string; taskTitle?: string }) {
  const cancel = useAction(api.cancelTask, {
    successMessage: "Task cancelled",
    invalidateKeys: [["project", projectId], ["tasks", projectId]],
  });
  return (
    <ActionButton
      label={`Cancel${taskTitle ? `: ${taskTitle}` : ""}`}
      tone="danger"
      pending={cancel.isPending}
      onClick={() => cancel.mutate(taskId)}
    />
  );
}

function ReassignTaskAction({
  projectId,
  taskId,
  newAgentId,
  taskTitle,
  agentName,
}: {
  projectId: string;
  taskId: string;
  newAgentId: string;
  taskTitle?: string;
  agentName?: string;
}) {
  const reassign = useAction(({ taskId, newAgentId }: { taskId: string; newAgentId: string }) => api.reassignTask(taskId, newAgentId), {
    successMessage: "Task reassigned",
    invalidateKeys: [["project", projectId], ["tasks", projectId], ["agents", projectId]],
  });
  return (
    <ActionButton
      label={`Reassign${taskTitle ? `: ${taskTitle}` : ""} to ${agentName ?? "agent"}`}
      pending={reassign.isPending}
      onClick={() => reassign.mutate({ taskId, newAgentId })}
    />
  );
}

function AnswerInboxAction({ projectId, inboxId, response }: { projectId: string; inboxId: string; response: string }) {
  const answer = useAction((response: string) => api.answerInboxItem(inboxId, response), {
    successMessage: "Response sent",
    invalidateKeys: [["inbox"], ["project", projectId]],
  });
  return (
    <ActionButton label={`Send: "${response}"`} pending={answer.isPending} onClick={() => answer.mutate(response)} />
  );
}

function ResolveDecisionAction({
  projectId,
  decisionId,
  response,
}: {
  projectId: string;
  decisionId: string;
  response: string;
}) {
  const resolve = useAction((response: string) => api.resolveDecision(decisionId, response), {
    successMessage: "Decision resolved",
    invalidateKeys: [["project", projectId], ["decisions"]],
  });
  return (
    <ActionButton label={`Resolve: "${response}"`} pending={resolve.isPending} onClick={() => resolve.mutate(response)} />
  );
}

function ApproveReviewAction({ projectId, taskId, taskTitle }: { projectId: string; taskId: string; taskTitle?: string }) {
  const approve = useAction(api.approveReview, {
    successMessage: "Review approved",
    invalidateKeys: [["project", projectId], ["tasks", projectId]],
  });
  return (
    <ActionButton
      label={`Approve${taskTitle ? `: ${taskTitle}` : ""}`}
      pending={approve.isPending}
      onClick={() => approve.mutate(taskId)}
    />
  );
}

function RequestReviewChangesAction({
  projectId,
  taskId,
  reason,
  taskTitle,
}: {
  projectId: string;
  taskId: string;
  reason: string;
  taskTitle?: string;
}) {
  const requestChanges = useAction(
    ({ taskId, reason }: { taskId: string; reason: string }) => api.requestReviewChanges(taskId, reason),
    { successMessage: "Changes requested", invalidateKeys: [["project", projectId], ["tasks", projectId]] },
  );
  return (
    <ActionButton
      label={`Request changes${taskTitle ? `: ${taskTitle}` : ""}`}
      pending={requestChanges.isPending}
      onClick={() => requestChanges.mutate({ taskId, reason })}
    />
  );
}
