import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { useViewStore } from "./store";

export function useProjects() {
  return useQuery({ queryKey: ["projects"], queryFn: api.getProjects });
}

export function useSelectedProject() {
  const { data: projects } = useProjects();
  const selectedProjectId = useViewStore((s) => s.selectedProjectId);
  const setSelectedProjectId = useViewStore((s) => s.setSelectedProjectId);

  useEffect(() => {
    if (!selectedProjectId && projects && projects.length > 0) {
      setSelectedProjectId(projects[0].id);
    }
  }, [projects, selectedProjectId, setSelectedProjectId]);

  return {
    projects: projects ?? [],
    selectedProjectId,
    setSelectedProjectId,
  };
}

export function useElapsedLabel(since: string): string {
  const start = Date.parse(since);
  const tick = useTick(1000);
  const seconds = Math.max(0, Math.floor((tick - start) / 1000));
  const minutes = Math.floor(seconds / 60);
  const remSeconds = seconds % 60;
  if (minutes <= 0) return `${remSeconds}s`;
  return `${minutes}m ${remSeconds}s`;
}

function useTick(intervalMs: number): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}
