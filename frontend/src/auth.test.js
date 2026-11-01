import test from "node:test";
import assert from "node:assert/strict";
import { AuthError, SESSION_KEY, createManagementClient, decodeCPAStorage } from "./auth.js";

const host = "cpa.test:8317";
const userAgent = "Synthetic review browser";
const env = { host, userAgent };
const ok = () => new Response(JSON.stringify({ service: "cpa-usage-ma" }), { status: 200 });
function storage(entries = []) {
  const data = new Map(entries);
  return { getItem: (key) => data.get(key) ?? null, setItem: (key, value) => data.set(key, value), removeItem: (key) => data.delete(key) };
}
function encode(version, payload) {
  const key = new TextEncoder().encode(`cli-proxy-api-webui::secure-storage|${host}${version === 1 ? `|${userAgent}` : ""}`);
  const data = new TextEncoder().encode(JSON.stringify(payload));
  return `enc::v${version}::${Buffer.from(data.map((value, i) => value ^ key[i % key.length])).toString("base64")}`;
}
function fixture(options = {}) {
  return createManagementClient({ ...env, localStorage: storage(), sessionStorage: storage(), fetch: async () => ok(), ...options });
}

test("panel v1 and v2 credentials decode with their distinct host/UA rules", async () => {
  for (const version of [1, 2]) {
    const payload = { state: { managementKey: "panel-key", label: "后台凭据" }, version: 0 };
    assert.deepEqual(decodeCPAStorage(encode(version, payload), env), payload);
    let sent;
    const client = fixture({
      localStorage: storage([["cli-proxy-auth", encode(version, payload)]]),
      fetch: async (url, options) => { sent = { url, options }; return ok(); },
    });
    await client.request("/health");

    assert.equal(sent.url, "/v0/management/plugins/cpa-usage-ma/api/health");
    assert.equal(client.credential().source, "panel");
    assert.equal(sent.options.headers.get("Authorization"), "Bearer panel-key");
    if (version === 2) assert.deepEqual(decodeCPAStorage(encode(version, payload), { ...env, userAgent: "Changed browser" }), payload);
  }
});

test("legacy plain JSON credentials work, but an unremembered session has no key", () => {
  for (const payload of [{ managementKey: " direct " }, { state: { managementKey: " direct " } }]) {
    assert.equal(fixture({ localStorage: storage([["cli-proxy-auth", JSON.stringify(payload)]]) }).credential().key, "direct");
  }
  const client = fixture({ localStorage: storage([["cli-proxy-auth", JSON.stringify({ state: { rememberPassword: false } })]]) });
  assert.throws(() => client.credential(), (e) => e instanceof AuthError && e.code === "missing_key");
});

test("missing, unsupported and corrupt credentials are distinguishable without exposing their contents", () => {
  assert.throws(() => fixture().credential(), { code: "missing_key" });
  for (const raw of ["enc::v3::private-value", "enc::v2::%%", "invalid-private-json"]) {
    assert.throws(() => fixture({ localStorage: storage([["cli-proxy-auth", raw]]) }).credential(), (e) => e.code === "storage_format" && !e.message.includes(raw));
  }
  const blocked = { getItem() { throw Error("blocked"); } };
  assert.throws(() => fixture({ localStorage: blocked }).credential(), { code: "storage_unavailable" });
});

test("manual key overrides auto credentials only after verification and survives a same-tab reload", async () => {
  const local = storage([["cli-proxy-auth", encode(2, { state: { managementKey: "panel-key" } })]]);
  const session = storage();
  let supplied;
  const client = fixture({ localStorage: local, sessionStorage: session, devKey: "dev-key", fetch: async (url, options) => {
    supplied = options.headers.get("Authorization"); return ok();
  } });
  assert.deepEqual(await client.connect(" manual-key "), { persisted: true });
  assert.equal(supplied, "Bearer manual-key");
  assert.equal(session.getItem(SESSION_KEY), "manual-key");
  assert.equal(local.getItem(SESSION_KEY), null);
  assert.equal(client.credential().key, "manual-key");
  assert.equal(fixture({ localStorage: local, sessionStorage: session }).credential().key, "manual-key");
  assert.equal(fixture({ localStorage: local, sessionStorage: storage() }).credential().key, "panel-key");
  client.clearManualKey();
  assert.equal(session.getItem(SESSION_KEY), null);
  assert.equal(client.credential().key, "dev-key");
  assert.equal(fixture({ localStorage: local, sessionStorage: session }).credential().key, "panel-key");
});

test("bad manual key cannot be verified by falling back to a valid automatic key", async () => {
  const session = storage([[SESSION_KEY, "existing-good-key"]]);
  let supplied;
  const client = fixture({ sessionStorage: session, devKey: "valid-dev-key", fetch: async (url, options) => {
    supplied = options.headers.get("Authorization"); return new Response("", { status: 401 });
  } });
  await assert.rejects(client.connect("bad-key"), { code: "unauthorized" });
  assert.equal(supplied, "Bearer bad-key");
  assert.equal(session.getItem(SESSION_KEY), "existing-good-key");
  assert.equal(client.credential().key, "existing-good-key");
  await assert.rejects(client.connect("  "), { code: "missing_key" });
});

test("401, 403, transport failures and unexpected health responses retain distinct errors", async () => {
  for (const [status, code] of [[401, "unauthorized"], [403, "forbidden"]]) {
    const client = fixture({ devKey: "key", fetch: async () => new Response("not JSON", { status }) });
    await assert.rejects(client.request("/usage"), { code });
  }
  await assert.rejects(fixture({ fetch: async () => { throw Error("network unavailable"); } }).connect("key"), /network unavailable/);
  const client = fixture({ fetch: async () => new Response(JSON.stringify({ service: "another-service" })) });
  await assert.rejects(client.connect("key"), /响应不符合预期/);
  assert.throws(() => client.credential(), { code: "missing_key" });
});

test("blocked browser storage still allows a verified page-only manual credential", async () => {
  const blocked = { getItem() { throw Error("blocked"); }, setItem() { throw Error("blocked"); }, removeItem() { throw Error("blocked"); } };
  const client = fixture({ localStorage: blocked, sessionStorage: blocked });
  assert.deepEqual(await client.connect("page-only-key"), { persisted: false });
  assert.equal(client.credential().key, "page-only-key");
  client.clearManualKey();
  assert.throws(() => client.credential(), { code: "storage_unavailable" });
});
