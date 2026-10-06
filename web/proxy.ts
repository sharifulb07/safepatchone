import { NextRequest, NextResponse } from "next/server";
import { randomBytes } from "node:crypto";

export function proxy(request: NextRequest) {
  const nonce = randomBytes(18).toString("base64");
  const dev = process.env.NODE_ENV === "development";
  const checkout = request.nextUrl.pathname === "/checkout";
  const paddle = checkout ? " https://*.paddle.com" : "";
  const csp = `default-src 'self'; script-src 'self' 'nonce-${nonce}' 'strict-dynamic'${dev ? " 'unsafe-eval'" : ""}; style-src 'self' 'unsafe-inline'; img-src 'self' data:${paddle}; font-src 'self'; connect-src 'self'${paddle}; frame-src ${checkout ? "https://*.paddle.com" : "'none'"}; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'`;
  const headers = new Headers(request.headers);
  headers.set("Content-Security-Policy", csp);
  headers.set("x-nonce", nonce);
  const response = NextResponse.next({ request: { headers } });
  response.headers.set("Content-Security-Policy", csp);
  response.headers.set("Cache-Control", "private, no-store");
  return response;
}
export const config = { matcher: ["/((?!_next/static|_next/image|favicon.ico|v1/).*)"] };
