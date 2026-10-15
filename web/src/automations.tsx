import { useEffect, useState } from 'preact/hooks';
import { api, get, type Automation, type Environment, type Prompt, type Session, type Spec } from './api';
import { span } from './insights';
import { TargetPicker, fromTarget, targetProblem, toTarget, type Pick } from './pickers';
import { Chip, ConfirmButton, EffortSelect, ModelField, ago, fail, go, money, targetLabel, toast, useForm, useLoad, when } from './util';

const MAX_FAILURES = 3;
const toSpec = ({ name, environment_id, repos, prompt, model, effort, schedule, reuse, enabled }: Spec): Spec => ({
  name, environment_id: environment_id || '', repos: repos.map(r => ({ repo: r.repo, base_branch: r.base_branch })),
  prompt, model, effort, schedule, reuse, enabled,
});

const runNow = (a: Automation) =>
  api<{ session: Session }>('POST', `/api/automations/${a.id}/run`).then(r => go('/s/' + r.session.id), fail);

export function Automations() {
  const [list, load] = useLoad<Automation[]>('/api/automations');
  const [envs] = useLoad<Environment[]>('/api/environments');
  const toggle = (a: Automation) => api('PUT', `/api/automations/${a.id}`, { ...toSpec(a), enabled: !a.enabled }).then(load, fail);
  return (
    <div class="page">
      <div class="page-head">
        <h1>Automations</h1>
        <a class="btn primary" href="#/automations/new">New automation</a>
      </div>
      {list?.length === 0 && <p class="empty">No automations yet. An automation runs a saved prompt on a schedule or when its webhook is called.</p>}
      <div class="rows">
        {list?.map(a => (
          <div key={a.id} class="row">
            <div class="row-main">
              <a class="row-title" href={`#/automations/${a.id}`}>{a.name}</a>
              <div class="row-meta">
                {a.environment_id ? <span>Environment: {envs ? envs.find(e => e.id === a.environment_id)?.name ?? 'deleted' : '…'}</span>
                  : <span class={a.repos.length ? 'mono' : ''}>{targetLabel(a)}</span>}
                <span class="mono">{a.schedule || 'webhook / manual'}</span>
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
  });
  const [target, setTarget] = useState<Pick>(() => fromTarget());
  const [current, setCurrent] = useState<Automation>();
  const [runs, setRuns] = useState<Prompt[]>();
  const [hook, setHook] = useState<{ id: string; url: string; token: string }>();
  const [busy, setBusy] = useState(false);

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
        <div class="field">
          <span class="field-label">Workspace</span>
          <TargetPicker v={target} set={setTarget} />
        </div>
        <div class="grid2">
          <label>Model<ModelField value={f.model} set={model => setF(p => ({ ...p, model }))} def="Default" /></label>
          <label>Effort<EffortSelect value={f.effort} set={effort => setF(p => ({ ...p, effort }))} def="Default" /></label>
        </div>
        <label>Prompt<textarea rows={6} required {...bind('prompt')} /></label>
        <label>Schedule
          <input class="mono" placeholder="0 9 * * 1-5" spellcheck={false} {...bind('schedule')} />
          <span class="hint">
            Five-field cron: <code>0 9 * * 1-5</code> weekdays at 9:00, <code>*/30 * * * *</code> every 30 minutes,
            {' '}<code>CRON_TZ=America/New_York 0 9 * * *</code> with a time zone. Leave empty to run only from the webhook or by hand.
          </span>
        </label>
        <label class="toggle"><input type="checkbox" {...check('reuse')} /> Reuse one session for every run</label>
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
