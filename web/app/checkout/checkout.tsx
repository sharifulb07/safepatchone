"use client";

import Script from "next/script";
import { useEffect, useRef, useState } from "react";

type Paddle = {
  Environment: { set: (environment: "sandbox") => void };
  Initialize: (settings: { token: string; checkout: { settings: { displayMode: "overlay"; successUrl: string } }; eventCallback: (event: { name: string }) => void }) => void;
  Checkout: { open: (settings: { transactionId: string }) => void };
};
declare global { interface Window { Paddle?: Paddle } }

export default function Checkout({ token, environment, nonce }: { token: string; environment: string; nonce?: string }) {
  const [transaction, setTransaction] = useState("");
  const [message, setMessage] = useState("Checking your account…");
  const [error, setError] = useState("");
  const [ready, setReady] = useState(false);
  const initialized = useRef(false);
  useEffect(() => {
    if (!token) return;
    const controller = new AbortController();
    const id = new URL(location.href).searchParams.get("_ptxn") ?? "";
    if (!/^txn_[a-z0-9]{26}$/.test(id)) { setError("Start checkout from your organization's selected plan."); return; }
    void fetch("/v1/onboarding/session", { credentials: "same-origin", cache: "no-store", signal: controller.signal }).then(async response => {
      if (response.status === 401) throw new Error("Sign in, then return to your organization to start checkout.");
      if (!response.ok) throw new Error("Your account is temporarily unavailable. Please try again.");
      setTransaction(id);
      setMessage("Opening Paddle's secure payment window…");
    }).catch(e => { if (!controller.signal.aborted) setError(e instanceof Error ? e.message : "Checkout is unavailable."); });
    return () => controller.abort();
  }, [token]);

  function initialize() {
    if (initialized.current || !window.Paddle) return;
    try {
      if (environment === "sandbox") window.Paddle.Environment.set("sandbox");
      window.Paddle.Initialize({ token, checkout: { settings: { displayMode: "overlay", successUrl: `${location.origin}/onboarding` } }, eventCallback: event => {
        if (event.name === "checkout.completed") setMessage("Payment submitted. SafePatch will confirm your subscription on the server before enabling paid access.");
        if (event.name === "checkout.closed") setMessage("Payment window closed. You can reopen it or return to your organization.");
        if (event.name === "checkout.error") setError("Paddle could not open checkout. Return to your organization and try again.");
      } });
      // Paddle.js opens the server-created transaction from the _ptxn URL.
      initialized.current = true;
      setReady(true);
    } catch { setError("Paddle could not initialize checkout. Please try again."); }
  }

  return <>
    {!token ? <p role="alert">Payment checkout is not configured yet. Please contact SafePatch support.</p> : error ? <p role="alert" className="service-alert">{error}</p> : <p role="status">{message}</p>}
    {environment === "sandbox" && token && <p className="onboarding-muted">Sandbox checkout — use Paddle test payment details only.</p>}
    {transaction && !error && <Script src="https://cdn.paddle.com/paddle/v2/paddle.js" nonce={nonce} strategy="afterInteractive" onReady={initialize} onError={() => setError("The payment service could not load. Check your connection and try again.")} />}
    {ready && !error && <button className="action" onClick={() => window.Paddle?.Checkout.open({ transactionId: transaction })}>Reopen payment window</button>}
    <p><a href="/onboarding">Return to your organization</a></p>
    <p className="onboarding-muted">Your plan and download access update after server confirmation. Closing this page does not cancel an existing subscription.</p>
  </>;
}
