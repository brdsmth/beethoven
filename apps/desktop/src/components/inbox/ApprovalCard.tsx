import type { InboxItem } from "../../lib/types";
import { InboxCardShell, DisabledActionButton } from "./InboxCardShell";

export function ApprovalCard({ item }: { item: InboxItem }) {
  return (
    <InboxCardShell
      kind="approval"
      title={item.prompt}
      createdAt={item.created_at}
      actions={
        <>
          <DisabledActionButton label="Approve" />
          <DisabledActionButton label="Reject" />
        </>
      }
    >
      {item.task_title && <p>Task: {item.task_title}</p>}
      {item.agent_name && <p className="text-slate-500">Requested by {item.agent_name}</p>}
    </InboxCardShell>
  );
}
