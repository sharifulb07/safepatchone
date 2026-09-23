"use client";

import { useEffect, useState } from "react";
import type { ProductCatalog } from "../../lib/catalog";
import "./onboarding.css";

type Repo = { id: string; full_name: string; private: boolean };
type State = { organization_id: string; revision: number; edition: string; edition_version_id: string; installation_id: number; repositories: Repo[]; policy_id: string; proof_id: string };
type View = { state: State; access: { ready: boolean; source: string }; policies: { id: string; name: string; description: string }[]; proof?: { id: string; status: string } };
type Session = { user_id: string; organizations: { organization_id: string; name: string }[]; github_install_url: string };
const steps = ["Your account", "Organization", "Edition", "Checkout or trial", "Connect GitHub", "Repositories", "Policy", "First proof"];
const messages: Record<string, string> = {
  AUTHENTICATION_REQUIRED: "Please sign in again to continue.",
  ONBOARDING_UNAVAILABLE: "This step is temporarily unavailable. Your saved progress is safe. Please try again.",
  PREREQUISITE_NOT_READY: "This step is waiting for server confirmation. Check your status before continuing.",
  FEATURE_NOT_ENTITLED: "Your organization does not currently own the capability required for this action.",
  PERMISSION_DENIED: "You do not have permission to perform this action in this organization.",
  ONBOARDING_CONFLICT: "Your setup changed in another request. Refresh your progress and try again.",
  INVALID_REQUEST: "The request could not be accepted. Check your selection and try again.",
};
export default function Onboarding({ catalog }: { catalog: ProductCatalog }) {
  const [session, setSession] = useState<Session | null>(null);
  const [view, setView] = useState<View | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [edition, setEdition] = useState("");
  const [installations, setInstallations] = useState<{ id: number; name: string }[]>([]);
  const [installation, setInstallation] = useState("");
  const [repos, setRepos] = useState<Repo[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [connected, setConnected] = useState(false);
  const [attempt, setAttempt] = useState("");
  const [policy, setPolicy] = useState("");
  const [editingEdition, setEditingEdition] = useState(false);
  const state = view?.state;
  const step = !session ? 0 : !state ? 1 : !state.edition || editingEdition ? 2 : !view?.access.ready ? 3 : state.proof_id ? 7 : state.policy_id ? 7 : state.repositories.length ? 6 : connected ? 5 : 4;

  async function api<T>(path: string, body?: unknown): Promise<T> {
    const csrf = document.cookie.split("; ").find(v => v.startsWith("__Host-safepatch_csrf="))?.split("=")[1] ?? "";
    const response = await fetch(`/v1/onboarding/${path}`, { method: body === undefined ? "GET" : "POST", credentials: "same-origin", cache: "no-store", headers: body === undefined ? {} : { "Content-Type": "application/json", "X-CSRF-Token": csrf }, body: body === undefined ? undefined : JSON.stringify(body) });
    const result = await response.json();
    if (!response.ok) { if (response.status === 401) { setSession(null); setView(null); } throw new Error(result.error ?? "ONBOARDING_UNAVAILABLE"); }
    return result;
  }
  async function work(action: () => Promise<void>) { setBusy(true); setError(""); try { await action(); } catch (e) { setError(messages[e instanceof Error ? e.message : ""] ?? messages.ONBOARDING_UNAVAILABLE); } finally { setBusy(false); } }
  async function load(org: string) { const next = await api<View>(org); if (view?.state.organization_id !== org) { setConnected(false); setInstallation(""); setInstallations([]); setRepos([]); } setView(next); setEditingEdition(false); setEdition(next.state.edition || edition); setPolicy(next.state.policy_id); setSelected(next.state.repositories.map(r => r.id)); const url = new URL(location.href); url.searchParams.set("organization", org); history.replaceState(null, "", url); }
  useEffect(() => {
    setAttempt(crypto.randomUUID());
    const params = new URLSearchParams(location.search);
    const requested = params.get("edition") ?? "";
    if (catalog.editions.some(e => e.key === requested)) setEdition(requested);
    // Query parameters preselect intent only. They never mark billing complete.
    let active = true;
    (async () => { try { const current = await api<Session>("session"); if (!active) return; setSession(current); const org = params.get("organization"); if (org && current.organizations.some(o => o.organization_id === org)) await load(org); } catch (e) { if (active && (e as Error).message !== "AUTHENTICATION_REQUIRED") setError(messages.ONBOARDING_UNAVAILABLE); } finally { if (active) setBusy(false); } })();
    return () => { active = false; };
    // The initial server session is loaded once; mutations explicitly reload it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return <div className="onboarding-shell">
    <header className="header"><div className="nav wrap"><a className="brand" href="/pricing">SafePatch<span className="brand-dot">.</span></a><span className="onboarding-header-label">Your first verified change</span>{session && <button className="text-link" disabled={busy} onClick={() => work(async () => { await api("logout", {}); setSession(null); setView(null); })}>Sign out</button>}</div></header>
    <main className="onboarding-main wrap">
      <aside><p className="eyebrow">A CLEAR PATH TO CONFIDENCE</p><h1>Let’s get<br /><em>you started.</em></h1><p>Connect your organization, choose your boundaries, and start gathering proof.</p><ol aria-label="Onboarding progress">{steps.map((label, index) => <li key={label} aria-current={step === index ? "step" : undefined} className={index === step ? "current" : index < step ? "done" : ""}><span>{index < step ? "✓" : index + 1}</span>{label}</li>)}</ol></aside>
      <section className="onboarding-panel" aria-labelledby="step-title" aria-busy={busy}>
        <div className="onboarding-panel-top"><span>STEP {step + 1} OF {steps.length}</span>{state && <button className="text-link" disabled={busy} onClick={() => work(() => load(state.organization_id))}>Refresh progress ↻</button>}</div>
        <h2 id="step-title">{steps[step]}</h2>
        {error && <div role="alert" className="onboarding-alert">{error}</div>}
        {state && <div className="onboarding-actions">
          {!state.proof_id && state.edition && step !== 2 && <button className="text-link" disabled={busy} onClick={() => setEditingEdition(true)}>Change selected edition</button>}
          <button className="text-link" disabled={busy} onClick={() => work(async () => { setSession(await api<Session>("session")); setView(null); setEditingEdition(false); setAttempt(crypto.randomUUID()); const url = new URL(location.href); url.searchParams.delete("organization"); history.replaceState(null, "", url); })}>Switch organization</button>
        </div>}
        {busy && <p role="status" className="onboarding-muted">Checking with SafePatch…</p>}

        {step === 0 && <><p>Use your GitHub account to sign up or sign in. Repository access is selected separately after your organization is ready.</p><a className="action" href="/v1/auth/github/start">Sign up with GitHub <span>↗</span></a><p className="onboarding-muted">Already have an account? <a href="/v1/auth/github/start">Log in with GitHub</a></p></>}
        {step === 1 && <><p>Your organization owns its subscription, repositories, and proof history.</p>{session?.organizations.length ? <label>Continue an existing organization<select defaultValue="" disabled={busy} onChange={e => { if (e.target.value) work(() => load(e.target.value)); }}><option value="">Select organization</option>{session.organizations.map(o => <option key={o.organization_id} value={o.organization_id}>{o.name}</option>)}</select></label> : null}<form onSubmit={e => { e.preventDefault(); work(async () => { const next = await api<State>("organizations", { attempt, name }); await load(next.organization_id); }); }}><label>New organization name<input value={name} onChange={e => setName(e.target.value)} maxLength={256} required autoComplete="organization" placeholder="e.g. Acme Engineering" /></label><button className="action" disabled={busy || !name.trim()}>Create organization <span>→</span></button></form></>}
        {step === 2 && <><p>Select an edition for this organization. Selecting a plan does not activate paid access.</p><div className="onboarding-options">{catalog.editions.map(e => <label key={e.key} className={edition === e.key ? "selected" : ""}><input type="radio" name="edition" value={e.key} checked={edition === e.key} onChange={() => setEdition(e.key)} /><span><strong>{e.name}</strong><small>{e.description}</small><small>{e.trial_days ? `${e.trial_days}-day trial` : e.price_label}</small></span></label>)}</div><button className="action" disabled={busy || !edition} onClick={() => work(async () => { await api(`${state!.organization_id}/edition`, { revision: state!.revision, edition }); await load(state!.organization_id); })}>Continue <span>→</span></button></>}
        {step === 3 && <><p>Your edition is selected. SafePatch is waiting for authoritative subscription or trial confirmation before enabling its capabilities.</p><div className="onboarding-actions"><button className="action" disabled={busy} onClick={() => work(async () => { await api(`${state!.organization_id}/billing`, { revision: state!.revision, mode: "trial" }); await load(state!.organization_id); })}>Start organization trial <span>→</span></button><button className="action" disabled={busy} onClick={() => work(async () => { const result = await api<{ redirect_url: string }>(`${state!.organization_id}/billing`, { revision: state!.revision, mode: "checkout" }); if (result.redirect_url) location.assign(result.redirect_url); else await load(state!.organization_id); })}>Continue to checkout <span>↗</span></button><button className="secondary" disabled={busy} onClick={() => work(() => load(state!.organization_id))}>Check payment or contract status</button></div><p className="onboarding-muted">Returning from checkout is not payment confirmation. Contract-managed or unavailable offers remain pending until confirmed by the server.</p></>}
        {step === 4 && <><p>Install the SafePatch GitHub App and grant it access to the repositories you want to verify.</p><a className="action" href={session?.github_install_url} target="_blank" rel="noopener noreferrer">Connect GitHub <span>↗</span></a><button className="secondary" disabled={busy} onClick={() => work(async () => { const found = await api<{ id: number; name: string }[]>("installations"); setInstallations(found); if (!found.length) throw new Error("PREREQUISITE_NOT_READY"); setConnected(true); })}>I’ve installed the app — continue</button></>}
        {step === 5 && <><p>Only repositories currently accessible through your GitHub installation and organization capabilities are shown.</p><label>GitHub installation<select value={installation} disabled={busy} onChange={e => { const id = e.target.value; setInstallation(id); setSelected([]); setRepos([]); if (id) work(async () => setRepos(await api<Repo[]>(`${state!.organization_id}/repositories?installation_id=${encodeURIComponent(id)}`))); }}><option value="">Choose an installation</option>{installations.map(i => <option key={i.id} value={i.id}>{i.name}</option>)}</select></label><div className="repo-picker">{repos.map(r => <label key={r.id}><input type="checkbox" checked={selected.includes(r.id)} onChange={e => setSelected(e.target.checked ? [...selected, r.id] : selected.filter(id => id !== r.id))} /><span>{r.full_name}<small>{r.private ? "Private" : "Public"}</small></span></label>)}{installation && !repos.length && !busy && <p>No eligible repositories. Check GitHub access and your organization’s capabilities.</p>}</div><button className="action" disabled={busy || !selected.length || selected.length > 50} onClick={() => work(async () => { await api(`${state!.organization_id}/repositories`, { revision: state!.revision, installation_id: Number(installation), repository_ids: selected }); await load(state!.organization_id); })}>Use {selected.length} repositories <span>→</span></button></>}
        {step === 6 && <><p>Choose the policy your first proof must satisfy. Missing or incomplete evidence can never satisfy a required check.</p><div className="onboarding-options">{view!.policies.map(p => <label key={p.id} className={policy === p.id ? "selected" : ""}><input type="radio" name="policy" checked={policy === p.id} onChange={() => setPolicy(p.id)} /><span><strong>{p.name}</strong><small>{p.description}</small></span></label>)}</div>{!view!.policies.length && <p>No eligible policies are available for this organization.</p>}<button className="action" disabled={busy || !policy} onClick={() => work(async () => { await api(`${state!.organization_id}/policy`, { revision: state!.revision, policy_id: policy }); await load(state!.organization_id); })}>Save policy <span>→</span></button></>}
        {step === 7 && <><p>{state!.repositories.length} repositories selected. Policy: {view!.policies.find(p => p.id === state!.policy_id)?.name ?? state!.policy_id}.</p>{view?.proof ? <div className="proof-status" role="status"><span className="eyebrow">SERVER-REPORTED PROOF STATUS</span><strong>{view.proof.status}</strong><p>{["PENDING", "QUEUED", "RUNNING"].includes(view.proof.status) ? "Your proof has not completed. Refresh to check worker progress." : view.proof.status === "PASS" ? "The configured checks passed. This is not authorization to deploy to production." : "This result does not establish a passing proof. Review the worker evidence before proceeding."}</p><code>{view.proof.id}</code><button className="secondary" disabled={busy} onClick={() => work(() => load(state!.organization_id))}>Check proof status</button></div> : <button className="action" disabled={busy} onClick={() => work(async () => { await api(`${state!.organization_id}/proof`, { revision: state!.revision }); await load(state!.organization_id); })}>Run first proof <span>→</span></button>}<p className="onboarding-muted">The worker rechecks current access and resource limits before verification. A queued job is not a completed proof.</p></>}
      </section>
    </main>
  </div>;
}
