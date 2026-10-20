export const state = {
  serverTimeMs: null,
  statusData: null,
  queriesData: null,
  rulesData: null,
  connectionData: null,
  queriesFilter: {
    window: "15m",
    decision: "all",
    q: "",
    page: 0,
    pageSize: "25"
  },
  onUnauthorized: null
};

export function setServerTime(isoString) {
  if (!isoString) return;
  const parsed = Date.parse(isoString);
  if (!Number.isNaN(parsed)) {
    state.serverTimeMs = parsed;
  }
}

export function formatTime24(isoString) {
  if (!isoString) return "--:--";
  const d = new Date(isoString);
  if (Number.isNaN(d.getTime())) return "--:--";
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
}

export function formatDateShort(isoString) {
  if (!isoString) return "";
  const d = new Date(isoString);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleDateString([], { year: "numeric", month: "short", day: "numeric" });
}

export function formatRelativeAge(updatedIso, referenceIso) {
  if (!updatedIso) return { text: "--", hours: 0 };
  const updatedMs = Date.parse(updatedIso);
  const refMs = referenceIso ? Date.parse(referenceIso) : state.serverTimeMs;
  if (Number.isNaN(updatedMs) || !refMs || Number.isNaN(refMs)) {
    return { text: "--", hours: 0 };
  }
  const diffSec = Math.max(0, Math.floor((refMs - updatedMs) / 1000));
  const hours = diffSec / 3600;
  if (diffSec < 60) {
    return { text: diffSec + " s ago", hours };
  }
  const mins = Math.floor(diffSec / 60);
  if (mins < 60) {
    return { text: mins + " m ago", hours };
  }
  const hrs = Math.floor(mins / 60);
  if (hrs < 48) {
    return { text: hrs + " h ago", hours };
  }
  const days = Math.floor(hrs / 24);
  return { text: days + " d ago", hours };
}

export function formatNumber(n) {
  if (typeof n !== "number" || Number.isNaN(n)) return "0";
  return n.toLocaleString("en-US");
}

export function formatBytes(bytes) {
  if (typeof bytes !== "number" || Number.isNaN(bytes) || bytes <= 0) {
    return "0 KB";
  }
  const mb = 1048576;
  if (bytes >= mb) {
    const val = (bytes / mb).toFixed(1).replace(/\.0$/, "");
    return val + " MB";
  }
  const kb = Math.max(0.1, bytes / 1024).toFixed(1).replace(/\.0$/, "");
  return kb + " KB";
}

export function mapRuleError(err) {
  if (!err) return "Request failed.";
  if (err.status === 429) {
    const sec = err.retryAfter || 5;
    return "Too many tries. Wait " + sec + " s.";
  }
  switch (err.code) {
    case "invalid_domain":
      return "That is not a valid domain name.";
    case "public_suffix":
      return "That is a public suffix: a rule would block a whole registry.";
    case "protected_name":
      return "That name is protected: blocking it could lock you out.";
    case "file_full":
      return "The rules file is full. Remove old rules first.";
    default:
      return err.message || "Could not update rules.";
  }
}

export function mapConnectionError(err) {
  if (!err) return "Request failed.";
  if (err.status === 429) {
    const sec = err.retryAfter || 5;
    return "Too many tries. Wait " + sec + " s.";
  }
  switch (err.code) {
    case "too_many_names":
      return "Remove an old name first.";
    case "resolve_failed":
      return "The new name does not resolve yet. Try again in a minute.";
    default:
      return err.message || "Could not update connection.";
  }
}

async function request(path, options = {}) {
  const fetchOpts = {
    method: options.method || "GET",
    credentials: "same-origin",
    cache: "no-store",
    headers: {}
  };

  if (fetchOpts.method === "POST") {
    fetchOpts.headers["Content-Type"] = "application/json";
    fetchOpts.headers["X-Hecate-Dashboard"] = "1";
    fetchOpts.body = JSON.stringify(options.body || {});
  }

  let res;
  try {
    res = await fetch(path, fetchOpts);
  } catch (_netErr) {
    const err = new Error("Offline, or this network cannot reach the server.");
    err.offline = true;
    throw err;
  }

  let data = null;
  const contentType = res.headers.get("content-type") || "";
  if (contentType.includes("application/json")) {
    try {
      data = await res.json();
    } catch (_parseErr) {
      data = null;
    }
  }

  if (!res.ok) {
    const err = new Error((data && data.message) || ("HTTP " + res.status));
    err.status = res.status;
    err.code = data && data.error ? data.error : "";
    const headerRetry = parseInt(res.headers.get("Retry-After") || "", 10);
    err.retryAfter = (data && typeof data.retry_after === "number")
      ? data.retry_after
      : (Number.isFinite(headerRetry) ? headerRetry : null);

    if (res.status === 401 && path !== "/api/login") {
      if (typeof state.onUnauthorized === "function") {
        state.onUnauthorized();
      }
    }
    throw err;
  }

  return data;
}

export function apiLogin(key) {
  return request("/api/login", { method: "POST", body: { key } });
}

export function apiLogout() {
  return request("/api/logout", { method: "POST", body: {} });
}

export function apiStatus() {
  return request("/api/status");
}

export function apiQueries(params) {
  const usp = new URLSearchParams();
  usp.set("window", params.window || "15m");
  usp.set("decision", params.decision || "all");
  usp.set("q", params.q || "");
  const size = Number(params.pageSize) || 25;
  usp.set("limit", String(size));
  usp.set("offset", String((Number(params.page) || 0) * size));
  return request("/api/queries?" + usp.toString());
}

export function apiRules() {
  return request("/api/rules");
}

export function apiAddRule(action, domain) {
  return request("/api/rules", { method: "POST", body: { action, domain } });
}

export function apiRemoveRule(domain) {
  return request("/api/rules/remove", { method: "POST", body: { domain } });
}

export function apiOps(action) {
  return request(action ? "/api/ops?action=" + encodeURIComponent(action) : "/api/ops");
}

export function apiRunOp(action) {
  return request("/api/ops/run", { method: "POST", body: { action } });
}

export function apiConnection() {
  return request("/api/connection");
}

export function apiRotateConnection(domain) {
  return request("/api/connection/rotate", { method: "POST", body: { domain } });
}

export function apiRemoveConnection(host) {
  return request("/api/connection/remove", { method: "POST", body: { host } });
}

export function clearNode(node) {
  while (node.firstChild) {
    node.removeChild(node.firstChild);
  }
}

export function createIcon(symbolId, extraClass) {
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("class", extraClass ? "icon " + extraClass : "icon");
  svg.setAttribute("aria-hidden", "true");
  const use = document.createElementNS(ns, "use");
  use.setAttribute("href", "#" + symbolId);
  svg.appendChild(use);
  return svg;
}

export function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) {
    node.textContent = String(text);
  }
  return node;
}

const LABEL_CHARS = "abcdefghijklmnopqrstuvwxyz0123456789";

function generateProbeLabel() {
  const bytes = new Uint8Array(20);
  window.crypto.getRandomValues(bytes);
  let out = "";
  for (let i = 0; i < 20; i += 1) {
    out += LABEL_CHARS[bytes[i] % LABEL_CHARS.length];
  }
  return out;
}

function buildProbeUrl(scheme, label, domain, port) {
  const cleanScheme = scheme || "https";
  const defaultPort = cleanScheme === "http" ? 80 : 443;
  const numPort = Number(port) || defaultPort;
  const portSuffix = numPort === defaultPort ? "" : ":" + numPort;
  return cleanScheme + "://" + label + "." + domain + portSuffix + "/probe";
}

export async function runProbeSequence(probeCfg, fallbackDomains) {
  const probeDomains = Array.isArray(probeCfg.domains) ? probeCfg.domains : fallbackDomains;
  const samples = Math.max(1, Number(probeCfg.samples) || 3);
  const scheme = probeCfg.scheme || "https";
  const results = [];

  for (let i = 0; i < probeDomains.length; i += 1) {
    const dom = probeDomains[i];
    const checks = [];
    for (let s = 0; s < samples; s += 1) {
      const url = buildProbeUrl(scheme, generateProbeLabel(), dom, probeCfg.port);
      const opts = { mode: "no-cors", cache: "no-store", credentials: "omit", signal: AbortSignal.timeout(6000) };
      checks.push(fetch(url, opts));
    }
    const settled = await Promise.allSettled(checks);
    const resolvedCount = settled.filter((r) => r.status === "fulfilled").length;
    results.push({ domain: dom, resolved: resolvedCount, samples });
  }
  return results;
}
