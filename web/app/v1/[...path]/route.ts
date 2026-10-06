import { NextRequest } from "next/server";

export const dynamic = "force-dynamic";
// This route is a presentation proxy only. Authentication, tenant ownership,
// entitlements, OAuth verification and all mutations stay in Go.
async function proxy(request: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  const { path } = await params;
  const downloads = path[0] === "downloads" && path[1] === "public" && (path.length === 2 || path.length === 3) && request.method === "GET";
  const organizationDownloads = path[0] === "organizations" && path[2] === "downloads" && ((path.length === 3 && request.method === "GET") || (path.length === 4 && request.method === "POST"));
  if (!path.length || (!["onboarding", "auth"].includes(path[0]) && !downloads && !organizationDownloads) || path.some(p => !/^[a-zA-Z0-9_-]+$/.test(p))) return Response.json({ error: "NOT_FOUND" }, { status: 404, headers: { "Cache-Control": "no-store" } });
  // Browser routes accept only HttpOnly sessions. Do not silently discard a
  // second credential and let the API act as a different principal.
  if (request.headers.has("authorization")) return Response.json({ error: "AUTHENTICATION_REJECTED" }, { status: 400, headers: { "Cache-Control": "no-store" } });
  const origin = process.env.SAFEPATCH_API_ORIGIN;
  if (!origin) return Response.json({ error: "ONBOARDING_UNAVAILABLE" }, { status: 503, headers: { "Cache-Control": "no-store" } });
  try {
    const base = new URL(origin);
    if (base.username || base.password || base.pathname !== "/" || base.search || base.hash || (base.protocol !== "https:" && !(base.protocol === "http:" && ["127.0.0.1", "[::1]"].includes(base.hostname)))) throw new Error("invalid upstream");
    const target = new URL(`/v1/${path.join("/")}`, base);
    target.search = request.nextUrl.search;
    const headers = new Headers();
    for (const name of ["cookie", "origin", "content-type", "x-csrf-token"]) { const value = request.headers.get(name); if (value) headers.set(name, value); }
    let body: Uint8Array | undefined;
    if (request.method === "POST") {
      const reader = request.body?.getReader(); const chunks: Uint8Array[] = []; let size = 0;
      if (reader) { while (true) { const { done, value } = await reader.read(); if (done) break; size += value.length; if (size > 32768) { await reader.cancel(); return Response.json({ error: "BODY_TOO_LARGE" }, { status: 413 }); } chunks.push(value); } }
      body = new Uint8Array(size); let offset = 0; for (const chunk of chunks) { body.set(chunk, offset); offset += chunk.length; }
    }
    const response = await fetch(target, { method: request.method, headers, body: body as BodyInit | undefined, redirect: "manual", cache: "no-store", signal: AbortSignal.timeout(28000) });
    const out = new Headers({ "Cache-Control": "no-store", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff" });
    for (const name of ["content-type", "location", "allow"]) { const value = response.headers.get(name); if (value) out.set(name, value); }
    for (const cookie of response.headers.getSetCookie()) out.append("Set-Cookie", cookie);
    return new Response(response.body, { status: response.status, headers: out });
  } catch { return Response.json({ error: "ONBOARDING_UNAVAILABLE" }, { status: 503, headers: { "Cache-Control": "no-store" } }); }
}
export const GET = proxy;
export const POST = proxy;
