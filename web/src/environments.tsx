import { useEffect, useState } from 'preact/hooks';
import { api, get, type Effective, type Environment, type RepoRef, type Secret, type SettingsInfo } from './api';
import { RepoRows, cleanRepos } from './pickers';
import { SecretsEditor } from './secrets';
import { TeamLabel, TeamMark, useMe, useTeamIndex, useTeams } from './teams';
import { ConfirmButton, EffortSelect, Group, Head, ModelField, SaveBar, errText, go, hashParam, num, plural, str, toast, useLoad } from './util';

export function Environments() {
  const [list] = useLoad<Environment[]>('/api/environments');
  const [secrets] = useLoad<Secret[]>('/api/secrets');
  const team = useTeamIndex();
  return (
    <>
      <Head title="Projects" actions={<a class="btn primary" href="#/settings/environments/new">New project</a>}>
        A reusable workspace: repositories, scripts that prepare the sandbox, secrets, and setting overrides.
        Choose one when you start a session or set up an automation. A team's projects are its members' alone.
      </Head>
      {list?.length === 0 && <p class="empty">No projects yet.</p>}
      <div class="rows">
        {list?.map(e => {
          const n = secrets?.filter(s => s.scope === 'env:' + e.id).length ?? 0;
          return (
            <a key={e.id} class="row link" href={`#/settings/environments/${e.id}`}>
              <TeamMark team={team(e.team_id)} />
              <span class="row-main">
                <span class="row-title">{e.name}</span>
                {e.description && <span class="row-desc">{e.description}</span>}
                <span class="row-meta">
                  {e.repos.length ? <span class="mono">{e.repos.map(r => r.repo).join(', ')}</span> : <span>No repositories</span>}
                  <span>{plural(n, 'secret')}</span>
                  {!e.inherit_secrets && <span>no global secrets</span>}
                </span>
              </span>
              <span class="dim">›</span>
            </a>
          );
        })}
      </div>
    </>
  );
}

type OF = { model: string; effort: string; instructions: string; image: string; cpus: string; memory_mb: string };
type EF = {
  name: string; description: string; repos: RepoRef[]; setup_script: string; start_script: string;
  inherit_secrets: boolean; overrides: OF;
};
const BLANK: EF = {
  name: '', description: '', repos: [], setup_script: '', start_script: '', inherit_secrets: true,
  overrides: { model: '', effort: '', instructions: '', image: '', cpus: '', memory_mb: '' },
};
const toEF = (e: Environment): EF => {
  const o = e.overrides;
  return {
    name: e.name, description: e.description, repos: e.repos.map(r => ({ repo: r.repo, base_branch: r.base_branch })),
    setup_script: e.setup_script, start_script: e.start_script, inherit_secrets: e.inherit_secrets,
    overrides: {
      model: o.model || '', effort: o.effort || '', instructions: o.instructions || '', image: o.image || '',
      cpus: str(o.cpus), memory_mb: str(o.memory_mb),
    },
  };
};
const body = (f: EF) => ({
  ...f, name: f.name.trim(), description: f.description.trim(), repos: cleanRepos(f.repos),
  overrides: { ...f.overrides, model: f.overrides.model.trim(), image: f.overrides.image.trim(), cpus: num(f.overrides.cpus), memory_mb: num(f.overrides.memory_mb) },
});

const TABS = [['configuration', 'Configuration'], ['secrets', 'Secrets'], ['overrides', 'Overrides']];

export function EnvironmentEdit({ id, tab = 'configuration' }: { id: string; tab?: string }) {
  const isNew = id === 'new';
  const [saved, setSaved] = useState<Environment>();
  const [f, setF] = useState<EF>(BLANK);
  const [team, setTeam] = useState(() => hashParam('team'));
  const teams = useTeams();
  const me = useMe();
  const [missing, setMissing] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [eff, reloadEff] = useLoad<Effective>(!isNew && `/api/secrets/effective?environment=${id}`);
  useEffect(() => {
    if (!isNew) get<Environment>(`/api/environments/${id}`).then(e => { setSaved(e); setF(toEF(e)); }, e => setMissing(errText(e)));
  }, [id]);
  if (missing) return <p class="empty">{missing}</p>;
  if (!isNew && !saved) return <p class="empty">Loading…</p>;

  const dirty = JSON.stringify(body(f)) !== JSON.stringify(body(saved ? toEF(saved) : BLANK));
  const set = (v: Partial<EF>) => { setF({ ...f, ...v }); setErr(''); };
  const save = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    try {
      if (isNew) {
        const env = await api<Environment>('POST', '/api/environments', { ...body(f), team_id: team });
        toast(`Created ${env.name}. Add its secrets next.`);
        go(`/settings/environments/${env.id}/secrets`);
        return;
      }
      const env = await api<Environment>('PUT', `/api/environments/${id}`, body(f));
      setSaved(env);
      setF(toEF(env));
      toast('Environment saved');
    } catch (e) { setErr(errText(e)); }
    setBusy(false);
  };
  // Saved straight away, leaving any other unsaved edits pending.
  const setInherit = async (inherit_secrets: boolean) => {
    try {
      const env = await api<Environment>('PUT', `/api/environments/${id}`, { ...body(toEF(saved!)), inherit_secrets });
      setSaved(env);
      setF({ ...f, inherit_secrets });
      reloadEff();
    } catch (e) { setErr(errText(e)); }
  };
  const remove = async () => {
    await api('DELETE', `/api/environments/${id}`);
    toast(`Deleted ${saved!.name}`);
    go('/settings/environments');
  };

  const saveBar = <SaveBar dirty={dirty || isNew} busy={busy} err={err} label={isNew ? 'Create environment' : 'Save changes'} />;
  return (
    <div class="form">
      <a class="back" href="#/settings/environments">‹ Projects</a>
      <Head title={isNew ? 'New project' : saved!.name}
        actions={!isNew && <ConfirmButton ask="Delete project" note="Its secrets and MCP servers go too." onConfirm={remove} />} />
      {isNew && !!teams?.length && (
        <label class="field team-field">
          <span class="field-label">Team</span>
          <select value={team} onChange={e => setTeam(e.currentTarget.value)}>
            <option value="">Workspace: everyone's</option>
            {teams.filter(t => !t.archived_at && (t.role || me?.admin)).map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
          <span class="hint">Only the team's members see and use it. This can't change later.</span>
        </label>
      )}
      {!isNew && saved?.team_id && <p class="crumbs"><TeamLabel team={teams?.find(t => t.id === saved.team_id)} /> <span class="dim">· its members see and use this project</span></p>}
      <nav class="tabs">
        {TABS.map(([k, label]) => isNew && k === 'secrets'
          ? <span key={k} class="tab disabled" title="Create the environment first">{label}</span>
          : <a key={k} class={'tab' + (k === tab ? ' on' : '')} href={`#/settings/environments/${id}/${k}`}>{label}</a>)}
      </nav>
      {tab !== 'secrets' ? (
        <form class="form" onSubmit={save}>
          {tab === 'overrides' ? <Overrides o={f.overrides} set={o => set({ overrides: { ...f.overrides, ...o } })} />
            : <Configuration f={f} set={set} />}
          {saveBar}
        </form>
      ) : !isNew && (
        <>
          <label class="toggle">
            <input type="checkbox" checked={saved!.inherit_secrets} onChange={e => setInherit(e.currentTarget.checked)} />
            Inherit global secrets
          </label>
          <p class="hint flush">
            Global secrets{saved?.team_id ? ", then the team's," : ''} apply here unless replaced by one of this project's. Repository
            secrets never apply inside a project. Credentials always reach the agent, whatever this setting.
          </p>
          <Group title="Project secrets">
            <SecretsEditor scope={'env:' + id} onChange={reloadEff} />
          </Group>
          <Group title="Effective secrets" hint="What a session in this environment receives, and from where.">
            {eff?.secrets.length === 0 && <p class="empty small">None.</p>}
            <div class="rows">
              {eff?.secrets.map(s => (
                <div key={s.name} class="row">
                  <span class="row-main mono">{s.name}</span>
                  {!!s.overrides?.length && <span class="hint">overrides Global</span>}
                  <span class={'badge' + (s.scope ? '' : ' plain')}>{!s.scope ? 'Global' : s.scope.startsWith('team:') ? 'Team' : 'This project'}</span>
                </div>
              ))}
            </div>
          </Group>
          {(dirty || err) && <form onSubmit={save}>{saveBar}</form>}
        </>
      )}
    </div>
  );
}

function Configuration({ f, set }: { f: EF; set: (v: Partial<EF>) => void }) {
  const bind = (k: 'name' | 'description' | 'setup_script' | 'start_script') =>
    ({ value: f[k], onInput: (e: Event) => set({ [k]: (e.currentTarget as HTMLInputElement).value }) });
  return (
    <>
      <div class="grid2">
        <label>Name<input required maxLength={100} placeholder="e.g. Web app" {...bind('name')} /></label>
        <label>Description<input placeholder="Optional" {...bind('description')} /></label>
      </div>
      <Group title="Repositories" hint={<>Each is cloned to <code>/workspace/&lt;name&gt;</code>. The first is the primary. A session copies this list when it starts.</>}>
        <RepoRows repos={f.repos} set={repos => set({ repos })} />
      </Group>
      <Group title="Scripts" hint={<>Both run with <code>sh -e</code> in the agent's working directory: the repository with one, <code>/workspace</code> otherwise.</>}>
        <label>Setup script
          <textarea class="mono" rows={6} spellcheck={false} placeholder={'npm ci\npip install -r requirements.txt'} {...bind('setup_script')} />
          <span class="hint">Runs once per sandbox, after cloning, and again whenever you change it. A failure is reported as a warning and retried on the next start.</span>
        </label>
        <label>Start script
          <textarea class="mono" rows={4} spellcheck={false} placeholder="docker compose up -d db" {...bind('start_script')} />
          <span class="hint">Runs every time the sandbox starts, after setup. It must succeed.</span>
        </label>
      </Group>
    </>
  );
}

function Overrides({ o, set }: { o: OF; set: (v: Partial<OF>) => void }) {
  const [g] = useLoad<SettingsInfo>('/api/settings');
  const s = g?.settings;
  const global = (v?: string | number, none = 'agent default') => `Global (${v || none})`;
  const bind = (k: keyof OF) => ({ value: o[k], onInput: (e: Event) => set({ [k]: (e.currentTarget as HTMLInputElement).value }) });
  return (
    <>
      <p class="hint flush">Applied on top of the <a href="#/settings/general">global settings</a>. Leave a field empty to use the global value.</p>
      <Group title="Agent">
        <div class="grid2">
          <label>Model<ModelField value={o.model} set={model => set({ model })} def={global(s?.model)} /></label>
          <label>Effort<EffortSelect value={o.effort} set={effort => set({ effort })} def={global(s?.effort)} /></label>
        </div>
        <label>Instructions
          <textarea rows={4} placeholder="e.g. This is a pnpm monorepo; run pnpm test from the root." {...bind('instructions')} />
          <span class="hint">Added after the global instructions.</span>
        </label>
      </Group>
      <Group title="Sandbox" hint="Applies when a session's sandbox is first created.">
        <label>Image<input class="mono" spellcheck={false} placeholder={global(s?.image || g?.defaults.image, '')} {...bind('image')} /></label>
        <div class="grid2">
          <label>CPUs<input type="number" min="0" max="64" step="any" placeholder={global(s?.cpus, 'no limit')} {...bind('cpus')} /></label>
          <label>Memory (MB)<input type="number" min="0" placeholder={global(s?.memory_mb, 'no limit')} {...bind('memory_mb')} /></label>
        </div>
      </Group>
    </>
  );
}
