// Loads web/ikml.wasm in Node and calls the functions the web page uses,
// on fixtures from testdata. Run: node scripts/wasm-smoke.mjs
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");
await import(path.join(root, "web", "wasm_exec.js"));
const go = new globalThis.Go();
const { instance } = await WebAssembly.instantiate(fs.readFileSync(path.join(root, "web", "ikml.wasm")), go.importObject);
go.run(instance);

let failed = 0;
function check(what, ok, detail = "") {
  console.log(`${ok ? "ok  " : "FAIL"} ${what}${detail ? ": " + detail : ""}`);
  if (!ok) { failed++; console.log(`::error::wasm smoke: ${what} ${detail}`); }
}
const file = (p) => new Uint8Array(fs.readFileSync(path.join(root, "testdata", p)));

const version = fs.readFileSync(path.join(root, "VERSION"), "utf8").trim();
check("version", globalThis.ikmlVersion === version, globalThis.ikmlVersion);

let r = JSON.parse(globalThis.ikmlInspect(file("bad/swapped-latlon.kml"), "swapped-latlon.kml"));
check("inspect finds swapped-latlon", r.report.issues.some((i) => i.code === "swapped-latlon"));

r = JSON.parse(globalThis.ikmlInspect(file("good/mymaps-export.kml"), "mymaps-export.kml"));
check("good file has no errors", r.report.errors === 0, String(r.report.errors));
check("preview has shapes", r.preview.length > 0, String(r.preview.length));
check("loss report for geojson", r.target === "geojson" && Array.isArray(r.losses));

r = JSON.parse(globalThis.ikmlInspect(file("good/dji-mission.kmz"), "dji-mission.kmz"));
check("kmz opens", r.report.format === "kmz" && r.report.readable, r.report.format);

const c = globalThis.ikmlConvert(file("good/mymaps-export.kml"), "mymaps-export.kml", "geojson");
const gj = JSON.parse(new TextDecoder().decode(c.bytes));
check("convert to geojson", gj.type === "FeatureCollection" && gj.features.length > 0, c.name);

const k = globalThis.ikmlClean(file("bad/unclosed-ring.kml"), "unclosed-ring.kml", "kml", false);
const rep = JSON.parse(k.report);
check("clean closes the ring", rep.changes.some((x) => x.code === "ring-unclosed"), k.name);

r = JSON.parse(globalThis.ikmlInspect(new Uint8Array([1, 2, 3]), "junk.bin"));
check("garbage gives an error, not a crash", typeof r.error === "string" || r.report?.errors > 0);

if (failed) { console.log(`${failed} check(s) failed`); process.exit(1); }
console.log("wasm smoke test passed");
process.exit(0);
