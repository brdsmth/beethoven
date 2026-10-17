import { useEffect, useId, useState } from 'preact/hooks';
import {
  githubBranches, githubRepos, isRepo, type Effective, type Environment, type GitHubRepo, type RepoRef, type Target,
} from './api';
import { Seg, plural, useLoad } from './util';

export const MAX_REPOS = 10;
const blank = (): RepoRef => ({ repo: '', base_branch: '' });

/** A repository name with suggestions from the GitHub token's repositories. */
export function RepoInput({ value, set, placeholder = 'owner/name' }: { value: string; set: (v: string) => void; placeholder?: string }) {
  const id = useId();
  const [repos, setRepos] = useState<GitHubRepo[]>([]);
  useEffect(() => { githubRepos().then(setRepos); }, []);
  return (
    <>
      <input class="mono" list={id} placeholder={placeholder} spellcheck={false} autocomplete="off" aria-label="Repository"
        value={value} onInput={e => set(e.currentTarget.value.trim())} />
      <datalist id={id}>
        {repos.map(r => <option key={r.repo} value={r.repo}>{r.description || (r.private ? 'private' : '')}</option>)}
      </datalist>
    </>
  );
}

/** A base branch, suggesting the repository's branches once focused. */
function BranchInput({ repo, value, set }: { repo: string; value: string; set: (v: string) => void }) {
  const id = useId();
  const [list, setList] = useState<{ repo: string; names: string[] }>();
  const [def, setDef] = useState('');
  useEffect(() => { githubRepos().then(rs => setDef(rs.find(r => r.repo === repo)?.default_branch || '')); }, [repo]);
  const load = () => { if (isRepo(repo)) githubBranches(repo).then(names => setList({ repo, names })); };
  return (
    <>
      <input class="mono" list={id} placeholder={def ? `${def} (default)` : 'default branch'} spellcheck={false}
        autocomplete="off" aria-label="Base branch" value={value} onFocus={load} onInput={e => set(e.currentTarget.value.trim())} />
      <datalist id={id}>{list?.repo === repo && list.names.map(b => <option key={b} value={b} />)}</datalist>
    </>
  );
}

/** An ordered list of repositories; the first is the primary. */
export function RepoRows({ repos, set }: { repos: RepoRef[]; set: (r: RepoRef[]) => void }) {
  const edit = (i: number, v: Partial<RepoRef>) => set(repos.map((r, j) => (j === i ? { ...r, ...v } : r)));
  const move = (i: number, d: number) => {
    const r = [...repos];
    [r[i], r[i + d]] = [r[i + d], r[i]];
    set(r);
  };
  return (
    <div class="repo-rows">
      {repos.map((r, i) => (
        <div key={i} class="repo-row">
          <span class="repo-n">{i ? <span class="dim">{i + 1}</span> : <span class="badge">primary</span>}</span>
          <span class="repo-name"><RepoInput value={r.repo} set={v => edit(i, { repo: v })} /></span>
          <span class="repo-branch"><BranchInput repo={r.repo} value={r.base_branch} set={v => edit(i, { base_branch: v })} /></span>
          <span class="repo-tools">
            <button type="button" class="btn icon" title="Move up" aria-label="Move up" disabled={!i} onClick={() => move(i, -1)}>↑</button>
            <button type="button" class="btn icon" title="Move down" aria-label="Move down" disabled={i === repos.length - 1}
              onClick={() => move(i, 1)}>↓</button>
            <button type="button" class="btn icon" title="Remove" aria-label="Remove repository"
              onClick={() => set(repos.filter((_, j) => j !== i))}>✕</button>
          </span>
        </div>
      ))}
      {repos.length < MAX_REPOS && (
        <button type="button" class="btn sm add" onClick={() => set([...repos, blank()])}>+ Add repository</button>
      )}
    </div>
  );
}

export const cleanRepos = (repos: RepoRef[]) =>
  repos.filter(r => r.repo.trim()).map(r => ({ repo: r.repo.trim(), base_branch: r.base_branch.trim() }));

/** Where the last session started, remembered for the next. */
export const TARGET_PREF = 'beethoven.target';

export type Kind = 'scratch' | 'repos' | 'env';
export type Pick = Target & { kind: Kind };
export const toTarget = (p: Pick): Target => ({
  environment_id: p.kind === 'env' ? p.environment_id : '',
  repos: p.kind === 'repos' ? cleanRepos(p.repos) : [],
});
export const fromTarget = (t?: Partial<Target>): Pick => {
  const repos = (t?.repos || []).map(r => ({ repo: r.repo, base_branch: r.base_branch }));
  const kind: Kind = t?.environment_id ? 'env' : repos.length ? 'repos' : 'scratch';
  return { kind, environment_id: t?.environment_id || '', repos: repos.length ? repos : [blank()] };
};
/** Why a target cannot be used yet, if it cannot. */
export const targetProblem = (p: Pick) =>
  p.kind === 'env' && !p.environment_id ? 'Choose a project.'
    : p.kind === 'repos' && !cleanRepos(p.repos).length ? 'Add a repository, or choose Scratch.' : '';

/** Where a session works: a scratch workspace, repositories, or an
 * environment (a project). With team, only the team's and the workspace's
 * projects are offered. */
export function TargetPicker({ v, set, team }: { v: Pick; set: (v: Pick) => void; team?: string }) {
  const [all] = useLoad<Environment[]>('/api/environments');
  const envs = team === undefined ? all : all?.filter(e => !e.team_id || e.team_id === team);
  const env = envs?.find(e => e.id === v.environment_id);
  const [eff] = useLoad<Effective>(v.kind === 'env' && env ? `/api/secrets/effective?environment=${env.id}` : false);
  useEffect(() => { // a remembered environment may have been deleted
    if (v.kind === 'env' && envs?.length && !env) set({ ...v, environment_id: envs[0].id });
  }, [v.kind, envs]);
  return (
    <div class="target">
      <Seg value={v.kind} set={kind => set({ ...v, kind })}
        options={[['scratch', 'Scratch'], ['repos', 'Repositories'], ['env', 'Project']]} />
      {v.kind === 'scratch' && <p class="hint">An empty workspace without a repository.</p>}
      {v.kind === 'repos' && <RepoRows repos={v.repos} set={repos => set({ ...v, repos })} />}
      {v.kind === 'env' && envs?.length === 0 && (
        <p class="hint">No projects yet. <a href="#/settings/environments/new">Create one</a> to reuse repositories, scripts and secrets.</p>
      )}
      {v.kind === 'env' && !!envs?.length && (
        <div class="env-pick">
          <select aria-label="Project" value={v.environment_id} onChange={e => set({ ...v, environment_id: e.currentTarget.value })}>
            {envs.map(e => <option key={e.id} value={e.id}>{e.name}</option>)}
          </select>
          {env && (
            <div class="hint">
              {env.description && <div>{env.description}</div>}
              <span class="mono">{env.repos.length ? env.repos.map(r => r.repo).join(', ') : 'No repositories'}</span>
              {eff && ` · ${plural(eff.secrets.length, 'secret')} apply`}
              {' · '}<a href={`#/settings/environments/${env.id}`}>Edit project</a>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export type KV = { key: string; value: string; stored?: boolean };

/** Name/value rows with write-only values: stored ones show blank and keep their value unless replaced. */
export function KeyValueEditor({ rows, set, keyHint, add }: { rows: KV[]; set: (r: KV[]) => void; keyHint: string; add: string }) {
  const edit = (i: number, v: Partial<KV>) => set(rows.map((r, j) => (j === i ? { ...r, ...v } : r)));
  return (
    <div class="kv-rows">
      {rows.map((r, i) => (
        <div key={i} class="kv-row">
          <input class="mono" placeholder={keyHint} spellcheck={false} autocomplete="off" aria-label="Name" value={r.key}
            readOnly={r.stored} title={r.stored ? 'Remove and add it again to rename' : undefined}
            onInput={e => edit(i, { key: e.currentTarget.value })} />
          <input type="password" autocomplete="new-password" aria-label="Value" value={r.value}
            placeholder={r.stored ? 'Leave blank to keep' : 'Value'} onInput={e => edit(i, { value: e.currentTarget.value })} />
          <button type="button" class="btn icon" title="Remove" aria-label="Remove" onClick={() => set(rows.filter((_, j) => j !== i))}>✕</button>
        </div>
      ))}
      <button type="button" class="btn sm add" onClick={() => set([...rows, { key: '', value: '' }])}>+ {add}</button>
    </div>
  );
}
