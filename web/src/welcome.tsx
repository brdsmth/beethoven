import type { ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import {
  api, githubRepos, reloadTeams, setMe, type AuthState, type Environment, type GitHubRepo, type GitHubStatus, type Info, type Team,
} from './api';
import { authorizeGitHub, returnFromGitHub } from './github';
import { STEPS } from './invite';
import { adminNames, useAdmins } from './people';
import { MAX_REPOS, TARGET_PREF } from './pickers';
import { setScope, useMe, useScope, useTeams } from './teams';
import { errText, fail, go, title, useLoad, writePref } from './util';

// Getting started, for someone who just joined: a short tour of how
// Beethoven works, then a checklist to their first session. It depends on
// the team they joined: with a project, they connect GitHub and start the
// session suggested to them; without one, they first give the team a
// project, from repositories on GitHub or a new one.

/** A first session, ready to start. */
export type Starter = { icon: string; label: string; hint: string; prompt: string };

const repoNames = (e?: Environment) => e?.repos.map(r => r.repo).join(' and ') || 'this project';

/** First sessions for a project that's in use. */
export const STARTERS: Starter[] = [
  { icon: '🧪', label: 'Fix a flaky test', hint: 'Find, fix, prove, open a PR',
    prompt: 'Find a test in this project that fails now and then. Work out why, fix it, run it enough times to show it\'s fixed, and open a pull request.' },
  { icon: '📦', label: 'Bump a dependency', hint: 'Upgrade and fix what breaks',
    prompt: 'Pick an outdated dependency that\'s safe to upgrade. Upgrade it, fix whatever breaks, run the tests, and open a pull request that explains what changed.' },
  { icon: '🔍', label: 'Explain the code', hint: 'No changes, just an answer',
    prompt: 'Give me a tour of this codebase: what it does, how it\'s laid out, and where to start reading. Don\'t change anything.' },
];

/** First sessions for a project nobody has prepared yet: no setup script. */
export const newProjectStarters = (e?: Environment): Starter[] => [
  { icon: '🧭', label: 'Get the project ready', hint: 'Learn the build, write AGENTS.md',
    prompt: `Get to know ${repoNames(e)}. For each repository, work out how to install its dependencies, build it and run its tests in this ` +
      'sandbox. Then open a pull request that adds an AGENTS.md describing the layout and those commands, and tell me what setup script ' +
      'this project should have. Don\'t change the app code.' },
  { icon: '🗺️', label: 'Explain the code', hint: 'A tour for you, no changes',
    prompt: `Give me a tour of ${repoNames(e)}: what it does, how it's laid out, and where to start reading. Don't change anything.` },
  { icon: '🐛', label: 'Take a small issue', hint: 'From the open issues',
    prompt: `Look at the open GitHub issues of ${repoNames(e)}, pick a small one you can fix with confidence, fix it with a test, and open a pull request that closes it.` },
];

/** Whether a project still needs preparing: nothing tells sandboxes how to set it up. */
export const unprepared = (e?: Environment) => !!e && !e.setup_script.trim() && !e.start_script.trim();

/** The starters that suit a project. */
export const startersFor = (e?: Environment) => (unprepared(e) ? newProjectStarters(e) : STARTERS);

/** Records progress in getting started. */
export const onboard = (v: { tour_seen?: boolean; started?: boolean; done?: boolean }) =>
  api<AuthState>('PUT', '/api/me/onboarding', v).then(setMe);

/** The team someone is getting started in, its projects and the one they'll start in. */
function useTeamProjects(envs?: Environment[]) {
  const me = useMe();
  const teams = useTeams();
  const id = me?.onboarding?.team_id || me?.default_team_id || '';
  const team = teams?.find(t => t.id === id && !t.archived_at);
  const projects = envs?.filter(e => (team ? e.team_id === team.id : !e.team_id));
  const project = projects?.find(e => e.id === me?.onboarding?.environment_id) || projects?.find(e => e.id === team?.default_environment_id)
    || projects?.[0];
  return { team, projects, project };
}

// ---------------------------------------------------------------- the tour

const TOUR = [
  { art: 'prompt', head: STEPS[0][1], text: 'Write it like a ticket for a colleague, and say what "done" looks like. Pick a project: the repositories it works on, with their setup. If your team has one, it\'s already picked.' },
  { art: 'log', head: STEPS[1][1], text: 'Each session runs in its own sandbox. Everything it does streams live and stays as a replay. Send more instructions any time: they wait their turn in the same conversation.' },
  { art: 'pr', head: STEPS[2][1], text: 'The agent opens a pull request as you, so your usual review and CI apply. Nothing merges by itself.' },
];

/** Three cards on how Beethoven works, shown once, on the first visit. */
export function Tour() {
  const [i, setI] = useState(0);
  const [open, setOpen] = useState(true);
  const close = () => { setOpen(false); onboard({ tour_seen: true }).catch(() => {}); };
  useEffect(() => {
    const esc = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    addEventListener('keydown', esc);
    return () => removeEventListener('keydown', esc);
  }, []);
  if (!open) return null;
  const card = TOUR[i];
  const last = i === TOUR.length - 1;
  return (
    <div class="modal-bg" role="dialog" aria-modal="true" aria-labelledby="tour-head">
      <div class="tour">
        <div class="tour-art" aria-hidden="true"><TourArt kind={card.art} /></div>
        <div class="tour-body">
          <h2 id="tour-head">{i + 1} · {card.head}</h2>
          <p>{card.text}</p>
        </div>
        <div class="tour-foot">
          <span class="pager" aria-label={`${i + 1} of ${TOUR.length}`}>{TOUR.map((_, n) => <i key={n} class={n === i ? 'on' : ''} />)}</span>
          {!last && <button type="button" class="btn quiet sm" onClick={close}>Skip</button>}
          {i > 0 && <button type="button" class="btn" onClick={() => setI(i - 1)}>Back</button>}
          <button type="button" class="btn primary" autoFocus onClick={() => (last ? close() : setI(i + 1))}>{last ? 'Get started' : 'Next'}</button>
        </div>
      </div>
    </div>
  );
}

function TourArt({ kind }: { kind: string }) {
  if (kind === 'prompt') {
    return (
      <div class="art-card">
        <span class="dim">What should the agent do?</span>
        <div>Bump stripe-go to v82 and fix whatever breaks</div>
        <div class="art-row"><span class="chip">Project: checkout</span><span class="chip">Seen by the team</span></div>
      </div>
    );
  }
  if (kind === 'log') {
    return (
      <div class="art-log">
        <div><span class="dot blue" /><b>Fix flaky checkout test</b><span class="chip blue push">running · 2m</span></div>
        <div><span class="tool">Read</span> checkout/checkout_test.go</div>
        <div><span class="tool">Bash</span> go test ./checkout/... -count=20</div>
        <div class="you">Also check the refund test while you're there<span class="chip push">queued</span></div>
      </div>
    );
  }
  return (
    <div class="art-card">
      <div class="art-pr"><span class="chip green">Open</span><b>Wait for the webhook in checkout tests</b></div>
      <div class="dim small">acme/payments#482 · by you</div>
      <div class="art-diff"><div class="d">- time.Sleep(2 * time.Second)</div><div class="a">+ waitFor(t, hook.Received)</div></div>
    </div>
  );
}

// -------------------------------------------------------------- get started

type Step = { key: string; title: string; done: boolean; desc?: ComponentChildren; body?: ComponentChildren; action?: ComponentChildren; waiting?: boolean };

export function GetStarted({ info }: { info?: Info }) {
  const me = useMe();
  const [gh] = useLoad<GitHubStatus>('/api/github');
  const [envs, reloadEnvs] = useLoad<Environment[]>('/api/environments');
  // Whom to ask, when something only an admin can set up is missing.
  const admins = useAdmins(!!me && !!me.people && !me.admin && !!gh && !!info && ((!gh.app && !info.github) || !info.anthropic));
  const { team, projects, project } = useTeamProjects(envs);
  const [sheet, setSheet] = useState(false);
  // The team had no project when they arrived: setting one up is a step, done or not.
  const empty = useRef<boolean>();
  const since = me?.onboarding?.since && Date.parse(me.onboarding.since) > 0 ? me.onboarding.since : me?.account?.created_at;
  if (projects && since && empty.current === undefined) empty.current = projects.every(e => Date.parse(e.created_at) >= Date.parse(since));
  const [, setScopeNow] = useScope();
  useEffect(() => { if (me?.onboarding?.tour && team) setScopeNow(team.id); }, [team?.id]); // their first visit: show their team's work
  title('Get started');
  if (!me || !envs || !gh || !info) return <div class="page narrow"><p class="empty">Loading…</p></div>;
  const o = me.onboarding;
  const where = team?.name || 'the workspace';
  const github = !!gh.account || (!gh.app && info.github);
  const askAdmins = `Ask ${adminNames(admins)}`;
  const first = () => { writePref(TARGET_PREF, project ? { kind: 'env', environment_id: project.id, repos: [] } : { kind: 'scratch', environment_id: '', repos: [] }); go('/?first=1'); };
  const scratch = () => { writePref(TARGET_PREF, { kind: 'scratch', environment_id: '', repos: [] }); go('/'); };

  const steps: Step[] = [{ key: 'account', title: 'Create your account', done: true }];
  if (team) {
    steps.push({ key: 'team', title: `Join ${team.name}`, done: true, desc: project
      ? <>{o?.invited_by_name ? `${o.invited_by_name} added you. ` : ''}New sessions start in {team.name} and use its <b>{project.name}</b> project.</>
      : <>{team.name} is new here: nobody has connected its code yet, so you'll set it up.</> });
  }
  steps.push({
    key: 'github', title: 'Connect GitHub', done: github,
    desc: gh.account ? <>Connected as <span class="mono">@{gh.account.login}</span>.</>
      : !gh.app && info.github ? 'The server works with GitHub through its own token.'
      : gh.app ? 'So pull requests open as you, and your commits are authored by you. One click on GitHub.'
      : me.admin ? 'Agents reach GitHub through a GitHub App of your own. Create it in a minute, from the GitHub settings.'
      : <>GitHub isn't set up on this server yet, so agents can't open pull requests. {askAdmins} to set it up; until then,
          you can <a href="#" onClick={e => { e.preventDefault(); scratch(); }}>try a scratch session</a>.</>,
    waiting: !github && !gh.app && !me.admin,
    action: github ? null : gh.app
      ? <button class="btn primary" onClick={() => { returnFromGitHub('/#/welcome'); authorizeGitHub().catch(fail); }}>Connect GitHub</button>
      : me.admin ? <a class="btn primary" href="#/settings/github">Set up GitHub</a> : null,
  });
  if (empty.current) {
    const ready = projects!.length > 0;
    steps.push({
      key: 'project', title: `Give ${where} a project`, done: ready,
      desc: ready ? <>Created <b>{project!.name}</b> with {repoNames(project)}. It's where {where}'s sessions start.</>
        : `A project is the code agents work on: one or more repositories, and how to set them up. Everyone in ${where} will use it.`,
      body: !ready && (
        <>
          <div class="choices">
            <div class="choice">
              <span class="choice-icon" aria-hidden="true">⎇</span><b>Use an existing repository</b>
              <span class="muted">The code is already on GitHub. Pick its repositories and you're done.</span>
              <span><button class="btn primary sm" disabled={!info.github} onClick={() => setSheet(true)}>Choose repositories</button></span>
            </div>
            <div class="choice">
              <span class="choice-icon" aria-hidden="true">+</span><b>Start a new repository</b>
              <span class="muted">Starting from nothing? Beethoven creates the repository, and an agent builds the first version.</span>
              <span><button class="btn sm" disabled={!gh.app} title={gh.app ? undefined : "Needs Beethoven's GitHub App"} onClick={() => { if (team) setScope(team.id); go('/projects/new?first=1'); }}>Start a new one</button></span>
            </div>
          </div>
          <p class="hint">{!info.github ? 'Connect GitHub first: projects work on its repositories. ' : ''}Not ready?{' '}
            <a href="#" onClick={e => { e.preventDefault(); scratch(); }}>Try a scratch session</a>: an empty sandbox with no repository, to see how it works.</p>
        </>
      ),
    });
  }
  const suggested = o?.prompt;
  steps.push({
    key: 'first', title: 'Start your first session', done: !!o?.started,
    desc: !project && empty.current ? `Once ${where} has a project, you'll get a first task suggested for it.`
      : suggested ? undefined : unprepared(project) ? `A good first one for a new project: have the agent learn how to build and test ${repoNames(project)}, and write it down for everyone.`
      : 'Try something small you\'d normally put off.',
    body: !o?.started && suggested && (
      <div class="from-inviter"><b>{o?.invited_by_name ? `${o.invited_by_name} suggested a first task` : 'A first task to try'}</b><span class="ellipsis">{suggested}</span></div>
    ),
    action: !o?.started && (project || !empty.current) ? <button class={'btn' + (github ? ' primary' : '')} onClick={first}>Open it</button> : null,
  });
  const done = steps.filter(s => s.done).length;
  const finish = () => onboard({ done: true }).then(() => go('/'), fail);

  return (
    <div class="page narrow getting-started">
      <div class="welcome-head">
        <div>
          <h1>{me.account?.name ? `Welcome, ${me.account.name.split(' ')[0]}` : 'Welcome'}</h1>
          <p class="muted">{done === steps.length ? "You're all set." : team && empty.current
            ? `${team.name} is new here, so you'll set it up first. A few steps to your first pull request.`
            : 'A few steps to your first pull request.'}</p>
        </div>
        {o && <button class="btn quiet sm push" onClick={finish}>{done === steps.length ? 'Finish' : 'Hide'}</button>}
      </div>
      <div class="checklist">
        <div class="progress" role="progressbar" aria-valuemin={0} aria-valuemax={steps.length} aria-valuenow={done}>
          <i style={{ width: `${(100 * done) / steps.length}%` }} />
        </div>
        {steps.map(s => (
          <div key={s.key} class={'check' + (s.done ? ' done' : '') + (s.waiting ? ' waiting' : '')}>
            <span class="tick" aria-label={s.done ? 'Done' : 'To do'}>{s.done ? '✓' : ''}</span>
            <div class="check-main">
              <div class="check-title">{s.title}</div>
              {s.desc && <div class="check-desc">{s.desc}</div>}
              {s.body}
            </div>
            {s.action}
          </div>
        ))}
      </div>
      {!info.anthropic && (
        <p class="banner warn">Agents can't run until a Claude credential is added. {me.admin
          ? <>Add one in <a href="#/settings/credentials">Settings › Credentials</a>.</> : `${askAdmins} to add one.`}</p>
      )}
      <div class="glossary">
        <div><b>Session</b>One task and its conversation with an agent, in its own sandbox. You can send follow-ups.</div>
        <div><b>Project</b>A saved set of repositories with their setup and secrets. Each team picks its default.</div>
        <div><b>Automation</b>A saved prompt that runs on a schedule, a GitHub event or a webhook.</div>
      </div>
      {sheet && <ProjectSheet team={team} envs={envs} gh={gh} close={() => setSheet(false)}
        made={e => { setSheet(false); reloadEnvs(); reloadTeams(); writePref(TARGET_PREF, { kind: 'env', environment_id: e.id, repos: [] }); }} />}
    </div>
  );
}

/** A project from repositories already on GitHub: just which ones, and a name. */
function ProjectSheet({ team, envs, gh, close, made }: {
  team?: Team; envs: Environment[]; gh: GitHubStatus; close: () => void; made: (e: Environment) => void;
}) {
  const [repos, setRepos] = useState<GitHubRepo[]>();
  const [q, setQ] = useState('');
  const [picked, setPicked] = useState<string[]>([]);
  const [name, setName] = useState('');
  const [named, setNamed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  useEffect(() => { githubRepos().then(setRepos); }, []);
  useEffect(() => {
    const esc = (e: KeyboardEvent) => { if (e.key === 'Escape') close(); };
    addEventListener('keydown', esc);
    return () => removeEventListener('keydown', esc);
  }, []);
  const usedBy = (repo: string) => envs.find(e => e.repos.some(r => r.repo === repo))?.name;
  const toggle = (repo: string) => {
    const next = picked.includes(repo) ? picked.filter(r => r !== repo) : [...picked, repo].slice(0, MAX_REPOS);
    setPicked(next);
    if (!named) setName(team?.slug || next[0]?.split('/')[1] || '');
  };
  const shown = (repos || []).filter(r => r.repo.toLowerCase().includes(q.trim().toLowerCase()));
  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setErr('');
    try {
      made(await api<Environment>('POST', '/api/environments', {
        team_id: team?.id || '', name: name.trim(), description: '', repos: picked.map(repo => ({ repo, base_branch: '' })),
        setup_script: '', start_script: '', overrides: {},
      }));
    } catch (e) { setErr(errText(e)); setBusy(false); }
  };
  return (
    <div class="sheet-bg" onPointerDown={e => { if (e.target === e.currentTarget) close(); }}>
      <form class="sheet" role="dialog" aria-modal="true" aria-labelledby="sheet-head" onSubmit={submit}>
        <div>
          <h2 id="sheet-head">Give {team?.name || 'the workspace'} a project</h2>
          <p class="muted">Pick the repositories your team works on. Agents get a copy of each in their sandbox.</p>
        </div>
        <input type="search" placeholder="Search repositories" autoFocus value={q} onInput={e => setQ(e.currentTarget.value)} aria-label="Search repositories" />
        <div class="repo-pick">
          {!repos && <p class="empty small">Loading…</p>}
          {repos && !shown.length && <p class="empty small">{repos.length ? 'Nothing matches.' : 'Beethoven can\'t reach any repositories yet.'}</p>}
          {shown.map(r => {
            const used = usedBy(r.repo);
            return (
              <label key={r.repo} class={'repo-pick-row' + (picked.includes(r.repo) ? ' on' : '')}>
                <input type="checkbox" checked={picked.includes(r.repo)} onChange={() => toggle(r.repo)} />
                <span class="mono ellipsis">{r.repo}</span>
                {used ? <span class="chip push">in {used}</span> : r.description ? <span class="dim ellipsis push">{r.description}</span> : null}
              </label>
            );
          })}
        </div>
        <p class="hint">{gh.app
          ? <>Don't see it? Beethoven's GitHub App can't reach it yet: someone who administers the app on GitHub adds it
              {gh.app.install_url ? <> in <a href={gh.app.install_url} target="_blank" rel="noopener">its settings ↗</a></> : ''}.</>
          : "Don't see it? The server's GitHub token can't reach it: ask whoever set the token up."}</p>
        <label class="field"><span class="field-label">Project name</span>
          <input required maxLength={100} value={name} onInput={e => { setName(e.currentTarget.value); setNamed(true); }} /></label>
        {team && !team.default_environment_id && <p class="hint">It becomes {team.name}'s default: new sessions in {team.name} start there.</p>}
        <p class="hint">Setup scripts and secrets can wait: your first session can work out how to build and test the code.</p>
        <div class="form-foot">
          {err ? <span class="err" role="alert">{err}</span> : <span />}
          <button type="button" class="btn" onClick={close}>Cancel</button>
          <button class="btn primary" disabled={busy || !picked.length || !name.trim()}>{busy ? 'Creating…' : 'Create project'}</button>
        </div>
      </form>
    </div>
  );
}
