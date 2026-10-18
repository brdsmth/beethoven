import { useEffect, useState } from 'preact/hooks';
import { api, get, type Automation, type Condition, type Environment, type EventKind, type Prompt, type Session, type Spec, type Trigger } from './api';
import { span } from './insights';
import { TargetPicker, fromTarget, targetProblem, toTarget, type Pick } from './pickers';
import { TeamLabel, useMe, useTeamIndex, useTeams } from './teams';
import { Chip, ConfirmButton, EffortSelect, ModelField, ago, fail, go, hashParam, money, targetLabel, toast, useForm, useLoad, when } from './util';

const MAX_FAILURES = 3;
const toSpec = (a: Automation): Spec => ({
  name: a.name, team_id: a.team_id || '', environment_id: a.environment_id || '',
  repos: a.repos.map(r => ({ repo: r.repo, base_branch: r.base_branch })),
  prompt: a.prompt, model: a.model, effort: a.effort, schedule: a.schedule, reuse: a.reuse, enabled: a.enabled,
  trigger: a.trigger_event ? { event: a.trigger_event, conditions: a.trigger_conditions || [] } : undefined,
});

type Kinds = { kinds: EventKind[]; webhook: { path: string; secret: boolean } };

/** How to say each condition type, and whether it can be negated. */
const CONDITIONS: Record<string, { label: string; placeholder: string; not?: string; regex?: boolean }> = {
  added_label: { label: 'Label added is', placeholder: 'agent, needs-fix' },
  label: { label: 'Has a label', placeholder: 'bug, p1', not: 'Has none of these' },
  branch: { label: 'Branch matches', placeholder: 'main, release/*' },
  base: { label: 'Target branch matches', placeholder: 'main' },
  actor: { label: 'Done by', placeholder: 'octocat', not: 'Not done by' },
  draft: { label: 'Draft', placeholder: 'true or false' },
  text: { label: 'Text contains', placeholder: '@beethoven', regex: true, not: "Text doesn't contain" },
  conclusion: { label: 'CI conclusion is', placeholder: 'failure, timed_out' },
  check_name: { label: 'Workflow or check', placeholder: 'CI, Deploy*' },
  review_state: { label: 'Review is', placeholder: 'changes_requested, approved' },
};

const describeTrigger = (a: Automation, kinds?: EventKind[]) =>
  a.trigger_event ? 'on ' + (kinds?.find(k => k.id === a.trigger_event)?.label.toLowerCase() ?? a.trigger_event) : '';

const runNow = (a: Automation) =>
  api<{ session: Session }>('POST', `/api/automations/${a.id}/run`).then(r => go('/s/' + r.session.id), fail);

export function Automations() {
  const [list, load] = useLoad<Automation[]>('/api/automations');
  const [envs] = useLoad<Environment[]>('/api/environments');
  const [kinds] = useLoad<Kinds>('/api/automations/events');
  const team = useTeamIndex();
  const toggle = (a: Automation) => api('PUT', `/api/automations/${a.id}`, { ...toSpec(a), enabled: !a.enabled }).then(load, fail);
  return (
    <div class="page">
      <div class="page-head">
        <h1>Automations</h1>
        <a class="btn primary" href="#/automations/new">New automation</a>
      </div>
      {list?.length === 0 && <p class="empty">No automations yet. An automation runs a saved prompt on a schedule, when something happens in
        a repository (an issue labeled, a pull request opened…), or when its webhook is called.</p>}
      <div class="rows">
        {list?.map(a => (
          <div key={a.id} class="row">
            <div class="row-main">
              <a class="row-title" href={`#/automations/${a.id}`}>{a.name}</a>
              <div class="row-meta">
                {a.team_id && <TeamLabel team={team(a.team_id)} />}
                {a.environment_id ? <span>Project: {envs ? envs.find(e => e.id === a.environment_id)?.name ?? 'deleted' : '…'}</span>
                  : <span class={a.repos.length ? 'mono' : ''}>{targetLabel(a)}</span>}
                {a.trigger_event && <span>{describeTrigger(a, kinds?.kinds)}</span>}
                {(a.schedule || !a.trigger_event) && <span class="mono">{a.schedule || 'webhook / manual'}</span>}
                {a.enabled && a.next_run_at && <span>next {when(a.next_run_at)}</span>}
                {!a.enabled && a.failures >= MAX_FAILURES ? <span class="err">paused after {a.failures} failures</span>
                  : a.failures > 0 && <span class="warn">{a.failures} failed in a row</span>}
              </div>
            </div>
            <div class="actions">
              <label class="toggle"><input type="checkbox" checked={a.enabled} onChange={() => toggle(a)} /> Enabled</label>
              <button class="btn" onClick={() => runNow(a)}>Run now</button>
              <a class="btn" href={`#/automations/${a.id}`}>Edit</a>
              <ConfirmButton ask="Delete automation" note="Its sessions are kept." onConfirm={() => api('DELETE', `/api/automations/${a.id}`).then(load)} />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

export function AutomationEdit({ id }: { id: string }) {
  const isNew = id === 'new';
  const { f, setF, bind, check } = useForm<Spec>({
    name: '', environment_id: '', repos: [], prompt: '', model: '', effort: '', schedule: '', reuse: false, enabled: true,
    team_id: isNew ? hashParam('team') : '',
  });
  const teams = useTeams();
  const me = useMe();
  const [target, setTarget] = useState<Pick>(() => fromTarget());
  const [current, setCurrent] = useState<Automation>();
  const [runs, setRuns] = useState<Prompt[]>();
  const [hook, setHook] = useState<{ id: string; url: string; token: string }>();
  const [busy, setBusy] = useState(false);
  const [kinds] = useLoad<Kinds>('/api/automations/events');

  useEffect(() => {
    if (isNew) return;
    get<Automation>(`/api/automations/${id}`).then(a => {
      setCurrent(a);
      setF(toSpec(a));
      setTarget(fromTarget(a));
    }, fail);
    get<Prompt[]>(`/api/automations/${id}/runs`).then(setRuns, fail);
  }, [id]);

  const problem = targetProblem(target);
  const save = async (e: Event) => {
    e.preventDefault();
    if (problem) return toast(problem, true);
    setBusy(true);
    try {
      const spec = { ...f, ...toTarget(target) };
      if (isNew) {
        const r = await api<{ automation: Automation; hook_token: string; hook_path: string }>('POST', '/api/automations', spec);
        setHook({ id: r.automation.id, url: location.origin + r.hook_path, token: r.hook_token });
      } else {
        setCurrent(await api<Automation>('PUT', `/api/automations/${id}`, spec));
        toast('Saved');
      }
    } catch (err) { fail(err); }
    setBusy(false);
  };

  if (hook) return <HookCreated {...hook} />;
  if (!isNew && !current) return <div class="page"><p class="empty">Loading…</p></div>;
  return (
    <div class="page narrow">
      <div class="page-head">
        <h1>{isNew ? 'New automation' : current!.name}</h1>
        {current && <button class="btn" onClick={() => runNow(current)}>Run now</button>}
      </div>
      <form class="form" onSubmit={save}>
        <label>Name<input required {...bind('name')} /></label>
        {isNew && !!teams?.length && (
          <label>Team
            <select value={f.team_id} onChange={e => setF(p => ({ ...p, team_id: e.currentTarget.value }))}>
              <option value="">Workspace: no team</option>
              {teams.filter(t => !t.archived_at && (t.role || me?.admin)).map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
            </select>
            <span class="hint">It runs as you, in the team; its runs are the team's sessions. This can't change later.</span>
          </label>
        )}
        {!isNew && current?.team_id && <p class="crumbs"><TeamLabel team={teams?.find(t => t.id === current.team_id)} /></p>}
        <div class="field">
          <span class="field-label">Workspace</span>
          <TargetPicker v={target} set={setTarget} team={f.team_id || ''} />
        </div>
        <div class="grid2">
          <label>Model<ModelField value={f.model} set={model => setF(p => ({ ...p, model }))} def="Default" /></label>
          <label>Effort<EffortSelect value={f.effort} set={effort => setF(p => ({ ...p, effort }))} def="Default" /></label>
        </div>
        <label>Prompt<textarea rows={6} required {...bind('prompt')} /></label>
        <TriggerEditor kinds={kinds} v={f.trigger} set={trigger => setF(p => ({ ...p, trigger }))} />
        <label>Schedule
          <input class="mono" placeholder="0 9 * * 1-5" spellcheck={false} {...bind('schedule')} />
          <span class="hint">
            Five-field cron: <code>0 9 * * 1-5</code> weekdays at 9:00, <code>*/30 * * * *</code> every 30 minutes,
            {' '}<code>CRON_TZ=America/New_York 0 9 * * *</code> with a time zone. Leave empty to run only on events, from the webhook or by hand.
          </span>
        </label>
        <label class="toggle"><input type="checkbox" {...check('reuse')} />
          {f.trigger?.event ? ' Reuse one session per issue or pull request' : ' Reuse one session for every run'}</label>
        <label class="toggle"><input type="checkbox" {...check('enabled')} /> Enabled</label>
        {current && current.failures > 0 && (
          <p class="warn">{current.failures} run{current.failures > 1 ? 's' : ''} failed in a row
            {current.failures >= MAX_FAILURES && '; paused. Enabling it again resets the count'}.</p>
        )}
        <div class="form-foot">
          <a class="btn" href="#/automations">Cancel</a>
          <button class="btn primary" disabled={busy}>{isNew ? 'Create automation' : 'Save'}</button>
        </div>
      </form>
      {runs && (
        <section class="section">
          <h2>Runs</h2>
          {runs.length === 0 ? <p class="empty small">No runs yet.</p> : <RunStats runs={runs} />}
          <div class="rows">
            {runs.map(p => (
              <a key={p.id} class="row link" href={`#/s/${p.session_id}`}>
                <Chip status={p.status} />
                <span class="row-main">
                  <span>{p.source} run · {ago(p.created_at)}</span>
                  {p.error && <span class="row-meta err">{p.error}</span>}
                </span>
                <span class="dim">{money(p.cost_usd)}</span>
              </a>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

/** Picks the repository event that fires the automation and its conditions. */
function TriggerEditor({ kinds, v, set }: { kinds?: Kinds; v?: Trigger; set: (t?: Trigger) => void }) {
  const kind = kinds?.kinds.find(k => k.id === v?.event);
  const conds = v?.conditions ?? [];
  const setCond = (i: number, c?: Condition) =>
    set({ event: v!.event, conditions: c ? conds.map((x, j) => (j === i ? c : x)) : conds.filter((_, j) => j !== i) });
  const unused = kind?.conditions.filter(t => !conds.some(c => c.type === t)) ?? [];
  return (
    <div class="field">
      <span class="field-label">Repository event</span>
      <select value={v?.event ?? ''} onChange={e => {
        const event = e.currentTarget.value;
        const allowed = kinds?.kinds.find(k => k.id === event)?.conditions ?? [];
        set(event ? { event, conditions: conds.filter(c => allowed.includes(c.type)) } : undefined);
      }}>
        <option value="">None</option>
        {kinds?.kinds.map(k => <option key={k.id} value={k.id}>{k.label}</option>)}
      </select>
      {kind && (
        <div class="conditions">
          {conds.map((c, i) => {
            const meta = CONDITIONS[c.type] ?? { label: c.type, placeholder: '' };
            return (
              <div key={c.type} class="condition">
                <span class="condition-label">{c.negate && meta.not ? meta.not : meta.label}</span>
                <input value={c.values.join(', ')} placeholder={meta.placeholder} spellcheck={false}
                  onInput={e => setCond(i, { ...c, values: e.currentTarget.value.split(',').map(s => s.trim()) })} />
                {meta.not && <label class="toggle small"><input type="checkbox" checked={!!c.negate}
                  onChange={e => setCond(i, { ...c, negate: e.currentTarget.checked })} /> not</label>}
                {meta.regex && <label class="toggle small"><input type="checkbox" checked={!!c.regex}
                  onChange={e => setCond(i, { ...c, regex: e.currentTarget.checked })} /> regex</label>}
                <button type="button" class="btn small" onClick={() => setCond(i)}>Remove</button>
              </div>
            );
          })}
          {unused.length > 0 && (
            <select value="" onChange={e => set({ event: v!.event, conditions: [...conds, { type: e.currentTarget.value, values: [] }] })}>
              <option value="">Add a condition…</option>
              {unused.map(t => <option key={t} value={t}>{CONDITIONS[t]?.label ?? t}</option>)}
            </select>
          )}
          <span class="hint">
            Fires for events in the repositories above, when every condition holds. Lists match any of their values;
            branch patterns take <code>*</code>. Each run gets the issue or pull request with the prompt, and the agent can reply
            with comments and labels.
            {kinds && !kinds.webhook.secret && <> <b>This server isn't receiving GitHub events yet</b>: connect a GitHub App created
              from a public HTTPS address, or point a repository webhook at <code>{location.origin + kinds.webhook.path}</code> with
              {' '}<code>BEETHOVEN_GITHUB_WEBHOOK_SECRET</code> set.</>}
          </span>
        </div>
      )}
    </div>
  );
}

function HookCreated({ id, url, token }: { id: string; url: string; token: string }) {
  const curl = `curl -X POST ${url} \\\n  -H "Authorization: Bearer ${token}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"example":true}'`;
  const copy = (t: string) => navigator.clipboard?.writeText(t).then(() => toast('Copied'), fail);
  return (
    <div class="page narrow">
      <h1>Automation created</h1>
      <p class="warn">Copy the webhook token now. It is shown only once.</p>
      <div class="form">
        <label>Webhook URL<div class="copy"><code>{url}</code><button class="btn" onClick={() => copy(url)}>Copy</button></div></label>
        <label>Token<div class="copy"><code>{token}</code><button class="btn" onClick={() => copy(token)}>Copy</button></div></label>
        <label>Example
          <div class="copy"><pre class="code">{curl}</pre><button class="btn" onClick={() => copy(curl)}>Copy</button></div>
        </label>
        <p class="hint">The request body is passed to the agent along with the prompt. Send an Idempotency-Key header to make retries fire once.</p>
        <div class="form-foot">
          <a class="btn" href="#/automations">All automations</a>
          <a class="btn primary" href={`#/automations/${id}`}>Open automation</a>
        </div>
      </div>
    </div>
  );
}

/** How an automation's recent runs went. */
function RunStats({ runs }: { runs: Prompt[] }) {
  const done = runs.filter(p => p.finished_at && p.started_at);
  if (!done.length) return null;
  const ok = done.filter(p => p.status === 'done').length;
  const secs = done.map(p => (Date.parse(p.finished_at!) - Date.parse(p.started_at!)) / 1000).sort((a, b) => a - b);
  const median = secs.length % 2 ? secs[(secs.length - 1) / 2] : (secs[secs.length / 2 - 1] + secs[secs.length / 2]) / 2;
  const spent = runs.reduce((t, p) => t + p.cost_usd, 0);
  return (
    <p class="hint">
      Last {done.length} {done.length === 1 ? 'run' : 'runs'}: {Math.round((ok / done.length) * 100)}% succeeded · {span(median)} median
      · {money(spent)} spent
    </p>
  );
}
