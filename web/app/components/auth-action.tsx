"use client";
import { useEffect, useState } from "react";
export default function AuthAction({ signup }: { signup: boolean }) {
  const [signedIn, setSignedIn] = useState(false);
  const [unavailable, setUnavailable] = useState(false);
  const [destination, setDestination] = useState("/onboarding");
  useEffect(() => {
    if (new URLSearchParams(location.search).get("return_to") === "/downloads") setDestination("/downloads");
    const controller = new AbortController();
    fetch("/v1/onboarding/session", { cache: "no-store", signal: controller.signal }).then(r => {
      if (r.ok) setSignedIn(true); else if (r.status !== 401) setUnavailable(true);
    }).catch(() => { if (!controller.signal.aborted) setUnavailable(true); });
    return () => controller.abort();
  }, []);
  const login = "/v1/auth/github/start" + (destination === "/downloads" ? "?return_to=%2Fdownloads" : "");
  return <>{unavailable && <p role="alert" className="service-alert">Sign-in is temporarily unavailable. Please try again when the account service is connected.</p>}<a className="action primary" href={signedIn ? destination : login}>{signedIn ? "Continue to your account" : signup ? "Sign up with GitHub" : "Sign in with GitHub"}<span aria-hidden="true">↗</span></a>{signedIn && <a className="text-link auth-download-link" href="/downloads">Go to your downloads →</a>}</>;
}
