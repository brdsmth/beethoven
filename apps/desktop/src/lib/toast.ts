import { create } from "zustand";

export type ToastTone = "success" | "error";

export interface Toast {
  id: string;
  message: string;
  tone: ToastTone;
}

interface ToastState {
  toasts: Toast[];
  dismiss: (id: string) => void;
}

const TOAST_DURATION_MS = 4000;

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  dismiss: (id) => set((state) => ({ toasts: state.toasts.filter((t) => t.id !== id) })),
}));

export function pushToast(message: string, tone: ToastTone = "success") {
  const id = crypto.randomUUID();
  useToastStore.setState((state) => ({ toasts: [...state.toasts, { id, message, tone }] }));
  setTimeout(() => useToastStore.getState().dismiss(id), TOAST_DURATION_MS);
}
