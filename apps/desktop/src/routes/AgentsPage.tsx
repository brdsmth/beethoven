import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useSelectedProject } from "../lib/hooks";
import { ProjectPicker } from "../components/ProjectPicker";
import { Panel } from "../components/Panel";
import { AgentTree } from "../components/agents/AgentTree";
import { AgentDetail } from "../components/agents/AgentDetail";

export function AgentsPage() {
  const { projects, selectedProjectId, setSelectedProjectId } = useSelectedProject();
  const [selectedAgentId, setSelectedAgentId] = useState<string | null>(null);

  const { data: agents } = useQuery({
    queryKey: ["agents", selectedProjectId],
    queryFn: () => api.getAgents(selectedProjectId as string),
    enabled: !!selectedProjectId,
  });

  if (!selectedProjectId) {
    return <div className="text-sm text-slate-500">No projects yet.</div>;
  }

  const selectedAgent = (agents ?? []).find((a) => a.id === selectedAgentId) ?? null;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold text-slate-100">Agents</h1>
        <ProjectPicker projects={projects} selectedProjectId={selectedProjectId} onChange={setSelectedProjectId} />
      </div>

      <div className="grid grid-cols-2 gap-4">
        <Panel title="Organization">
          <AgentTree agents={agents ?? []} selectedAgentId={selectedAgentId} onSelect={setSelectedAgentId} />
        </Panel>

        {selectedAgent ? (
          <AgentDetail agent={selectedAgent} />
        ) : (
          <Panel title="Agent Detail">
            <div className="text-sm text-slate-500">Select an agent to see details.</div>
          </Panel>
        )}
      </div>
    </div>
  );
}
