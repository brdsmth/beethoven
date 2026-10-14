import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import type { InboxItem } from "../../lib/types";
import { InboxCardShell, RespondField } from "./InboxCardShell";

export function QuestionCard({ item }: { item: InboxItem }) {
  const respond = useAction((response: string) => api.answerInboxItem(item.id, response), {
    successMessage: "Response sent",
    invalidateKeys: [["inbox"], ["project", item.project_id]],
  });

  return (
    <InboxCardShell
      kind="question"
      title={item.prompt}
      createdAt={item.created_at}
      projectSlug={item.project_slug}
      actions={<RespondField label="Respond" onSubmit={(value) => respond.mutate(value)} pending={respond.isPending} />}
    >
      {item.task_title && <p>Task: {item.task_title}</p>}
      {item.agent_name && <p className="text-slate-500">Asked by {item.agent_name}</p>}
    </InboxCardShell>
  );
}
