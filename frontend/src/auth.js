export const SESSION_KEY = "cpa-usage-ma-management-key";
const API_BASE = "/v0/management/plugins/cpa-usage-ma/api";

export class AuthError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "AuthError";
    this.code = code;
  }
}

export function decodeCPAStorage(raw, { host, userAgent }) {
  if (!raw) return null;
  let text = raw;
  if (raw.startsWith("enc::")) {
    const version = raw.startsWith("enc::v1::") ? 1 : raw.startsWith("enc::v2::") ? 2 : 0;
    if (!version) throw new AuthError("storage_format", "后台凭据格式暂不支持，请在插件中输入管理密钥。");
    const encoded = atob(raw.slice("enc::v1::".length));
    const key = new TextEncoder().encode(
      `cli-proxy-api-webui::secure-storage|${host}${version === 1 ? `|${userAgent}` : ""}`,
    );
    const bytes = Uint8Array.from(encoded, (char, i) => char.charCodeAt(0) ^ key[i % key.length]);
    text = new TextDecoder().decode(bytes);
  }
  return JSON.parse(text);
}

export function createManagementClient({ localStorage, sessionStorage, host, userAgent, fetch, devKey = "" }) {
  let manualKey = "";
  try { manualKey = sessionStorage?.getItem(SESSION_KEY)?.trim() || ""; } catch { /* Storage may be disabled. */ }

  function credential() {
    if (manualKey) return { key: manualKey, source: "manual" };
    if (devKey) return { key: devKey, source: "development" };
    let raw;
    try { raw = localStorage?.getItem("cli-proxy-auth"); } catch {
      throw new AuthError("storage_unavailable", "无法读取后台登录凭据，请在插件中输入管理密钥。");
    }
    let parsed;
    try { parsed = decodeCPAStorage(raw, { host, userAgent }); } catch (error) {
      if (error instanceof AuthError) throw error;
      throw new AuthError("storage_format", "后台登录凭据无法解析，请在插件中输入管理密钥。");
    }
    const state = parsed?.state ?? parsed;
    const key = typeof state?.managementKey === "string" ? state.managementKey.trim() : "";
    if (!key) throw new AuthError("missing_key", "未读取到后台管理密钥，请在插件中输入；无需勾选后台的“记住密码”。");
    return { key, source: "panel" };
  }

  async function send(path, options, key) {
    const headers = new Headers(options.headers);
    headers.set("Authorization", `Bearer ${key}`);
    if (options.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    const response = await fetch(`${API_BASE}${path}`, { ...options, headers });
    if (response.status === 401) throw new AuthError("unauthorized", "管理密钥无效或已失效，请重新输入。");
    if (response.status === 403) throw new AuthError("forbidden", "访问被拒绝，请检查管理接口访问限制或稍后重试。");
    const data = await response.json();
    if (!response.ok || data.error) throw Error(data.error || `服务返回 ${response.status}`);
    return data;
  }

  return {
    credential,
    request(path, options = {}) { return send(path, options, credential().key); },
    async connect(value) {
      const key = value.trim();
      if (!key) throw new AuthError("missing_key", "请输入 CPA 管理密钥。");
      // Verify the supplied key directly; never fall back to panel or development credentials.
      const health = await send("/health", {}, key);
      if (health.service !== "cpa-usage-ma") throw Error("管理接口响应不符合预期，请检查插件地址。");
      // A failed verification must leave the existing credential intact.
      manualKey = key;
      let persisted = false;
      try {
        sessionStorage?.setItem(SESSION_KEY, key);
        persisted = Boolean(sessionStorage);
      } catch { /* Keep a page-only credential when session storage is unavailable. */ }
      return { persisted };
    },
    clearManualKey() {
      manualKey = "";
      try { sessionStorage?.removeItem(SESSION_KEY); } catch { /* Storage may be disabled. */ }
    },
  };
}
