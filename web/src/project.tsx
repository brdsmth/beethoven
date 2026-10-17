import { useEffect, useState } from 'preact/hooks';
import { api, get, resetGitHub, withFiles, type Environment, type Info, type Session } from './api';
import { AttachButton, PendingFiles, useFiles } from './attach';
import { GitHubSetup } from './github';
import { TARGET_PREF } from './pickers';
import { scopeTeam, setScope, useMe, useScope, useTeams } from './state';
import { TeamMark } from './teams';
import { Seg, errText, go, modEnter, toast, writePref } from './util';

// Starting a project: the server creates a GitHub repository with a first
// commit and an environment for it, then starts the first session there.

/** An account the repository can belong to; problem says why the GitHub App can't create one there. */
type Owner = { login: string; type: string; problem?: string; fix_url?: string };
/** What a project is built with: the environment's setup script installs it. */
type Stack = { id: string; name: string; hint: string; setup: string };
type Options = { owners: Owner[]; stacks: Stack[]; error?: string };

/** GitHub keeps letters, digits, '.', '-' and '_' in a repository name and turns anything else into '-'. */
const repoName = (s: string) => s.replace(/[^A-Za-z0-9._-]+/g, '-');

export function NewProject({ info }: { info?: Info }) {
  const [opts, setOpts] = useState<Options>();
  const [owner, setOwner] = useState('');
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [visibility, setVisibility] = useState<'private' | 'public'>('private');
  const [stack, setStack] = useState('');
  const [variables, setVariables] = useState('');
  const [prompt, setPrompt] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const me = useMe();
  const teams = useTeams();
  const [scope] = useScope();
  const mine = teams?.filter(t => !t.archived_at && (t.role || me?.admin)) || [];
  const usable = (id?: string) => !!id && mine.some(t => t.id === id);
  const [team, setTeam] = useState<string>();
  const teamID = team ?? (usable(scopeTeam(scope)) ? scopeTeam(scope) : usable(me?.default_team_id) ? me!.default_team_id : '');
  const teamObj = teams?.find(t => t.id === teamID);
  const f = useFiles();
  const load = () => get<Options>('/api/projects/options').then(o => {
    setOpts(o);
    // Keep the choice if it still exists, else the first account that works.
    setOwner(cur => o.owners.some(x => x.login === cur) ? cur : (o.owners.find(x => !x.problem) || o.owners[0])?.login || '');
  }, e => setErr(errText(e)));
  useEffect(() => { if (info?.github) load(); }, [info?.github]);

  if (info && !info.github) {
    return (
      <div class="page narrow">
        <h1>New project</h1>
        <GitHubSetup />
        <p class="muted">Connect GitHub first: a project starts with a repository there.</p>
      </div>
    );
  }
  const chosen = opts?.owners.find(o => o.login === owner);
  const st = opts?.stacks.find(s => s.id === stack);
  const trimmed = name.replace(/^[-.]+|[-.]+$/g, '');
  const start = !!prompt.trim() || f.files.length > 0;
  const problem = !opts ? '' : !opts.owners.length ? 'There is no GitHub account to create the repository in.'
    : chosen?.problem ? 'The GitHub App needs permission first.' : !trimmed ? 'Name the repository.' : '';
  const submit = async () => {
    if (!opts || problem || busy) return;
    setBusy(true);
    setErr('');
    try {
      const r = await api<{ repo: { repo: string }; environment: Environment; session?: Session }>('POST', '/api/projects',
        withFiles({ owner, name: trimmed, description, public: visibility === 'public', stack, variables, prompt, team_id: teamID }, f.files));
      resetGitHub(); // pickers list the new repository
      if (teamID) setScope(teamID); // the sidebar shows the team's work
      writePref(TARGET_PREF, { kind: 'env', environment_id: r.environment.id, repos: [] }); // new sessions start there
      toast(`Created ${r.repo.repo}`);
      go(r.session ? '/s/' + r.session.id : '/');
    } catch (e) {
      setErr(errText(e));
      setBusy(false);
    }
  };

  return (
    <div class="page narrow">
      <h1>New project</h1>
      <p class="muted lead">
        Creates a GitHub repository with a first commit, and an environment that prepares the sandbox and holds the
        project's variables. Then the agent can start building.
      </p>
      {opts?.error && <p class="banner warn" role="alert">Couldn't ask GitHub where repositories can go: {opts.error}</p>}
      <form class="form" onSubmit={e => { e.preventDefault(); submit(); }}>
        <div class="field">
          <span class="field-label">Repository</span>
          <div class="repo-new">
            <select aria-label="Owner" value={owner} disabled={!opts?.owners.length} onChange={e => setOwner(e.currentTarget.value)}>
              {!opts && <option value="">Loading…</option>}
              {opts?.owners.map(o => <option key={o.login} value={o.login}>{o.login}</option>)}
            </select>
            <span class="dim">/</span>
            <input class="mono" aria-label="Repository name" placeholder="my-project" required autoFocus spellcheck={false}
              autocomplete="off" maxLength={100} value={name} onInput={e => setName(repoName(e.currentTarget.value))} />
          </div>
          {chosen?.problem && (
            <p class="banner warn">
              {chosen.problem}{' '}
              {chosen.fix_url && <a href={chosen.fix_url} target="_blank" rel="noopener">Open GitHub ↗</a>}{' '}
              <button type="button" class="btn sm" onClick={load}>Check again</button>
            </p>
          )}
          {opts && !opts.owners.length && !opts.error && (
            <p class="hint">The GitHub App isn't installed on any account yet. <a href="#/settings/github">Install it</a>, then come back.</p>
          )}
          <Seg value={visibility} set={setVisibility} options={[['private', 'Private'], ['public', 'Public']]} />
        </div>
        {mine.length > 0 && (
          <label>Team
            <span class="with-mark">
              <TeamMark team={teamObj} />
              <select value={teamID} onChange={e => setTeam(e.currentTarget.value)}>
                <option value="">Workspace: no team</option>
                {mine.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
              </select>
            </span>
            <span class="hint">{teamObj ? `The project, its variables and its sessions are ${teamObj.name}'s.` : 'Everyone here can use the project.'}</span>
          </label>
        )}
        <label>Description
          <input placeholder="Optional: one line about the project" maxLength={350} value={description}
            onInput={e => setDescription(e.currentTarget.value)} />
        </label>
        <label>Built with
          <select value={stack} onChange={e => setStack(e.currentTarget.value)}>
            {(opts?.stacks || [{ id: '', name: 'Anything' }]).map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
          {st && <span class="hint">{st.hint}</span>}
        </label>
        {st?.setup && (
          <details class="peek">
            <summary>Setup script</summary>
            <pre>{st.setup}</pre>
            <p class="hint">Runs once in each new sandbox. Change it later in the environment's settings.</p>
          </details>
        )}
        <label>Variables
          <textarea class="mono" rows={4} spellcheck={false} placeholder={'DATABASE_URL=postgres://…\nOPENAI_API_KEY=sk-…'}
            value={variables} onInput={e => setVariables(e.currentTarget.value)} />
          <span class="hint">Optional: paste a .env file. They're stored encrypted as the environment's secrets, and every session gets them as environment variables.</span>
        </label>
        <div class="field">
          <span class="field-label">First task</span>
          <div class={'prompt-box' + (f.over ? ' dropping' : '')}>
            <textarea rows={5} placeholder="What should the agent build first? Leave empty to only set the project up."
              value={prompt} onInput={e => setPrompt(e.currentTarget.value)} onKeyDown={modEnter(submit)} onPaste={f.onPaste} />
            <div class="prompt-files">
              <PendingFiles files={f.files} remove={f.remove} />
              <AttachButton add={f.add} label="Attach files" />
            </div>
          </div>
        </div>
        <div class="form-foot">
          {err ? <span class="err" role="alert">{err}</span> : <span class="hint">{problem || (trimmed && owner
            ? <>Creates <span class="mono">{owner}/{trimmed}</span>{start ? ' and starts a session there.' : '.'}</>
            : '')}</span>}
          <button class="btn primary" disabled={busy || !opts || !!problem}>
            {busy ? 'Creating…' : start ? 'Create and start' : 'Create project'}
          </button>
        </div>
      </form>
    </div>
  );
}
