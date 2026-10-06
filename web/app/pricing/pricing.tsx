"use client";

import { useRef, useState } from "react";
import type { CatalogEdition, CatalogFeature, ProductCatalog } from "../../lib/catalog";
import { SiteHeader } from "../components/site";

function Check({ className = "" }: { className?: string }) {
  return <svg className={className} aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none"><path d="m5 12 4 4L19 6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg>;
}
function Arrow() { return <span aria-hidden="true">↗</span>; }

export default function Pricing({ catalog }: { catalog: ProductCatalog }) {
  const [query, setQuery] = useState("");
  const [includePlanned, setIncludePlanned] = useState(true);
  const [selected, setSelected] = useState<CatalogEdition | null>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  const opener = useRef<HTMLElement | null>(null);
  const all = new Map<string, CatalogFeature>();
  for (const edition of catalog.editions) for (const feature of edition.features) all.set(feature.key, feature);
  const features = [...all.values()].sort((a, b) => a.category.localeCompare(b.category) || a.name.localeCompare(b.name));
  const shown = features.filter(f => (includePlanned || f.availability === "SDK") && `${f.name} ${f.description} ${f.key} ${f.category}`.toLowerCase().includes(query.trim().toLowerCase()));

  function highlights(edition: CatalogEdition, index: number) {
    const previous = new Set(catalog.editions[index - 1]?.features.map(f => f.key) ?? []);
    return [...edition.features].sort((a, b) => Number(previous.has(a.key)) - Number(previous.has(b.key)) || Number(a.availability !== "SDK") - Number(b.availability !== "SDK")).slice(0, 6);
  }

  function action(edition: CatalogEdition) {
    const label = `${edition.action.label} — ${edition.name}`;
    return edition.action.href
      ? <a className="action" href={edition.action.href} aria-label={label}>{edition.action.label}<Arrow /></a>
      : <button className="action" aria-label={label} onClick={event => {
        opener.current = event.currentTarget; setSelected(edition); dialog.current?.showModal();
      }}>{edition.action.label}<Arrow /></button>;
  }

  return <>
    <SiteHeader />

    <main id="main">
      <section className="hero wrap" aria-labelledby="pricing-title">
        <p className="eyebrow"><span className="status-dot" /> ONE PLATFORM. {catalog.editions.length} PERMANENT EDITIONS.</p>
        <h1 id="pricing-title">Confidence for every change.<br /><em>A plan for every team.</em></h1>
        <p className="hero-copy">From your first local check to organization-wide assurance.<br className="desktop-break" /> Choose the capabilities that fit the way you build.</p>
        <a className="text-link" href="#compare">Explore the full feature comparison <span aria-hidden="true">↓</span></a>
      </section>

      <section id="editions" className="wrap editions" aria-label="SafePatch editions">
        <div className="section-label"><span>CHOOSE YOUR STARTING POINT</span><span>One shared verification foundation <Check /></span></div>
        <div className="plans">
          {catalog.editions.map((edition, index) => <article className="plan" key={edition.key} aria-labelledby={`plan-${edition.key}`}>
            <div className="plan-top"><span className="plan-number">0{index + 1}</span><span className="plan-motif" aria-hidden="true">{Array.from({ length: index + 1 }, (_, i) => <i key={i} />)}</span></div>
            <h2 id={`plan-${edition.key}`}>{edition.name}</h2>
            <p className="plan-description">{edition.description}</p>
            <div className="price-block"><p className="price">{edition.price_label}</p><p className="price-note">{edition.price_note}</p></div>
            {action(edition)}
            <p className="trial-note">{edition.trial_days ? `${edition.trial_days}-day organization trial` : edition.trial_note}</p>
            <div className="plan-features"><p className="list-label">EDITION HIGHLIGHTS</p>
              <ul>{highlights(edition, index).map(feature => <li key={feature.key}><Check /><span>{feature.name}{feature.availability !== "SDK" && <small className="planned">Planned</small>}</span></li>)}</ul>
              <a className="feature-count" href="#compare">Explore {edition.features.length} catalog features <span aria-hidden="true">→</span></a>
            </div>
          </article>)}
        </div>
        <p className="catalog-note"><span className="info-mark" aria-hidden="true">i</span> Edition features reflect the product catalog. SDK building blocks and planned capabilities are identified in the comparison below.</p>
      </section>

      <section className="principles wrap" aria-label="Shared foundations">
        <div><span className="principle-icon" aria-hidden="true">◇</span><p><strong>One verification foundation</strong><span>The same core platform in every edition.</span></p></div>
        <div><span className="principle-icon" aria-hidden="true">↗</span><p><strong>Room to grow</strong><span>Change capabilities as your needs evolve.</span></p></div>
        <div><span className="principle-icon" aria-hidden="true">◎</span><p><strong>Your organization, connected</strong><span>Subscriptions and trials belong to your organization.</span></p></div>
      </section>

      <section id="compare" className="comparison wrap" aria-labelledby="compare-title">
        <div className="section-heading"><div><p className="eyebrow">THE DETAILS, IN ONE PLACE</p><h2 id="compare-title">Find your fit.</h2></div><p>Compare the capabilities in each edition.<br />SDK = developer building blocks. Planned = not yet available.</p></div>
        <div className="filter-bar"><label className="search"><span aria-hidden="true">⌕</span><span className="sr-only">Search features</span><input type="search" value={query} onChange={e => setQuery(e.target.value)} placeholder="Search features, e.g. proof or policy" maxLength={128} /></label><label className="toggle"><input type="checkbox" checked={includePlanned} onChange={e => setIncludePlanned(e.target.checked)} />Include planned features</label></div>
        <p className="result-count" aria-live="polite">{shown.length} of {features.length} catalog features</p>
        <div className="table-scroll" tabIndex={0} role="region" aria-label="Feature comparison; scroll horizontally on smaller screens">
          <table><caption className="sr-only">SafePatch edition feature comparison</caption><thead><tr><th scope="col">Capabilities</th>{catalog.editions.map(e => <th scope="col" key={e.key}>{e.name}</th>)}</tr></thead>
            <tbody>{shown.map(f => <tr key={f.key}><th scope="row"><span className="feature-name">{f.name}</span><span className="feature-description">{f.description}</span></th>{catalog.editions.map(e => { const included = e.features.find(v => v.key === f.key);return <td key={e.key}>{included ? <span className={`availability ${included.availability === "SDK" ? "sdk" : "future"}`}>{included.availability === "SDK" && <Check />}{included.availability === "SDK" ? "SDK" : "Planned"}{included.limit_value !== null && <small>Limit: {included.limit_value}</small>}</span> : <span className="not-included"><span aria-hidden="true">—</span><span className="sr-only">Not included</span></span>}</td>;})}</tr>)}</tbody>
          </table>
          {shown.length === 0 && <div className="empty"><strong>No matching features</strong><p>Try a different search or include planned features.</p><button className="text-link" onClick={() => {setQuery("");setIncludePlanned(true);}}>Reset filters</button></div>}
        </div>
      </section>

      <section id="questions" className="faq wrap" aria-labelledby="faq-title"><div><p className="eyebrow">A LITTLE MORE CLARITY</p><h2 id="faq-title">Good questions.<br /><em>Straight answers.</em></h2></div><div className="faq-items">
        <details><summary>What happens when an organization trial ends?</summary><p>Trial grants expire automatically. Your organization retains its applicable Community capabilities and any valid paid sources. Existing project and repository data is preserved.</p></details>
        <details><summary>Are all listed features available today?</summary><p>No. The comparison distinguishes developer SDK building blocks from planned capabilities. A catalog entry describes an edition’s capabilities; it does not promise a deployed service or a successful verification.</p></details>
        <details><summary>Does selecting a plan give every member access?</summary><p>No. Your organization’s entitlements and each member’s permissions are checked separately. Trial activation requires an authenticated organization owner with billing management permission.</p></details>
        <details><summary>Where can I find published prices?</summary><p>Paid self-service prices have not been published in the product configuration yet. Enterprise pricing is managed by contract. This page will reflect public pricing when it is configured.</p></details>
      </div></section>
    </main>
    <footer className="footer wrap"><a className="brand" href="/pricing"><span className="brand-mark"><Check /></span>SafePatch<span className="brand-dot">.</span></a><p>Evidence before confidence.</p><a href="/v1/product-catalog">View product catalog <Arrow /></a></footer>

    <dialog ref={dialog} aria-labelledby="action-title" onClose={() => opener.current?.focus()} onClick={e => {if (e.target === e.currentTarget) dialog.current?.close();}}><div className="dialog-content"><button className="dialog-close" aria-label="Close dialog" onClick={() => dialog.current?.close()}>×</button><p className="eyebrow">SAFEPATCH</p><h2 id="action-title">{selected?.action.label}{selected ? ` · ${selected.name}` : ""}</h2><p>{selected?.action.unavailable_message}</p><button className="action" onClick={() => dialog.current?.close()}>Back to editions <span aria-hidden="true">→</span></button></div></dialog>
  </>;
}
