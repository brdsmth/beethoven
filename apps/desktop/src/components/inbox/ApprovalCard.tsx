import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import type { InboxItem } from "../../lib/types";
import { ActionButton, InboxCardShell } from "./InboxCardShell";

export function ApprovalCard({ item }: { item: InboxItem }) {
  const respond = useAction((response: string) => api.answerInboxItem(item.id, response), {
    successMessage: "Response sent",
    invalidateKeys: [["inbox", item.project_id], ["project", item.project_id]],
  });

  return (
    <InboxCardShell
      kind="approval"
      title={item.prompt}
      createdAt={item.created_at}
      actions={
        <>
          <ActionButton label="Approve" onClick={() => respond.mutate("approved")} pending={respond.isPending} />
          <ActionButton
            label="Reject"
            tone="danger"
            onClick={() => respond.mutate("rejected")}
            pending={respond.isPending}
          />
        </>
      }
    >
      {item.task_title && <p>Task: {item.task_title}</p>}
      {item.agent_name && <p className="text-slate-500">Requested by {item.agent_name}</p>}
    </InboxCardShell>
  );
}
