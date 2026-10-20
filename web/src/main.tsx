import { render } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import {
  api, get, onAuthRequired, setMe, withFiles, type Account, type AuthState, type Environment, type Info, type Session,
  type Visibility,
} from './api';
import { AttachButton, PendingFiles, useFiles } from './attach';
import { AutomationEdit, Automations } from './automations';
import { GitHubReturn, GitHubSetup, githubStep } from './github';
import { Insights } from './insights';
import { InviteAccept } from './invite';
import { adminNames, useAdmins } from './people';
import { TeamMark, scopeTeam, useMe, useScope, useTeams } from './teams';
import { NewProject } from './project';
import { SlackReturn, slackStep } from './slack';
import { TARGET_PREF, TargetPicker, fromTarget, targetProblem, toTarget, type Pick } from './pickers';
import { SessionView } from './session';
import { Settings } from './settings';
import { Sidebar } from './sidebar';
import { GetStarted, Tour, startersFor, unprepared } from './welcome';
import {
  EffortSelect, ModelField, Toasts, fail, go, hashParam, modEnter, readPref, setEfforts, title, useRoute, writePref,
} from './util';

// Root asks who is signed in before showing anything; a 401 later (signed
// out elsewhere, or the login expired) asks again.
function Root() {
  const [auth, setAuth] = useState<AuthState>();
  const [error, setError] = useState('');
  const route = useRoute();
  useEffect(() => {
    const load = () => get<AuthState>('/api/auth').then(a => { setAuth(a); setMe(a); setError(''); }, e => setError(e.message));
    load();
    return onAuthRequired(load);
  }, []);
  if (route[0] === 'invite' && route[1]) return <InviteAccept token={route[1]} />;
  if (!auth) return error ? <div class="auth"><p class="auth-error" role="alert">Can't reach the server: {error}</p></div> : null;
  if (auth.required && !auth.account) return <SignIn setup={auth.setup} done={a => { setAuth(a); setMe(a); }} />;
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
  title(setup ? 'Create your account' : 'Sign in');
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
  const me = useMe();
  let view;
  if (githubStep()) view = <GitHubReturn />;
  else if (slackStep()) view = <SlackReturn />;
  else if (page === 's' && arg) view = <SessionView key={arg} id={arg} />;
  else if (page === 'automations') view = arg ? <AutomationEdit key={arg} id={arg} /> : <Automations />;
  else if (page === 'insights') view = <Insights />;
  else if (page === 'teams') view = <Moved to={'/settings/' + route.join('/')} />; // team pages live in the settings
  else if (page === 'projects' && arg === 'new') view = <NewProject info={info} />;
  else if (page === 'settings') view = <Settings info={info} route={route.slice(1)} />;
  else if (page === 'welcome') view = <GetStarted info={info} />;
  else view = <NewSession info={info} />;
  if (page !== 's' && page !== 'welcome') title(page === 'automations' ? 'Automations' : page === 'insights' ? 'Insights'
    : page === 'settings' ? 'Settings' : page === 'projects' && arg === 'new' ? 'New project' : 'New session');

  return (
    <div class={'app' + (drawer ? ' drawer-open' : '')}>
      <header class="topbar">
        <button class="icon-btn" aria-label="Menu" onClick={() => setDrawer(!drawer)}>☰</button>
        <a class="brand" href="#/">Beethoven</a>
      </header>
      <Sidebar current={page === 's' ? arg : page || ''} account={account} />
      <div class="scrim" onClick={() => setDrawer(false)} />
      <main class="main">{view}</main>
      {me?.onboarding?.tour && <Tour />}
      <Toasts />
    </div>
  );
}

function NewSession({ info }: { info?: Info }) {
  const [target, setTarget] = useState<Pick>(() => {
    const p = readPref<Pick>(TARGET_PREF);
    return { ...fromTarget(p), kind: ['scratch', 'repos', 'env'].includes(p?.kind as string) ? p!.kind : 'scratch' };
  });
  const me = useMe();
  // A first session: what the invitation suggested, waiting in the prompt.
  const first = !!me?.onboarding && !me.onboarding.started;
  const [opened] = useState(() => first && !!hashParam('first')); // from getting started
  const [prompt, setPrompt] = useState(() => (opened && me?.onboarding?.prompt) || '');
  const [model, setModel] = useState('');
  const [effort, setEffort] = useState('');
  const [busy, setBusy] = useState(false);
  const teams = useTeams();
  const [envs, setEnvs] = useState<Environment[]>();
  useEffect(() => { if (first) get<Environment[]>('/api/environments').then(setEnvs, () => {}); }, [first]);
  const admins = useAdmins(!!info && !info.anthropic && !me?.admin);
  const [scope] = useScope();
  // The team the session belongs to: the switcher's, else the person's default.
  const [team, setTeam] = useState<string>();
  const [visibility, setVisibility] = useState<Visibility | ''>('');
  const mine = teams?.filter(t => !t.archived_at && (t.role || me?.admin)) || [];
  const usable = (id?: string) => !!id && mine.some(t => t.id === id);
  const chosen = team ?? (scope === 'workspace' ? '' : usable(scopeTeam(scope)) ? scopeTeam(scope)
    : usable(me?.default_team_id) ? me!.default_team_id : '');
  const teamObj = teams?.find(t => t.id === chosen);
  const keep = useRef(opened); // getting started chose the first session's project: keep it until the team changes
  useEffect(() => { // a team's default project
    if (!teamObj?.default_environment_id) return;
    if (keep.current) { keep.current = false; return; }
    setTarget(t => ({ ...t, kind: 'env', environment_id: teamObj.default_environment_id }));
  }, [chosen, teamObj?.default_environment_id]);
  const f = useFiles();
  const project = target.kind === 'env' ? envs?.find(e => e.id === target.environment_id) : undefined;
  const starters = startersFor(project);
  // Opened from getting started without a suggestion: the project's first starter.
  useEffect(() => {
    if (opened && !me?.onboarding?.prompt && project && unprepared(project)) setPrompt(p => p || starters[0].prompt);
  }, [project?.id]);
  const empty = !prompt.trim() && !f.files.length;
  const problem = targetProblem(target);
  const submit = async () => {
    if (empty || problem || busy) return;
    setBusy(true);
    try {
      const t = toTarget(target);
      const r = await api<{ session: Session }>('POST', '/api/sessions', withFiles({
        prompt, model, effort, ...(t.environment_id ? { environment_id: t.environment_id } : { repos: t.repos }),
        team_id: chosen, visibility,
      }, f.files));
      writePref(TARGET_PREF, target);
      if (me?.onboarding && !me.onboarding.started) get<AuthState>('/api/auth').then(setMe, () => {}); // it counts as their first
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
        {first && me?.onboarding?.prompt && prompt === me.onboarding.prompt && (
          <div class="from-inviter">
            <b>{me.onboarding.invited_by_name ? `${me.onboarding.invited_by_name} suggested this for your first session` : 'A first session to try'}</b>
            <span>Change it if you like, or pick something else below.</span>
          </div>
        )}
        <div class={'prompt-box' + (f.over ? ' dropping' : '')}>
          <textarea class="big" rows={8} autoFocus placeholder="What should the agent do?" value={prompt}
            onInput={e => setPrompt(e.currentTarget.value)} onKeyDown={modEnter(submit)} onPaste={f.onPaste} />
          <div class="prompt-files">
            <PendingFiles files={f.files} remove={f.remove} />
            <AttachButton add={f.add} label="Attach files" />
          </div>
        </div>
        {first && (
          <div class="field">
            <span class="hint">{unprepared(project) ? 'Good first sessions in a new project' : 'Or start from one of these'}</span>
            <div class="starters">
              {starters.map(s => (
                <button type="button" key={s.label} class={'starter' + (prompt === s.prompt ? ' on' : '')} onClick={() => setPrompt(s.prompt)}>
                  <span>{s.icon} {s.label}</span><span class="dim">{s.hint}</span>
                </button>
              ))}
            </div>
          </div>
        )}
        {(mine.length > 0 || me?.people) && (
          <div class="grid2">
            {mine.length > 0 && (
              <label>Team
                <span class="with-mark">
                  <TeamMark team={teamObj} />
                  <select value={chosen} onChange={e => setTeam(e.currentTarget.value)}>
                    <option value="">Workspace: no team</option>
                    {mine.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
                  </select>
                </span>
              </label>
            )}
            <label>Seen by
              <select value={visibility} onChange={e => setVisibility(e.currentTarget.value as Visibility | '')}>
                <option value="">{teamObj ? `The team's default (${teamObj.default_visibility === 'team' ? 'the team' : 'everyone'})` : 'Everyone'}</option>
                <option value="workspace">Everyone</option>
                {teamObj && <option value="team">The team</option>}
                {me?.account && <option value="private">Only me</option>}
              </select>
            </label>
          </div>
        )}
        <div class="field">
          <span class="field-label">Workspace</span>
          <TargetPicker v={target} set={t => {
            setTarget(t);
            const env = t.kind === 'env' && t.environment_id; // a team's project brings its team
            if (env) get<Environment>(`/api/environments/${env}`).then(e => { if (e.team_id) setTeam(e.team_id); }, () => {});
          }} team={chosen} />
        </div>
        <div class="grid2">
          <label>Model<ModelField value={model} set={setModel} def={info?.model ? `Default (${info.model})` : 'Default'} /></label>
          <label>Effort<EffortSelect value={effort} set={setEffort} def={info?.effort ? `Default (${info.effort})` : 'Default'} /></label>
        </div>
        {info && !info.anthropic && (
          <p class="banner warn">
            No Anthropic credential is configured, so agents can't run. {me?.admin || !me?.people
              ? <>Add one in <a href="#/settings/credentials">Settings › Credentials</a>.</> : `Ask ${adminNames(admins)} to add one.`}
          </p>
        )}
        <div class="form-foot">
          <span class="hint">{problem || (first ? '⌘/Ctrl+Enter to start. Nothing merges without your review.' : '⌘/Ctrl+Enter to start.')}</span>
          <button class="btn primary" disabled={busy || empty || !!problem}>{busy ? 'Starting…' : 'Start session'}</button>
        </div>
      </form>
    </div>
  );
}

/** Sends an old link on to where its page lives now. */
function Moved({ to }: { to: string }) {
  useEffect(() => { location.replace('#' + to); }, [to]);
  return null;
}

render(<Root />, document.getElementById('app')!);
