import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import type { InboxItem } from "../../lib/types";
import { ActionButton, InboxCardShell } from "./InboxCardShell";

export function AlertCard({ item }: { item: InboxItem }) {
  const acknowledge = useAction<void, unknown>(() => api.answerInboxItem(item.id, "acknowledged"), {
    successMessage: "Acknowledged",
    invalidateKeys: [["inbox"], ["project", item.project_id]],
  });

  return (
    <InboxCardShell
      kind="alert"
      title={item.prompt}
      createdAt={item.created_at}
      projectSlug={item.project_slug}
      actions={
        <ActionButton label="Acknowledge" onClick={() => acknowledge.mutate()} pending={acknowledge.isPending} />
      }
    >
      {item.task_title && <p>Task: {item.task_title}</p>}
      {item.agent_name && <p className="text-slate-500">Raised by {item.agent_name}</p>}
    </InboxCardShell>
  );
}
