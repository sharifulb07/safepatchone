export function SiteHeader() {
  return <><a className="skip" href="#main">Skip to content</a><header className="header"><div className="nav wrap"><a href="/" className="brand" aria-label="SafePatch home"><span className="brand-mark" aria-hidden="true">✓</span>SafePatch<span className="brand-dot">.</span></a><nav aria-label="Main navigation"><a href="/#how-it-works">How it works</a><a href="/pricing">Pricing</a><a href="/downloads">Downloads</a></nav><a className="nav-cta" href="/signin">Sign in <span aria-hidden="true">↗</span></a></div></header></>;
}
export function SiteFooter() {
  return <footer className="site-footer wrap"><a className="brand" href="/">SafePatch.</a><p>Evidence for the changes that matter.</p><div><a href="/pricing">Compare plans</a><a href="/downloads">Downloads</a><a href="/signup">Create an account</a></div></footer>;
}
