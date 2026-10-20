import type { ComponentChildren } from 'preact';
import { useEffect, useState } from 'preact/hooks';
import { api, get, type InviteInfo, type Role } from './api';
import { TeamMark } from './teams';
import { errText, title } from './util';

// The page an invitation link opens. It says what Beethoven is and what
// joining means (the team, the role, a note from whoever sent it), then
// creates the account, or adds someone already signed in to the team.
// A used or expired link names whom to ask for another.

const article = (r: Role) => (r === 'member' ? 'a member' : r === 'admin' ? 'an admin' : 'an owner');
const day = (iso: string) => new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
/** Where someone goes once they've joined. */
const welcome = () => { location.replace('/#/welcome'); location.reload(); };

export function InviteAccept({ token }: { token: string }) {
  const [info, setInfo] = useState<InviteInfo>();
  const [missing, setMissing] = useState('');
  const load = () => get<InviteInfo>(`/api/auth/invite?token=${encodeURIComponent(token)}`).then(setInfo, e => setMissing(errText(e)));
  useEffect(() => { load(); }, [token]);
  title('Join');
  if (missing) {
    return (
      <Card>
        <div class="auth-head"><h1>This invitation can't be used</h1><p class="muted">{missing[0].toUpperCase() + missing.slice(1)}.</p></div>
        <p class="hint">Ask whoever sent it for a new link. <a href="/#/">Sign in</a> if you're already here.</p>
      </Card>
    );
  }
  if (!info) return <Card><p class="muted">One moment…</p></Card>;
  if (info.state !== 'open') return <Closed info={info} />;
  if (info.signed_in) return <Join token={token} info={info} />;
  return <SignUp token={token} info={info} signedIn={load} />;
}

const Card = ({ children }: { children: ComponentChildren }) => (
  <div class="auth"><div class="auth-card"><div class="brand">Beethoven</div>{children}</div></div>
);

/** A link that was used, or ran out. */
function Closed({ info }: { info: InviteInfo }) {
  const from = info.from || 'whoever sent it';
  return (
    <Card>
      <div class="auth-head">
        <h1>{info.state === 'used' ? 'This invitation was already used' : 'This invitation has expired'}</h1>
        <p class="muted">An invitation works once, for 7 days. {info.from ? `${info.from} sent this one on ${day(info.created_at)}.` : ''}</p>
      </div>
      <p class="muted" style={{ margin: 0 }}>Ask {from} for a new link.</p>
      <p class="hint" style={{ margin: 0 }}>Already joined? <a href="/#/">Sign in</a></p>
    </Card>
  );
}

/** Someone with an account: the invitation adds them to its team. */
function Join({ token, info }: { token: string; info: InviteInfo }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const who = info.signed_in!;
  const join = async () => {
    setBusy(true);
    setErr('');
    try {
      await api('POST', '/api/auth/invite/join', { token });
      welcome();
    } catch (e) { setErr(errText(e) + '.'); setBusy(false); }
  };
  const signOut = () => api('POST', '/api/auth/logout').then(() => location.reload(), e => setErr(errText(e)));
  return (
    <Card>
      <From info={info} />
      {info.team ? (
        <>
          <p class="muted" style={{ margin: 0 }}>You're signed in as {who.email}. Accepting adds you to <b>{info.team.name}</b> as a member; your
            role here stays as it is.</p>
          {err && <p class="auth-error" role="alert">{err}</p>}
          <button class="btn primary block" disabled={busy} onClick={join}>{busy ? 'Joining…' : `Join ${info.team.name}`}</button>
        </>
      ) : (
        <>
          <p class="muted" style={{ margin: 0 }}>You're signed in as {who.email}, so you're here already. This invitation is for someone new.</p>
          <a class="btn primary block" href="/#/">Go to Beethoven</a>
        </>
      )}
      <button type="button" class="btn block quiet" onClick={signOut}>Not you? Sign out</button>
    </Card>
  );
}

/** Who sent it and their note. */
function From({ info }: { info: InviteInfo }) {
  return (
    <>
      <div class="invite-from">
        <span class="avatar" aria-hidden="true">{(info.from || '?').slice(0, 1).toUpperCase()}</span>
        <span class="invite-from-who">
          <b>{info.from ? `${info.from} invited you` : "You're invited"}</b>
          <span class="muted">{info.team ? `to ${info.team.name}, on ${location.host}` : `to Beethoven on ${location.host}`}</span>
        </span>
      </div>
      {info.note && <blockquote class="invite-note">{info.note}</blockquote>}
    </>
  );
}

const CAN: Record<Role, string[]> = {
  member: ['Start sessions, and use your teams\' projects and automations', "See and follow your team's sessions"],
  admin: ['Everything members can', 'Manage people, teams, settings and integrations'],
  owner: ['Everything admins can', 'Make and unmake owners'],
};

function SignUp({ token, info, signedIn }: { token: string; info: InviteInfo; signedIn: () => void }) {
  const [f, setF] = useState({ name: '', email: info.email || '', password: '' });
  const [login, setLogin] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const field = (k: keyof typeof f) => (e: Event) => {
    const v = (e.currentTarget as HTMLInputElement).value;
    setF(p => ({ ...p, [k]: v }));
  };
  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setErr('');
    try {
      if (login) {
        await api('POST', '/api/auth/login', { email: f.email, password: f.password });
        setBusy(false);
        return signedIn(); // now the invitation can add them to its team
      }
      await api('POST', '/api/auth/invite', { token, ...f });
      welcome();
    } catch (e) { setErr(errText(e) + '.'); setBusy(false); }
  };
  const team = info.team;
  const role = info.role || 'member';
  return (
    <div class="invite">
      <Story />
      <form class="invite-form" onSubmit={submit}>
        <From info={info} />
        {team && (
          <div class="invite-team">
            <TeamMark team={{ id: team.name, name: team.name, color: team.color }} size={28} />
            <span class="invite-team-text">
              <b>You'll join {team.name}</b>
              <span class="muted">{team.members === 1 ? '1 person' : `${team.members} people`} · {team.projects.length
                ? team.projects.slice(0, 3).join(', ') + (team.projects.length > 3 ? ` and ${team.projects.length - 3} more` : '')
                : "no projects yet: you'll help set it up"}</span>
            </span>
          </div>
        )}
        {login ? <p class="muted" style={{ margin: 0 }}>Sign in, and the invitation adds you to {team?.name}. Your role here stays as it is.</p> : (
          <div class="invite-role">
            <b>As {article(role)}</b> you can:
            <ul>{CAN[role].map(c => <li key={c}>{c}</li>)}</ul>
            {role === 'member' && <span class="dim">Admins handle people, credentials and integrations.</span>}
          </div>
        )}
        {!login && (
          <label class="field"><span class="field-label">Name</span>
            <input autoFocus autoComplete="name" value={f.name} onInput={field('name')} /></label>
        )}
        <label class="field"><span class="field-label">Email</span>
          <input type="email" required readOnly={!!info.email && !login} autoComplete="username" value={f.email} onInput={field('email')} /></label>
        <label class="field"><span class="field-label">Password</span>
          <input type="password" required minLength={login ? undefined : 8} autoComplete={login ? 'current-password' : 'new-password'}
            value={f.password} onInput={field('password')} />
          {!login && <span class="hint">At least 8 characters.</span>}</label>
        {err && <p class="auth-error" role="alert">{err}</p>}
        <button class="btn primary block" disabled={busy}>
          {busy ? (login ? 'Signing in…' : 'Creating…') : login ? 'Sign in' : team ? `Join ${team.name}` : 'Create account'}
        </button>
        <p class="hint invite-foot">
          {login ? <>New here? <a href="#" onClick={e => { e.preventDefault(); setLogin(false); }}>Create an account</a></>
            : team ? <>Already have an account? <a href="#" onClick={e => { e.preventDefault(); setLogin(true); }}>Sign in to accept</a></> : null}
          {' '}· Expires {day(info.expires_at)}
        </p>
      </form>
    </div>
  );
}

/** What Beethoven is, in three steps and a session's replay. */
function Story() {
  return (
    <div class="invite-story">
      <span class="brand">Beethoven</span>
      <div>
        <h1>Hand a coding task to an agent, get a pull request back.</h1>
        <p class="invite-lede">Beethoven runs coding agents on your team's own servers. You describe the work; an agent does it in its own
          sandbox while you get on with yours.</p>
      </div>
      <ol class="invite-how">
        {STEPS.map(([icon, head, text]) => <li key={head}><span class="n" aria-hidden="true">{icon}</span><div><b>{head}</b><span>{text}</span></div></li>)}
      </ol>
      <div class="invite-replay" aria-hidden="true">
        <div class="p">› Fix the flaky test in checkout_test.go</div>
        <div><span class="tool">Read</span> checkout/checkout_test.go</div>
        <div><span class="tool">Edit</span> wait for the payment webhook instead of sleeping</div>
        <div><span class="tool">Bash</span> go test ./checkout/... -count=20 <span class="ok">✓ ok</span></div>
        <div class="ok">⇄ Opened a pull request</div>
      </div>
    </div>
  );
}

/** How Beethoven works, shared with the welcome tour. */
export const STEPS: [string, string, string][] = [
  ['✎', 'Describe the task', 'From here, Slack or the CLI: "Fix the flaky checkout test and open a PR."'],
  ['▶', "Watch it work, or don't", 'Each session runs in its own container. Follow it live, or send more instructions while it runs.'],
  ['⇄', 'Review the pull request', 'When it\'s done, the agent opens a pull request on GitHub, authored by you.'],
];
