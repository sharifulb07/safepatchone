import { SiteHeader, SiteFooter } from "../components/site";
import Downloads from "./downloads";
export const metadata = { title: "Downloads · SafePatch", description: "Find SafePatch downloads for Windows, Linux, and macOS, plus the bundles available to your organization." };
export default function DownloadsPage() { return <><SiteHeader /><main id="main" className="wrap downloads-main"><section className="hero"><p className="eyebrow">YOUR PLATFORM. THE SAME FOUNDATION.</p><h1>Make yourself<br /><em>at home.</em></h1><p className="hero-copy">Find the right download for your machine.<br />Sign in to access your organization’s eligible bundles.</p></section><Downloads /></main><SiteFooter /></>; }
