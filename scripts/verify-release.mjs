import assert from "node:assert/strict";
import { readFileSync, readdirSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";

const root = fileURLToPath(new URL("../", import.meta.url));
const read = (path) => readFileSync(resolve(root, path));
const json = (path) => JSON.parse(read(path));
const version = read("VERSION").toString().trim();
assert.match(version, /^\d+\.\d+\.\d+$/);
const registry = json("plugin-store/registry.json");
assert.equal(registry.schema_version, 1);
assert.equal(registry.plugins.length, 1);
const plugin = registry.plugins[0];
assert.equal(plugin.id, "cpa-usage-ma");
assert.equal(plugin.name, "cpa-usage-ma");
assert.equal(plugin.author, "marven-buddy");
assert.equal(plugin.repository, "https://github.com/marven-buddy/cpa-usage-ma");
assert.equal(plugin.homepage, plugin.repository);
assert.equal(plugin.version, version);
const pkg = json("frontend/package.json");
const lock = json("frontend/package-lock.json");
for (const metadata of [pkg, lock, lock.packages[""]]) {
  assert.equal(metadata.name, "cpa-usage-ma-ui");
  assert.equal(metadata.version, version);
}
const source = read("plugin/main.go").toString();
assert.ok(source.includes(`var version = "${version}-dev"`));
assert.ok(source.includes(`"GitHubRepository":"${plugin.repository}"`));
assert.ok(source.includes('"Name":"cpa-usage-ma"'));
assert.ok(!source.includes("usage-report"), "Old plugin ID remains in backend source.");
assert.ok(read("frontend/src/auth.js").toString().includes('"/v0/management/plugins/cpa-usage-ma/api"'));
assert.ok(read("plugin/go.mod").toString().startsWith("module cpa-usage-ma"));
assert.ok(read("CHANGELOG.md").toString().includes(`## ${version}`));
assert.ok(read(`docs/releases/v${version}.md`).toString().startsWith(`# cpa-usage-ma v${version}`));

const assets = readdirSync(resolve(root, "frontend/dist/assets")).sort();
assert.deepEqual(readdirSync(resolve(root, "plugin/web/assets")).sort(), assets,
  "Embedded assets differ from the frontend build; synchronize plugin/web.");
for (const path of ["index.html", ...assets.map((name) => `assets/${name}`)]) {
  assert.deepEqual(read(`plugin/web/${path}`), read(`frontend/dist/${path}`),
    `Stale embedded frontend file: ${path}`);
  assert.ok(!read(`plugin/web/${path}`).includes(Buffer.from("/plugins/usage-report/")),
    `Original plugin API remains in production file: ${path}`);
}
for (const asset of assets) {
  assert.ok(source.includes(`/assets/${asset}`), `Missing registered route: ${asset}`);
  assert.ok(source.includes(`web/assets/${asset}`), `Missing resource handler: ${asset}`);
  assert.ok(read("plugin/web/index.html").toString().includes(asset));
}
assert.ok(read("plugin/web/index.html").toString().includes("cpa-usage-ma"));

// When running locally, reject accidental leakage of the isolated dev secret.
const keyPath = resolve(root, ".dev/management-key");
if (existsSync(keyPath)) {
  const secret = readFileSync(keyPath, "utf8").trim();
  assert.ok(secret.length > 0);
  for (const path of ["index.html", ...assets.map((name) => `assets/${name}`)]) {
    assert.ok(!read(`plugin/web/${path}`).includes(Buffer.from(secret)),
      `Development credential found in production file: ${path}`);
  }
}
console.log(`Release preparation verified: cpa-usage-ma v${version}; embedded assets match the build.`);
