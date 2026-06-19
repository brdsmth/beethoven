import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { api } from "./api";
import { useViewStore } from "./store";
import { pushToast } from "./toast";

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

/**
 * Shared wrapper around `useMutation` for runtime/inbox/project/task/agent
 * actions: shows a toast on success/failure and invalidates the queries the
 * action affects, so every action component just calls `mutate(...)` and
 * lets the read paths (which already replay fresh state from the Rust
 * runtime) catch up.
 */
export function useAction<TVariables, TResult>(
  mutationFn: (variables: TVariables) => Promise<TResult>,
  options?: {
    successMessage?: string | ((variables: TVariables, result: TResult) => string);
    invalidateKeys?: QueryKey[];
  },
) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn,
    onSuccess: (result, variables) => {
      const message =
        typeof options?.successMessage === "function"
          ? options.successMessage(variables, result)
          : options?.successMessage;
      if (message) pushToast(message, "success");
      for (const key of options?.invalidateKeys ?? []) {
        queryClient.invalidateQueries({ queryKey: key });
      }
    },
    onError: (error: unknown) => {
      pushToast(error instanceof Error ? error.message : String(error), "error");
    },
  });
}
