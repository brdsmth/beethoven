import type { InboxItem } from "../../lib/types";
import { InboxCardShell, DisabledActionButton } from "./InboxCardShell";

export function QuestionCard({ item }: { item: InboxItem }) {
  return (
    <InboxCardShell
      kind="question"
      title={item.prompt}
      createdAt={item.created_at}
      actions={<DisabledActionButton label="Respond" />}
    >
      {item.task_title && <p>Task: {item.task_title}</p>}
      {item.agent_name && <p className="text-slate-500">Asked by {item.agent_name}</p>}
    </InboxCardShell>
  );
}
