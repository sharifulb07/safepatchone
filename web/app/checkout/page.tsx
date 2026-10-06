import { headers } from "next/headers";
import { SiteHeader, SiteFooter } from "../components/site";
import Checkout from "./checkout";

export const metadata = { title: "Secure checkout · SafePatch", robots: { index: false, follow: false } };
export default async function CheckoutPage() {
  const nonce = (await headers()).get("x-nonce") ?? undefined;
  const environment = process.env.SAFEPATCH_PADDLE_ENVIRONMENT ?? "";
  const token = process.env.SAFEPATCH_PADDLE_CLIENT_TOKEN ?? "";
  // Only the publishable client token crosses the server/client boundary.
  const enabled = (environment === "sandbox" && /^test_[a-zA-Z0-9]+$/.test(token)) || (environment === "production" && /^live_[a-zA-Z0-9]+$/.test(token));
  return <><SiteHeader /><main id="main" className="wrap auth-main"><section className="auth-card"><p className="eyebrow">SAFEPATCH BILLING</p><h1>Secure checkout</h1><Checkout token={enabled ? token : ""} environment={environment} nonce={nonce} /></section></main><SiteFooter /></>;
}
