import { spawnSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../../", import.meta.url));
const generated = new URL("../.generated/", import.meta.url);
// Fixed command and arguments; no shell interpolation or request input.
const result = spawnSync("go", ["run", "./web/scripts/catalog"], {
  cwd: root, encoding: "utf8", maxBuffer: 2 * 1024 * 1024, timeout: 120000,
  env: { ...process.env, GO_TELEMETRY_CHILD: "2" },
});
if (result.error || result.status !== 0) {
  console.error("Could not generate the product catalog. Check Go and the SafePatch public configuration.", result.error?.code ?? result.status);
  process.exit(1);
}
JSON.parse(result.stdout);
mkdirSync(generated, { recursive: true });
writeFileSync(new URL("product-catalog.json", generated), result.stdout, "utf8");
