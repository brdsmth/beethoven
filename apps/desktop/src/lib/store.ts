import { create } from "zustand";

interface TimelineFilterState {
  projectId: string;
  agentId: string;
  eventType: string;
  since: string;
  until: string;
}

interface ViewState {
  selectedProjectId: string | null;
  setSelectedProjectId: (id: string | null) => void;
  timelineFilters: TimelineFilterState;
  setTimelineFilters: (filters: Partial<TimelineFilterState>) => void;
}

export const useViewStore = create<ViewState>((set) => ({
  selectedProjectId: null,
  setSelectedProjectId: (id) => set({ selectedProjectId: id }),
  timelineFilters: { projectId: "", agentId: "", eventType: "", since: "", until: "" },
  setTimelineFilters: (filters) =>
    set((state) => ({ timelineFilters: { ...state.timelineFilters, ...filters } })),
}));
