import type { InboxItem } from "../../lib/types";
import { InboxCardShell, DisabledActionButton } from "./InboxCardShell";

export function AlertCard({ item }: { item: InboxItem }) {
  return (
    <InboxCardShell
      kind="alert"
      title={item.prompt}
      createdAt={item.created_at}
      actions={<DisabledActionButton label="Acknowledge" />}
    >
      {item.task_title && <p>Task: {item.task_title}</p>}
      {item.agent_name && <p className="text-slate-500">Raised by {item.agent_name}</p>}
    </InboxCardShell>
  );
}
