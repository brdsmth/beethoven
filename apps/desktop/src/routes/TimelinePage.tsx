import { EventStream } from "../components/timeline/EventStream";

export function TimelinePage() {
  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold text-slate-100">Timeline</h1>
      <EventStream />
    </div>
  );
}
