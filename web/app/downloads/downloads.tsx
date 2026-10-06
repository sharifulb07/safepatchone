"use client";
import { useEffect, useRef, useState } from "react";

type Artifact = { id: string; version: string; channel: string; filename: string; sha256: string; bytes: number };
type Session = { organizations: { organization_id: string; name: string }[] };
type DownloadLink = { url: string; expires_at: string; filename: string; sha256: string };
const platforms = [{ key: "windows", name: "Windows", symbol: "⊞" }, { key: "darwin", name: "macOS", symbol: "⌘" }, { key: "linux", name: "Linux", symbol: "◇" }];
function csrf() { return document.cookie.split("; ").find(v => v.startsWith("__Host-safepatch_csrf="))?.split("=")[1] ?? ""; }
async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, { ...options, cache: "no-store" });
  if (!response.ok) throw new Error(response.status === 401 ? "Sign in to continue." : response.status === 403 ? "Your account does not have access to these downloads. Check your organization and permissions." : "Downloads are temporarily unavailable. Please try again.");
  return response.json();
}
export default function Downloads() {
  const [items, setItems] = useState<Artifact[]>([]), [privateItems, setPrivateItems] = useState<Artifact[]>([]);
  const [session, setSession] = useState<Session | null>(null), [org, setOrg] = useState("");
  const [arch, setArch] = useState("amd64"), [loading, setLoading] = useState(true), [orgLoading, setOrgLoading] = useState(false);
  const [error, setError] = useState(""), [accountError, setAccountError] = useState(""), [orgError, setOrgError] = useState(""), [linkError, setLinkError] = useState("");
  const [pending, setPending] = useState(""), [link, setLink] = useState<DownloadLink | null>(null);
  const generation = useRef(0);
  useEffect(() => {
    const controller = new AbortController();
    request<{ items: Artifact[] }>("/v1/downloads/public", { signal: controller.signal }).then(v => setItems(v.items)).catch(e => { if (!controller.signal.aborted) setError(e.message); }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    fetch("/v1/onboarding/session", { cache: "no-store", signal: controller.signal }).then(async r => { if (r.ok) setSession(await r.json()); else if (r.status !== 401) setAccountError("The account service is unavailable. Organization downloads cannot be checked yet."); }).catch(() => { if (!controller.signal.aborted) setAccountError("The account service is unavailable. Please try again."); });
    return () => controller.abort();
  }, []);
  useEffect(() => {
    if (!link) return;
    const timer = setTimeout(() => setLink(null), Math.max(0, Date.parse(link.expires_at) - Date.now()));
    return () => clearTimeout(timer);
  }, [link]);
  async function selectOrg(value: string) {
    const current = ++generation.current;
    setOrg(value); setPrivateItems([]); setLink(null); setPending(""); setOrgError(""); setOrgLoading(!!value);
    if (!value) return;
    try { const result = await request<{ items: Artifact[] }>(`/v1/organizations/${encodeURIComponent(value)}/downloads`); if (current === generation.current) setPrivateItems(result.items); }
    catch (e) { if (current === generation.current) setOrgError((e as Error).message); }
    finally { if (current === generation.current) setOrgLoading(false); }
  }
  async function issue(item: Artifact, privateDownload: boolean) {
    const current = generation.current;
    setPending(item.id); setLink(null); setLinkError("");
    try {
      const result = await request<DownloadLink>(privateDownload ? `/v1/organizations/${encodeURIComponent(org)}/downloads/${encodeURIComponent(item.id)}` : `/v1/downloads/public/${encodeURIComponent(item.id)}`, privateDownload ? { method: "POST", headers: { "X-CSRF-Token": csrf() } } : undefined);
      const url = new URL(result.url);
      if (url.protocol !== "https:" || url.username || url.password || !Number.isFinite(Date.parse(result.expires_at)) || Date.parse(result.expires_at) <= Date.now()) throw new Error("The download link is invalid or expired. Request a new link.");
      if (current === generation.current) setLink(result);
    } catch (e) { if (current === generation.current) setLinkError((e as Error).message); }
    finally { if (current === generation.current) setPending(""); }
  }
  function artifact(item: Artifact, privateDownload: boolean) {
    return <div className="download-item" key={item.id}><strong>{item.filename}</strong><p>v{item.version} · {item.channel} · {(item.bytes / 1048576).toFixed(1)} MB</p><button className="action" disabled={!!pending} onClick={() => issue(item, privateDownload)}>{pending === item.id ? "Preparing link…" : "Prepare download"}<span>↓</span></button><details><summary>SHA-256 checksum</summary><code>{item.sha256}</code></details></div>;
  }
  return <><div className="download-toolbar"><div><h2>Shared CLI releases</h2><p>Choose your operating system and processor.</p></div><label>Processor<select value={arch} onChange={e => setArch(e.target.value)}><option value="amd64">Intel / AMD (x64)</option><option value="arm64">ARM64 / Apple silicon</option></select></label></div>{loading && <p role="status">Loading published releases…</p>}{error && <p role="alert" className="service-alert">{error}</p>}<div className="platform-grid">{platforms.map(platform => { const matches = items.filter(a => a.filename.includes(`_${platform.key}_${arch}.`)); return <article className="platform-card" key={platform.key}><span className="platform-symbol" aria-hidden="true">{platform.symbol}</span><h2>{platform.name}</h2><p>{arch === "arm64" ? "ARM64" : "x64"}</p>{matches.map(item => artifact(item, false))}{!loading && !error && !matches.length && <p className="fine-print">No published release for this platform yet.</p>}</article>; })}</div><p className="fine-print">All editions share the same core binaries. Your organization’s active entitlements and your permissions determine access. Downloading a file does not activate a paid plan.</p>{items.filter(a => !platforms.some(p => ["amd64", "arm64"].some(cpu => a.filename.includes(`_${p.key}_${cpu}.`)))).length > 0 && <section><h2>Other public release assets</h2>{items.filter(a => !platforms.some(p => ["amd64", "arm64"].some(cpu => a.filename.includes(`_${p.key}_${cpu}.`)))).map(item => artifact(item, false))}</section>}<section className="organization-downloads"><div><p className="eyebrow">AVAILABLE TO YOUR ORGANIZATION</p><h2>Your plan’s downloads</h2><p>Self-hosted, air-gap, and restricted channel bundles appear here when your organization and role have access.</p><a className="text-link" href="/pricing">Compare features and plans →</a></div><div>{accountError && <p role="alert" className="service-alert">{accountError}</p>}{session ? <><label>Organization<select value={org} onChange={e => selectOrg(e.target.value)}><option value="">Choose an organization</option>{session.organizations.map(o => <option key={o.organization_id} value={o.organization_id}>{o.name}</option>)}</select></label>{!session.organizations.length && <a className="action" href="/onboarding">Set up your organization →</a>}{orgLoading && <p role="status">Checking current access…</p>}{orgError && <p className="service-alert" role="alert">{orgError}</p>}{privateItems.map(item => artifact(item, true))}{org && !orgLoading && !orgError && !privateItems.length && <p>No additional bundles are available for this organization and role.</p>}</> : <a className="action primary" href="/signin?return_to=%2Fdownloads">Sign in to see your downloads <span>↗</span></a>}</div></section>{linkError && <p role="alert" className="service-alert">{linkError}</p>}{link && <section className="ready-download" role="status"><h2>Your download is ready</h2><p>{link.filename}</p><a className="action primary" href={link.url} rel="noreferrer">Download now <span>↓</span></a><p className="fine-print">This link expires shortly. Request a new one if it expires before your download starts.</p><code>{link.sha256}</code></section>}<section className="install-help"><h2>Check your download</h2><p>Compare the file’s SHA-256 checksum with the value shown above before extracting it.</p><details><summary>Windows (PowerShell)</summary><pre>Get-FileHash .\safepatch_VERSION_windows_amd64.zip -Algorithm SHA256</pre><p>Extract the ZIP, then run <code>.\safepatch.exe version</code> from the extracted directory.</p></details><details><summary>macOS</summary><pre>shasum -a 256 safepatch_VERSION_darwin_arm64.zip</pre><p>Extract the ZIP, then run <code>./safepatch version</code>. Use the amd64 archive for Intel Macs.</p></details><details><summary>Linux</summary><pre>sha256sum safepatch_VERSION_linux_amd64.zip</pre><p>Extract the ZIP, then run <code>./safepatch version</code>.</p></details></section></>;
}
