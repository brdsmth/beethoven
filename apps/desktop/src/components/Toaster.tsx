import { useToastStore } from "../lib/toast";

export function Toaster() {
  const toasts = useToastStore((s) => s.toasts);
  const dismiss = useToastStore((s) => s.dismiss);

  if (toasts.length === 0) return null;

  return (
    <div className="pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2">
      {toasts.map((toast) => (
        <div
          key={toast.id}
          className={`pointer-events-auto flex items-start gap-3 rounded-md border px-4 py-2.5 text-sm shadow-lg ${
            toast.tone === "success"
              ? "border-emerald-700 bg-emerald-950/90 text-emerald-200"
              : "border-rose-700 bg-rose-950/90 text-rose-200"
          }`}
        >
          <span className="max-w-sm">{toast.message}</span>
          <button
            onClick={() => dismiss(toast.id)}
            className="text-xs text-slate-400 hover:text-slate-200"
            aria-label="Dismiss"
          >
            ✕
          </button>
        </div>
      ))}
    </div>
  );
}
