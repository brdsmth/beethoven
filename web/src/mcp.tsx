import { useState } from 'preact/hooks';
import { api, type Environment, type MCPServer } from './api';
import { useTeams } from './teams';
import { KeyValueEditor, RepoInput, type KV } from './pickers';
import { ConfirmButton, Head, Seg, errText, fail, toast, useLoad } from './util';

/** Splits a command line into arguments, honoring single and double quotes. */
export function splitArgs(line: string): string[] {
  const out: string[] = [];
  let cur = '', quote = '', open = false;
  for (const c of line) {
    if (quote) {
      if (c === quote) quote = '';
      else cur += c;
    } else if (c === '"' || c === "'") {
      quote = c;
      open = true;
    } else if (/\s/.test(c)) {
      if (open || cur) out.push(cur);
      cur = '';
      open = false;
    } else cur += c;
  }
  if (open || cur) out.push(cur);
  return out;
}
const quoteArg = (a: string) => (a && !/[\s'"]/.test(a) ? a : a.includes("'") ? `"${a}"` : `'${a}'`);
export const joinArgs = (args: string[]) => args.map(quoteArg).join(' ');

export function MCPServers() {
  const [list, reload] = useLoad<MCPServer[]>('/api/mcp-servers');
  const [envs] = useLoad<Environment[]>('/api/environments');
  const teams = useTeams();
  const [editing, setEditing] = useState('');
  const scopeLabel = (s: string) => !s ? 'Global'
    : s.startsWith('env:') ? `Project: ${envs?.find(e => 'env:' + e.id === s)?.name ?? 'deleted'}`
    : s.startsWith('team:') ? `Team: ${teams?.find(t => 'team:' + t.id === s)?.name ?? 'deleted'}` : s;
  const toggle = (m: MCPServer) => api('PUT', `/api/mcp-servers/${m.id}`, {
    name: m.name, scope: m.scope, type: m.type, url: m.url, command: m.command, enabled: !m.enabled,
  }).then(reload, fail);
  const done = () => { setEditing(''); reload(); };
  const form = (m?: MCPServer) => <MCPForm key={m?.id || 'new'} server={m} envs={envs || []} done={done} cancel={() => setEditing('')} />;
  return (
    <>
      <Head title="MCP servers" actions={!editing && <button class="btn primary" onClick={() => setEditing('new')}>Add server</button>}>
        Extra tool servers offered to agents alongside Beethoven's own. A server applies to every session, to sessions in
        one team or project, or to sessions working on one repository.
      </Head>
      {editing === 'new' && form()}
      {list?.length === 0 && editing !== 'new' && <p class="empty">No MCP servers yet.</p>}
      <div class="cards">
        {list?.map(m => editing === m.id ? form(m) : (
          <div key={m.id} class={'card mcp' + (m.enabled ? '' : ' off')}>
            <div class="card-head">
              <b class="mono">{m.name}</b>
              <span class="tag">{m.type === 'http' ? 'HTTP' : 'stdio'}</span>
              <span class={'tag' + (m.scope && !m.scope.startsWith('env:') ? ' mono' : '')}>{scopeLabel(m.scope)}</span>
              <span class="actions push">
                <label class="toggle"><input type="checkbox" checked={m.enabled} onChange={() => toggle(m)} /> Enabled</label>
                <button class="btn sm" onClick={() => setEditing(m.id)}>Edit</button>
                <ConfirmButton small onConfirm={() => api('DELETE', `/api/mcp-servers/${m.id}`).then(reload)} />
              </span>
            </div>
            <div class="mono ellipsis muted">{m.type === 'http' ? m.url : joinArgs(m.command || [])}</div>
            {m.secret_keys.length > 0 && (
              <div class="row-meta">
                <span>{m.type === 'http' ? 'Headers' : 'Environment'}</span>
                {m.secret_keys.map(k => <code key={k}>{k}</code>)}
              </div>
            )}
          </div>
        ))}
      </div>
    </>
  );
}

function MCPForm({ server, envs, done, cancel }: { server?: MCPServer; envs: Environment[]; done: () => void; cancel: () => void }) {
  const teams = useTeams();
  const [name, setName] = useState(server?.name || '');
  const [type, setType] = useState<'http' | 'stdio'>(server?.type || 'http');
  const [url, setUrl] = useState(server?.url || '');
  const [command, setCommand] = useState(joinArgs(server?.command || []));
  const [scope, setScope] = useState(server?.scope || '');
  const [pickRepo, setPickRepo] = useState(false);
  const [rows, setRows] = useState<KV[]>((server?.secret_keys || []).map(key => ({ key, value: '', stored: true })));
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const repoMode = pickRepo || (scope !== '' && !scope.startsWith('env:'));
  const args = splitArgs(command);
  const save = async (e: Event) => {
    e.preventDefault();
    if (repoMode && !scope) return setErr('Choose a repository.');
    setBusy(true);
    try {
      const body = {
        name: name.trim(), scope, type, ...(type === 'http' ? { url: url.trim() } : { command: args }),
        secret: Object.fromEntries(rows.filter(r => r.key.trim()).map(r => [r.key.trim(), r.value])),
      };
      await api(server ? 'PUT' : 'POST', '/api/mcp-servers' + (server ? '/' + server.id : ''), body);
      toast(server ? `Saved ${body.name}` : `Added ${body.name}`);
      done();
    } catch (e) {
      setErr(errText(e));
      setBusy(false);
    }
  };
  return (
    <form class="card form mcp-form" onSubmit={save} onInput={() => setErr('')}>
      <h2>{server ? `Edit ${server.name}` : 'Add an MCP server'}</h2>
      <div class="grid2">
        <label>Name<input class="mono" required pattern="[A-Za-z0-9_\-]+" maxLength={64} placeholder="e.g. linear" spellcheck={false}
          value={name} onInput={e => setName(e.currentTarget.value)} />
          <span class="hint">Letters, digits, - and _.</span></label>
        <label>Scope
          <select value={repoMode ? 'repo' : scope} onChange={e => {
            const v = e.currentTarget.value;
            setPickRepo(v === 'repo');
            setScope(v === 'repo' ? '' : v);
          }}>
            <option value="">Global: every session</option>
            {!!teams?.length && <optgroup label="Team">{teams.filter(t => t.manages).map(t => <option key={t.id} value={'team:' + t.id}>{t.name}</option>)}</optgroup>}
            {envs.length > 0 && <optgroup label="Project">{envs.map(e => <option key={e.id} value={'env:' + e.id}>{e.name}</option>)}</optgroup>}
            <option value="repo">A repository…</option>
          </select>
          {repoMode && <RepoInput value={scope} set={setScope} />}
        </label>
      </div>
      <div class="field">
        <span class="field-label">Transport</span>
        <Seg value={type} set={setType} options={[['http', 'HTTP'], ['stdio', 'Command (stdio)']]} />
      </div>
      {type === 'http' ? (
        <label>URL<input class="mono" type="url" required placeholder="https://mcp.example.com/mcp" spellcheck={false}
          value={url} onInput={e => setUrl(e.currentTarget.value)} /></label>
      ) : (
        <label>Command
          <input class="mono" required placeholder="npx -y @modelcontextprotocol/server-github" spellcheck={false}
            value={command} onInput={e => setCommand(e.currentTarget.value)} />
          <span class="hint">Runs inside the sandbox. Quote arguments that contain spaces.
            {args.length > 1 && <> Arguments: {args.map((a, i) => <code key={i}>{a}</code>)}</>}</span>
        </label>
      )}
      <div class="field">
        <span class="field-label">{type === 'http' ? 'Headers' : 'Environment variables'}</span>
        <KeyValueEditor rows={rows} set={setRows} keyHint={type === 'http' ? 'Authorization' : 'API_KEY'}
          add={type === 'http' ? 'Add header' : 'Add variable'} />
        <span class="hint">Values are encrypted and write-only{server && rows.some(r => r.stored) ? '; leave one blank to keep it' : ''}.</span>
      </div>
      <div class="form-foot">
        {err ? <span class="err" role="alert">{err}</span> : <span />}
        <span class="actions">
          <button type="button" class="btn" onClick={cancel}>Cancel</button>
          <button class="btn primary" disabled={busy}>{server ? 'Save' : 'Add server'}</button>
        </span>
      </div>
    </form>
  );
}
