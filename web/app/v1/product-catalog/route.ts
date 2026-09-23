import { catalog } from "../../../lib/catalog";

// Public marketing metadata only. Never use this endpoint for authorization.
export const dynamic = "force-static";
export function GET() {
  return Response.json(catalog, { headers: { "X-Content-Type-Options": "nosniff" } });
}
