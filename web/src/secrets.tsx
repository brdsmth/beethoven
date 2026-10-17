import { useState } from 'preact/hooks';
import { api, isRepo, scopeQuery, type Environment, type Secret } from './api';
import { RepoInput } from './pickers';
import { useTeams } from './state';
import { ConfirmButton, Group, Head, ago, errText, fail, go, plural, toast, useLoad } from './util';

type ImportResult = { imported: string[]; skipped: { line: number; reason: string }[] };

/** Lists, sets, deletes and imports the secrets of one scope. */
export function SecretsEditor({ scope, onChange }: { scope: string; onChange?: () => void }) {
  const [list, reload] = useLoad<Secret[]>(`/api/secrets?${scopeQuery(scope)}`);
  const [name, setName] = useState('');
  const [value, setValue] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const changed = () => { reload(); onChange?.(); };
  const save = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    try {
      await api('PUT', '/api/secrets', { scope, name: name.trim(), value });
      toast(`Saved ${name.trim()}`);
      setName('');
      setValue('');
      changed();
    } catch (e) { setErr(errText(e)); }
    setBusy(false);
  };
  const exists = list?.some(s => s.name === name.trim());
  return (
    <div class="secrets">
      {list?.length === 0 && <p class="empty small">No secrets here yet.</p>}
      <div class="rows">
        {list?.map(s => (
          <div key={s.name} class="row">
            <span class="row-main mono">{s.name}</span>
            <span class="dim small">updated {ago(s.updated_at)}</span>
            <button class="btn sm" onClick={() => { setName(s.name); setValue(''); }}>Replace</button>
            <ConfirmButton small onConfirm={() =>
              api('DELETE', `/api/secrets?${scopeQuery(scope)}&name=${encodeURIComponent(s.name)}`).then(changed)} />
          </div>
        ))}
      </div>
      <form class="inline-form" onSubmit={save}>
        <input class="mono" required placeholder="NAME" aria-label="Name" spellcheck={false} autocomplete="off"
          value={name} onInput={e => { setName(e.currentTarget.value); setErr(''); }} />
        <input type="password" required placeholder="Value" aria-label="Value" autocomplete="new-password"
          value={value} onInput={e => { setValue(e.currentTarget.value); setErr(''); }} />
        <button class="btn primary" disabled={busy || !name.trim() || !value}>{exists ? 'Replace' : 'Add secret'}</button>
      </form>
      {err && <p class="err small" role="alert">{err}</p>}
      <ImportEnv scope={scope} done={changed} />
    </div>
  );
}

function ImportEnv({ scope, done }: { scope: string; done: () => void }) {
  const [text, setText] = useState('');
  const [res, setRes] = useState<ImportResult>();
  const run = async () => {
    try {
      const r = await api<ImportResult>('POST', '/api/secrets/import', { scope, text });
      setRes(r);
      if (!r.skipped.length) setText('');
      if (r.imported.length) done();
    } catch (e) { fail(e); }
  };
  return (
    <details class="import">
      <summary>Import a .env file</summary>
      <textarea class="mono" rows={5} spellcheck={false} value={text} onInput={e => setText(e.currentTarget.value)}
        placeholder={'# Paste .env contents; comments, quotes and export are understood\nAPI_URL=https://example.com\nexport TOKEN="…"'} />
      <div class="form-foot">
        <span class="hint">Variables with existing names are replaced.</span>
        <button type="button" class="btn" disabled={!text.trim()} onClick={run}>Import</button>
      </div>
      {res && (
        <div class="import-result">
          {res.imported.length > 0 && <p class="ok">Imported {plural(res.imported.length, 'secret')}: <span class="mono">{res.imported.join(', ')}</span></p>}
          {res.skipped.map(s => <p key={s.line} class="warn">Line {s.line} skipped: {s.reason}</p>)}
          {!res.imported.length && !res.skipped.length && <p class="dim">Nothing to import.</p>}
        </div>
      )}
    </details>
  );
}

export function SecretsPage({ scope }: { scope: string }) {
  const [all, reload] = useLoad<Secret[]>('/api/secrets');
  const [envs] = useLoad<Environment[]>('/api/environments');
  const [adding, setAdding] = useState('');
  const counts: Record<string, number> = {};
  all?.forEach(s => { counts[s.scope] = (counts[s.scope] || 0) + 1; });
  const teams = useTeams();
  const repos = Object.keys(counts).filter(s => s && !s.startsWith('env:') && !s.startsWith('team:'));
  if (scope && !repos.includes(scope)) repos.push(scope);
  repos.sort();
  const href = (s: string) => '#/settings/secrets' + (s ? '/' + encodeURIComponent(s) : '');
  const pill = (s: string, label: string) => (
    <a key={s} href={href(s)} class={'pill' + (s === scope ? ' on' : '') + (s ? ' mono' : '')}>{label}<span class="count">{counts[s] || 0}</span></a>
  );
  return (
    <>
      <Head title="Secrets">Environment variables in the agent's sandbox. Values are encrypted and can't be read back.</Head>
      <div class="scopes">
        <div class="scope-group"><h3>Global</h3>{pill('', 'All sessions')}</div>
        <div class="scope-group">
          <h3>Repositories</h3>
          {repos.map(s => pill(s, s))}
          <form class="inline-form" onSubmit={e => { e.preventDefault(); go('/settings/secrets/' + encodeURIComponent(adding)); setAdding(''); }}>
            <RepoInput value={adding} set={setAdding} placeholder="Add a repository scope" />
            <button class="btn" disabled={!isRepo(adding)}>Add</button>
          </form>
        </div>
        {!!teams?.length && (
          <div class="scope-group">
            <h3>Teams</h3>
            {teams.filter(t => t.role || t.manages).map(t => (
              <a key={t.id} class="pill" href={`#/settings/teams/${t.slug}/secrets`} title="Edit on the team's page">
                {t.name}<span class="count">{counts['team:' + t.id] || 0}</span> ↗
              </a>
            ))}
          </div>
        )}
        {!!envs?.length && (
          <div class="scope-group">
            <h3>Projects</h3>
            {envs.map(e => (
              <a key={e.id} class="pill" href={`#/settings/environments/${e.id}/secrets`} title="Edit in the project">
                {e.name}<span class="count">{counts['env:' + e.id] || 0}</span> ↗
              </a>
            ))}
          </div>
        )}
      </div>
      <Group title={scope ? `Secrets for ${scope}` : 'Global secrets'}
        hint={scope ? `Sessions working on ${scope} receive these, over global ones with the same name.` : 'Every session receives these, unless its project opts out.'}>
        <SecretsEditor key={scope} scope={scope} onChange={reload} />
      </Group>
      <div class="note">
        <b>Precedence.</b> Global secrets come first, then the secrets of the session's team. A session on repositories adds
        each repository's secrets, the primary repository winning. A session in a project adds that project's secrets instead
        (repository secrets don't apply there) and keeps global ones only if the project inherits them. Global and repository
        secrets are for admins to change; a team's for its leads. Credentials are separate and always reach the agent.
      </div>
    </>
  );
}
