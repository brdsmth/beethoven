import { render } from 'preact';
import { useEffect, useState } from 'preact/hooks';
import {
  api, get, onAuthRequired, onGlobal, type Account, type AuthState, type Environment, type Info, type Session,
} from './api';
import { AutomationEdit, Automations } from './automations';
import { GitHubReturn, GitHubSetup, githubStep } from './github';
import { Insights } from './insights';
import { SlackReturn, slackStep } from './slack';
import { TargetPicker, fromTarget, targetProblem, toTarget, type Pick } from './pickers';
import { SessionView } from './session';
import { Settings } from './settings';
import {
  Dot, EffortSelect, ModelField, Seg, Toasts, ago, fail, go, modEnter, readPref, setEfforts, targetLabel, useRoute, useTick,
  writePref,
} from './util';

// Root asks who is signed in before showing anything; a 401 later (signed
// out elsewhere, or the login expired) asks again.
function Root() {
  const [auth, setAuth] = useState<AuthState>();
  const [error, setError] = useState('');
  useEffect(() => {
    const load = () => get<AuthState>('/api/auth').then(a => { setAuth(a); setError(''); }, e => setError(e.message));
    load();
    return onAuthRequired(load);
  }, []);
  if (!auth) return error ? <div class="auth"><p class="auth-error" role="alert">Can't reach the server: {error}</p></div> : null;
  if (auth.required && !auth.account) return <SignIn setup={auth.setup} done={setAuth} />;
  return <App account={auth.account} />;
}

function SignIn({ setup, done }: { setup: boolean; done: (a: AuthState) => void }) {
  const [f, setF] = useState({ name: '', email: '', password: '' });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const field = (k: keyof typeof f) => (e: Event) => {
    const v = (e.currentTarget as HTMLInputElement).value;
    setF(p => ({ ...p, [k]: v }));
  };
  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      done(await api<AuthState>('POST', setup ? '/api/auth/setup' : '/api/auth/login',
        setup ? f : { email: f.email, password: f.password }));
    } catch (err) {
      const msg = (err as Error).message;
      setError(msg.charAt(0).toUpperCase() + msg.slice(1) + '.');
      setBusy(false);
    }
  };
  document.title = (setup ? 'Create your account' : 'Sign in') + ' · Beethoven';
  return (
    <div class="auth">
      <form class="auth-card" onSubmit={submit}>
        <div class="brand">Beethoven</div>
        <div class="auth-head">
          <h1>{setup ? 'Create your account' : 'Sign in'}</h1>
          {setup && <p class="muted">This server is new. The account you create now owns it; you'll sign in with it from then on.</p>}
        </div>
        {setup && (
          <label class="field"><span class="field-label">Name</span>
            <input id="auth-name" name="name" autoFocus autoComplete="name" value={f.name} onInput={field('name')} />
          </label>
        )}
        <label class="field"><span class="field-label">Email</span>
          <input id="auth-email" name="email" type="email" required autoFocus={!setup} autoComplete="username" value={f.email}
            onInput={field('email')} />
        </label>
        <label class="field"><span class="field-label">Password</span>
          <input id="auth-password" name="password" type="password" required minLength={setup ? 8 : undefined}
            autoComplete={setup ? 'new-password' : 'current-password'} value={f.password} onInput={field('password')} />
          {setup && <span class="hint">At least 8 characters.</span>}
        </label>
        {error && <p class="auth-error" role="alert">{error}</p>}
        <button class="btn primary block" disabled={busy}>
          {busy ? (setup ? 'Creating…' : 'Signing in…') : setup ? 'Create account' : 'Sign in'}
        </button>
      </form>
    </div>
  );
}

function App({ account }: { account: Account | null }) {
  const route = useRoute();
  const [info, setInfo] = useState<Info>();
  const [drawer, setDrawer] = useState(false);
  useEffect(() => { get<Info>('/api/info').then(i => { setEfforts(i.efforts); setInfo(i); }, () => {}); }, [route[0]]);
  useEffect(() => setDrawer(false), [route.join('/')]);

  const [page, arg] = route;
  let view;
  if (githubStep()) view = <GitHubReturn />;
  else if (slackStep()) view = <SlackReturn />;
  else if (page === 's' && arg) view = <SessionView key={arg} id={arg} />;
  else if (page === 'automations') view = arg ? <AutomationEdit key={arg} id={arg} /> : <Automations />;
  else if (page === 'insights') view = <Insights />;
  else if (page === 'settings') view = <Settings info={info} route={route.slice(1)} />;
  else view = <NewSession info={info} />;
  if (page !== 's') document.title = 'Beethoven';

  return (
    <div class={'app' + (drawer ? ' drawer-open' : '')}>
      <header class="topbar">
        <button class="icon-btn" aria-label="Menu" onClick={() => setDrawer(!drawer)}>☰</button>
        <a class="brand" href="#/">Beethoven</a>
      </header>
      <Sidebar current={page === 's' ? arg : page || ''} account={account} />
      <div class="scrim" onClick={() => setDrawer(false)} />
      <main class="main">{view}</main>
      <Toasts />
    </div>
  );
}

function Sidebar({ current, account }: { current: string; account: Account | null }) {
  const [archived, setArchived] = useState(false);
  const [list, setList] = useState<Session[]>();
  const [envs, setEnvs] = useState<Environment[]>();
  useTick();
  useEffect(() => { get<Environment[]>('/api/environments').then(setEnvs, () => {}); }, [current]);
  useEffect(() => {
    const load = () => get<Session[]>(`/api/sessions?limit=200${archived ? '&archived=1' : ''}`).then(setList, fail);
    load();
    return onGlobal(e => {
      if (e.type === 'session') {
        const s: Session = e.data;
        setList(l => {
          const rest = (l || []).filter(x => x.id !== s.id);
          return s.archived === archived ? [s, ...rest] : rest;
        });
      }
    }, load); // a reconnect may have missed changes
  }, [archived]);

  return (
    <nav class="sidebar">
      <div class="side-top">
        <a class="brand" href="#/">Beethoven</a>
        <a class="btn primary block" href="#/">New session</a>
        <Seg value={archived ? 'archived' : 'active'} set={v => setArchived(v === 'archived')}
          options={[['active', 'Active'], ['archived', 'Archived']]} />
      </div>
      <div class="side-list">
        {list?.length === 0 && <p class="empty small">{archived ? 'No archived sessions.' : 'No sessions yet.'}</p>}
        {list?.map(s => (
          <a key={s.id} href={`#/s/${s.id}`} class={'item' + (s.id === current ? ' active' : '')}>
            <Dot status={s.status} />
            <span class="item-main">
              <span class="item-title">{s.parent_id && <span class="dim">↳ </span>}{s.title || 'Untitled'}</span>
              <span class="item-meta">{targetLabel(s, envs)} · {ago(s.updated_at)}</span>
            </span>
          </a>
        ))}
      </div>
      {account && (
        <div class="side-account">
          <span class="ellipsis" title={account.email}>{account.name || account.email}</span>
          <button class="btn sm" onClick={() => api('POST', '/api/auth/logout').then(() => location.reload(), fail)}>
            Sign out
          </button>
        </div>
      )}
      <div class="side-foot">
        <a href="#/automations" class={current === 'automations' ? 'active' : ''}>Automations</a>
        <a href="#/insights" class={current === 'insights' ? 'active' : ''}>Insights</a>
        <a href="#/settings" class={current === 'settings' ? 'active' : ''}>Settings</a>
      </div>
    </nav>
  );
}

const TARGET_PREF = 'beethoven.target';

function NewSession({ info }: { info?: Info }) {
  const [target, setTarget] = useState<Pick>(() => {
    const p = readPref<Pick>(TARGET_PREF);
    return { ...fromTarget(p), kind: ['scratch', 'repos', 'env'].includes(p?.kind as string) ? p!.kind : 'scratch' };
  });
  const [prompt, setPrompt] = useState('');
  const [model, setModel] = useState('');
  const [effort, setEffort] = useState('');
  const [busy, setBusy] = useState(false);
  const problem = targetProblem(target);
  const submit = async () => {
    if (!prompt.trim() || problem || busy) return;
    setBusy(true);
    try {
      const t = toTarget(target);
      const r = await api<{ session: Session }>('POST', '/api/sessions', {
        prompt, model, effort, ...(t.environment_id ? { environment_id: t.environment_id } : { repos: t.repos }),
      });
      writePref(TARGET_PREF, target);
      go('/s/' + r.session.id);
    } catch (e) {
      fail(e);
      setBusy(false);
    }
  };
  return (
    <div class="page narrow">
      <h1>New session</h1>
      {info && !info.github && <GitHubSetup />}
      <form class="form" onSubmit={e => { e.preventDefault(); submit(); }}>
        <textarea class="big" rows={8} autoFocus placeholder="What should the agent do?" value={prompt}
          onInput={e => setPrompt(e.currentTarget.value)} onKeyDown={modEnter(submit)} />
        <div class="field">
          <span class="field-label">Workspace</span>
          <TargetPicker v={target} set={setTarget} />
        </div>
        <div class="grid2">
          <label>Model<ModelField value={model} set={setModel} def={info?.model ? `Default (${info.model})` : 'Default'} /></label>
          <label>Effort<EffortSelect value={effort} set={setEffort} def={info?.effort ? `Default (${info.effort})` : 'Default'} /></label>
        </div>
        {info && !info.anthropic && (
          <p class="banner warn">
            No Anthropic credential is configured, so agents can't run. Add one in <a href="#/settings/credentials">Settings › Credentials</a>.
          </p>
        )}
        <div class="form-foot">
          <span class="hint">{problem || '⌘/Ctrl+Enter to start.'}</span>
          <button class="btn primary" disabled={busy || !prompt.trim() || !!problem}>{busy ? 'Starting…' : 'Start session'}</button>
        </div>
      </form>
    </div>
  );
}

render(<Root />, document.getElementById('app')!);
