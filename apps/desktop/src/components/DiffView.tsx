/// Colors a unified diff's +/- lines so reviewing what an agent actually
/// changed is scannable -- a plain monochrome `<pre>` dump made every line
/// look the same regardless of whether it was added, removed, or just
/// context. `className` carries each call site's own sizing/scroll
/// behavior (max-height, overflow); this only owns line coloring.
export function DiffView({ content, className = "" }: { content: string; className?: string }) {
  const lines = content.split("\n");

  return (
    <pre className={`overflow-x-auto rounded bg-slate-950 p-2 text-xs ${className}`}>
      {lines.map((line, idx) => (
        <div key={idx} className={diffLineColor(line)}>
          {line.length === 0 ? " " : line}
        </div>
      ))}
    </pre>
  );
}

function diffLineColor(line: string): string {
  if (line.startsWith("+++") || line.startsWith("---")) return "text-slate-500";
  if (line.startsWith("+")) return "text-emerald-400";
  if (line.startsWith("-")) return "text-rose-400";
  if (line.startsWith("@@")) return "text-sky-400";
  if (line.startsWith("diff --git") || line.startsWith("index ")) return "font-semibold text-slate-500";
  return "text-slate-400";
}
