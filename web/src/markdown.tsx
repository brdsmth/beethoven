import { h, type ComponentChild } from 'preact';

// A small Markdown subset rendered straight to VNodes: agent text is
// untrusted, so it never goes through an HTML parser.

export const safeHref = (url?: string) => (url && /^https?:\/\//i.test(url) ? url : undefined);

export const Link = ({ href, children }: { href?: string; children: ComponentChild }) =>
  safeHref(href) ? <a href={href} target="_blank" rel="noopener noreferrer">{children}</a> : <>{children}</>;

const INLINE = /`([^`\n]+)`|\*\*(.+?)\*\*|\[([^\]\n]+)\]\(([^)\s]+)\)|(https?:\/\/[^\s<>()[\]]*[^\s<>()[\].,;:!?'"])/g;

function inline(s: string): ComponentChild[] {
  const out: ComponentChild[] = [];
  let last = 0;
  for (const m of s.matchAll(INLINE)) {
    if (m.index > last) out.push(s.slice(last, m.index));
    if (m[1] != null) out.push(<code>{m[1]}</code>);
    else if (m[2] != null) out.push(<strong>{inline(m[2])}</strong>);
    else if (m[3] != null) out.push(safeHref(m[4]) ? <Link href={m[4]}>{inline(m[3])}</Link> : m[0]);
    else out.push(<Link href={m[5]}>{m[5]}</Link>);
    last = m.index + m[0].length;
  }
  if (last < s.length) out.push(s.slice(last));
  return out;
}

const FENCE = /^\s*(`{3,}|~{3,})/;
const HEADING = /^(#{1,6})\s+(.*?)\s*#*$/;
const ITEM = /^\s*([-*+]|\d{1,9}[.)])\s+(.*)$/;
const RULE = /^\s*([-*_])(\s*\1){2,}\s*$/;
const isBlockStart = (l: string) => FENCE.test(l) || HEADING.test(l) || ITEM.test(l) || RULE.test(l);

export function Markdown({ text }: { text: string }) {
  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  const out: ComponentChild[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    let m: RegExpMatchArray | null;
    if (!line.trim()) { i++; continue; }
    if ((m = line.match(FENCE))) {
      const body: string[] = [];
      for (i++; i < lines.length && !lines[i].trimStart().startsWith(m[1]); i++) body.push(lines[i]);
      i++;
      out.push(<pre class="code"><code>{body.join('\n')}</code></pre>);
    } else if ((m = line.match(HEADING))) {
      out.push(h(`h${Math.min(m[1].length + 2, 6)}`, null, inline(m[2])));
      i++;
    } else if (RULE.test(line)) {
      out.push(<hr />);
      i++;
    } else if ((m = line.match(ITEM))) {
      const ordered = /\d/.test(m[1]);
      const items: string[] = [];
      while (i < lines.length) {
        const it = lines[i].match(ITEM);
        if (it && /\d/.test(it[1]) === ordered) items.push(it[2]);
        else if (lines[i].trim() && /^\s+/.test(lines[i]) && items.length) items[items.length - 1] += '\n' + lines[i].trim();
        else break;
        i++;
      }
      const lis = items.map(t => <li>{inline(t)}</li>);
      out.push(ordered ? <ol start={parseInt(m[1]) || 1}>{lis}</ol> : <ul>{lis}</ul>);
    } else {
      const para: string[] = [];
      while (i < lines.length && lines[i].trim() && (!para.length || !isBlockStart(lines[i]))) para.push(lines[i++]);
      out.push(<p>{inline(para.join('\n'))}</p>);
    }
  }
  return <div class="md">{out}</div>;
}
