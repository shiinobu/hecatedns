import { clearNode, createIcon, el, formatTime24, setServerTime } from "./api.js";

let toastTimer = null;
let rateLimitTimer = null;

export const ROUTES = {
  status: "Status",
  queries: "Queries",
  rules: "Rules",
  connection: "Connection"
};

const ROUTE_INDEX = {
  status: "0",
  queries: "1",
  rules: "2",
  connection: "3"
};

export const shellState = {
  currentRoute: "status",
  isLoginVisible: false,
  isLoading: false
};

export function parseHashRoute() {
  const hash = window.location.hash || "#/status";
  const match = hash.replace(/^#\/?/, "").toLowerCase();
  if (Object.prototype.hasOwnProperty.call(ROUTES, match)) {
    return match;
  }
  return "status";
}

export function syncServerTime(isoString) {
  if (isoString) setServerTime(isoString);
}

export function updateUpdatedMeta(isoString, isStale) {
  const updEl = document.getElementById("updated-text");
  const staleEl = document.getElementById("stale-badge");
  if (updEl && isoString) {
    updEl.textContent = "Updated " + formatTime24(isoString);
  }
  if (staleEl) {
    staleEl.hidden = !isStale;
  }
}

export function setInteractiveDisabled(disabled) {
  shellState.isLoading = disabled;
  const refreshBtn = document.getElementById("btn-refresh");
  if (refreshBtn) refreshBtn.disabled = disabled;
  const blockBtn = document.getElementById("btn-rule-block");
  const allowBtn = document.getElementById("btn-rule-allow");
  if (blockBtn) blockBtn.disabled = disabled;
  if (allowBtn) allowBtn.disabled = disabled;
}

export function showLoginMode() {
  shellState.isLoginVisible = true;
  clearBanner();
  const headerEl = document.getElementById("shell-header");
  const screenBar = document.getElementById("screen-bar");
  const tabBar = document.getElementById("tab-bar");
  const logoutBtn = document.getElementById("btn-logout");
  document.querySelector(".app-shell").classList.remove("is-fixed");
  if (headerEl) headerEl.hidden = true;
  if (screenBar) screenBar.hidden = true;
  if (tabBar) tabBar.hidden = true;
  if (logoutBtn) logoutBtn.hidden = true;

  Object.keys(ROUTES).forEach((r) => {
    const sec = document.getElementById("view-" + r);
    if (sec) sec.hidden = true;
  });

  const loginSec = document.getElementById("view-login");
  if (loginSec) loginSec.hidden = false;
  const keyInput = document.getElementById("login-key");
  if (keyInput) {
    keyInput.value = "";
    keyInput.focus();
  }
}

export function showShellMode() {
  shellState.isLoginVisible = false;
  const route = shellState.currentRoute;
  const headerEl = document.getElementById("shell-header");
  const loginSec = document.getElementById("view-login");
  const screenBar = document.getElementById("screen-bar");
  const tabBar = document.getElementById("tab-bar");
  const logoutBtn = document.getElementById("btn-logout");
  document.querySelector(".app-shell").classList.toggle("is-fixed", route === "queries");
  if (headerEl) headerEl.hidden = false;
  if (loginSec) loginSec.hidden = true;
  if (screenBar) screenBar.hidden = false;
  if (tabBar) {
    tabBar.hidden = false;
    tabBar.setAttribute("data-active-index", ROUTE_INDEX[route] || "0");
  }
  if (logoutBtn) logoutBtn.hidden = false;

  const titleEl = document.getElementById("screen-title");
  if (titleEl) titleEl.textContent = ROUTES[route] || "Status";

  Object.keys(ROUTES).forEach((r) => {
    const sec = document.getElementById("view-" + r);
    if (sec) sec.hidden = r !== route;
  });

  const tabs = document.querySelectorAll(".tab-item");
  tabs.forEach((tab) => {
    const r = tab.getAttribute("data-route");
    if (r === route) {
      tab.setAttribute("aria-current", "page");
    } else {
      tab.removeAttribute("aria-current");
    }
  });
}

export function clearBanner() {
  if (rateLimitTimer) {
    clearTimeout(rateLimitTimer);
    rateLimitTimer = null;
  }
  const slot = document.getElementById("banner-slot");
  if (slot) clearNode(slot);
}

export function showErrorBanner(err, onRetry) {
  clearBanner();
  const slot = document.getElementById("banner-slot");
  if (!slot) return;

  const banner = el("div", "banner");
  const body = el("div", "banner-body");
  body.appendChild(createIcon("icon-warn"));
  const msgSpan = el("span", "");
  body.appendChild(msgSpan);
  banner.appendChild(body);

  if (err && err.status === 429) {
    let remaining = err.retryAfter || 5;
    const tick = () => {
      msgSpan.textContent = "Too many tries. Wait " + remaining + " s.";
      if (remaining > 0) {
        remaining -= 1;
        rateLimitTimer = setTimeout(tick, 1000);
      }
    };
    tick();
  } else if (err && err.offline) {
    msgSpan.textContent = "Offline, or this network cannot reach the server.";
  } else {
    const code = err && err.status ? err.status : "error";
    msgSpan.textContent = "Could not load (HTTP " + code + ").";
  }

  if (typeof onRetry === "function") {
    const retryBtn = el("button", "btn btn-text", "Retry");
    retryBtn.type = "button";
    retryBtn.addEventListener("click", () => {
      onRetry();
    });
    banner.appendChild(retryBtn);
  }

  slot.appendChild(banner);
}

export function showToast(message, onUndo) {
  if (toastTimer) {
    clearTimeout(toastTimer);
    toastTimer = null;
  }
  const slot = document.getElementById("toast-slot");
  if (!slot) return;
  clearNode(slot);

  const toast = el("div", "toast");
  const text = el("span", "toast-text", message);
  toast.appendChild(text);

  if (typeof onUndo === "function") {
    const undoBtn = el("button", "btn btn-text", "Undo");
    undoBtn.type = "button";
    undoBtn.addEventListener("click", () => {
      if (toastTimer) {
        clearTimeout(toastTimer);
        toastTimer = null;
      }
      clearNode(slot);
      onUndo();
    });
    toast.appendChild(undoBtn);
  }

  slot.appendChild(toast);
  toastTimer = setTimeout(() => {
    clearNode(slot);
    toastTimer = null;
  }, 8000);
}

export function renderSkeletonCards(container, count) {
  clearNode(container);
  for (let i = 0; i < count; i += 1) {
    const card = el("div", "card");
    card.appendChild(el("div", "skeleton"));
    card.appendChild(el("div", "skeleton skeleton-lg"));
    container.appendChild(card);
  }
}
