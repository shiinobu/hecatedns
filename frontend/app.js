import {
  apiAddRule,
  apiLogin,
  apiLogout,
  apiQueries,
  apiRemoveRule,
  apiRules,
  apiStatus,
  formatTime24,
  mapRuleError,
  setServerTime,
  state
} from "./api.js";
import { initQueriesControls, renderQueriesView } from "./queries.js";
import {
  clearBanner,
  renderRulesList,
  renderSkeletonCards,
  renderStatusView,
  showErrorBanner,
  showToast
} from "./views.js";

const ROUTES = {
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

let currentRoute = "status";
let isLoginVisible = false;
let isLoading = false;

function parseHashRoute() {
  const hash = window.location.hash || "#/status";
  const match = hash.replace(/^#\/?/, "").toLowerCase();
  if (Object.prototype.hasOwnProperty.call(ROUTES, match)) {
    return match;
  }
  return "status";
}

function updateHeaderClock(isoString) {
  if (!isoString) return;
  setServerTime(isoString);
  const clockEl = document.getElementById("server-clock");
  if (clockEl) {
    clockEl.textContent = formatTime24(isoString);
  }
}

function updateUpdatedMeta(isoString, isStale) {
  const updEl = document.getElementById("updated-text");
  const staleEl = document.getElementById("stale-badge");
  if (updEl && isoString) {
    updEl.textContent = "Updated " + formatTime24(isoString);
  }
  if (staleEl) {
    staleEl.hidden = !isStale;
  }
}

function setInteractiveDisabled(disabled) {
  isLoading = disabled;
  const refreshBtn = document.getElementById("btn-refresh");
  if (refreshBtn) refreshBtn.disabled = disabled;
  const blockBtn = document.getElementById("btn-rule-block");
  const allowBtn = document.getElementById("btn-rule-allow");
  if (blockBtn) blockBtn.disabled = disabled;
  if (allowBtn) allowBtn.disabled = disabled;
}

function showLoginMode() {
  isLoginVisible = true;
  clearBanner();
  const headerEl = document.getElementById("shell-header");
  const screenBar = document.getElementById("screen-bar");
  const tabBar = document.getElementById("tab-bar");
  const logoutBtn = document.getElementById("btn-logout");
  const clockEl = document.getElementById("server-clock");
  if (headerEl) headerEl.hidden = true;
  if (screenBar) screenBar.hidden = true;
  if (tabBar) tabBar.hidden = true;
  if (logoutBtn) logoutBtn.hidden = true;
  if (clockEl) clockEl.hidden = true;

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

function showShellMode() {
  isLoginVisible = false;
  const headerEl = document.getElementById("shell-header");
  const loginSec = document.getElementById("view-login");
  const screenBar = document.getElementById("screen-bar");
  const tabBar = document.getElementById("tab-bar");
  const logoutBtn = document.getElementById("btn-logout");
  const clockEl = document.getElementById("server-clock");
  if (headerEl) headerEl.hidden = false;
  if (loginSec) loginSec.hidden = true;
  if (screenBar) screenBar.hidden = false;
  if (tabBar) {
    tabBar.hidden = false;
    tabBar.setAttribute("data-active-index", ROUTE_INDEX[currentRoute] || "0");
  }
  if (logoutBtn) logoutBtn.hidden = false;
  if (clockEl) clockEl.hidden = false;

  const titleEl = document.getElementById("screen-title");
  if (titleEl) titleEl.textContent = ROUTES[currentRoute] || "Status";

  Object.keys(ROUTES).forEach((r) => {
    const sec = document.getElementById("view-" + r);
    if (sec) sec.hidden = r !== currentRoute;
  });

  const tabs = document.querySelectorAll(".tab-item");
  tabs.forEach((tab) => {
    const r = tab.getAttribute("data-route");
    if (r === currentRoute) {
      tab.setAttribute("aria-current", "page");
    } else {
      tab.removeAttribute("aria-current");
    }
  });
}

async function loadStatusScreen() {
  const container = document.getElementById("status-content");
  clearBanner();
  setInteractiveDisabled(true);
  if (!state.statusData && container) {
    renderSkeletonCards(container, 3);
  }
  try {
    const data = await apiStatus();
    state.statusData = data;
    updateHeaderClock(data.time);
    updateUpdatedMeta(data.time, false);
    if (container) renderStatusView(container, data);
  } catch (err) {
    if (err && err.status === 401) return;
    if (state.statusData && container) {
      renderStatusView(container, state.statusData);
      updateUpdatedMeta(state.statusData.time, true);
    }
    showErrorBanner(err, () => loadStatusScreen());
  } finally {
    setInteractiveDisabled(false);
  }
}

async function loadQueriesScreen() {
  const listEl = document.getElementById("queries-list");
  clearBanner();
  setInteractiveDisabled(true);
  if (!state.queriesData && listEl) {
    renderSkeletonCards(listEl, 2);
  }
  try {
    if (!state.serverTimeMs) {
      const st = await apiStatus();
      state.statusData = st;
      updateHeaderClock(st.time);
    }
    const data = await apiQueries(state.queriesFilter);
    state.queriesData = data;
    updateHeaderClock(data.generated);
    updateUpdatedMeta(data.generated, false);
    renderQueriesView(data, () => loadQueriesScreen(), false);
  } catch (err) {
    if (err && err.status === 401) return;
    if (state.queriesData) {
      renderQueriesView(state.queriesData, () => loadQueriesScreen(), false);
      updateUpdatedMeta(state.queriesData.generated, true);
    }
    showErrorBanner(err, () => loadQueriesScreen());
  } finally {
    setInteractiveDisabled(false);
  }
}

function setRulesInlineError(text) {
  const errEl = document.getElementById("rules-error");
  if (!errEl) return;
  if (!text) {
    errEl.textContent = "";
    errEl.hidden = true;
  } else {
    errEl.textContent = text;
    errEl.hidden = false;
  }
}

async function handleRuleRemoveOnRules(domain) {
  setRulesInlineError("");
  setInteractiveDisabled(true);
  try {
    const res = await apiRemoveRule(domain);
    const removed = res && res.removed ? res.removed : { action: "block", domain };
    showToast("Rule removed: " + removed.domain, async () => {
      try {
        await apiAddRule(removed.action, removed.domain);
        loadRulesScreen();
      } catch (undoErr) {
        setRulesInlineError(mapRuleError(undoErr));
      }
    });
    await loadRulesScreen();
  } catch (err) {
    if (err && err.status === 401) return;
    setRulesInlineError(mapRuleError(err));
  } finally {
    setInteractiveDisabled(false);
  }
}

async function handleRuleSubmitOnRules(action) {
  const input = document.getElementById("rules-domain");
  if (!input || isLoading) return;
  const domain = input.value.trim();
  setRulesInlineError("");
  setInteractiveDisabled(true);
  try {
    const res = await apiAddRule(action, domain);
    input.value = "";
    const added = res && res.rule ? res.rule : { action, domain };
    const replaced = res ? res.replaced : null;
    showToast("Rule added: " + added.action + " " + added.domain, async () => {
      try {
        if (replaced === null || replaced === undefined) {
          await apiRemoveRule(added.domain);
        } else {
          await apiAddRule(replaced.action, replaced.domain);
        }
        loadRulesScreen();
      } catch (undoErr) {
        setRulesInlineError(mapRuleError(undoErr));
      }
    });
    await loadRulesScreen();
  } catch (err) {
    if (err && err.status === 401) return;
    setRulesInlineError(mapRuleError(err));
  } finally {
    setInteractiveDisabled(false);
  }
}

async function loadRulesScreen() {
  const listEl = document.getElementById("rules-list");
  const summaryEl = document.getElementById("rules-summary");
  clearBanner();
  setRulesInlineError("");
  setInteractiveDisabled(true);
  if (!state.rulesData && listEl) {
    renderSkeletonCards(listEl, 2);
  }
  try {
    const st = await apiStatus();
    state.statusData = st;
    updateHeaderClock(st.time);
    const data = await apiRules();
    state.rulesData = data;
    updateUpdatedMeta(st.time, false);
    if (listEl) {
      renderRulesList(listEl, summaryEl, data, handleRuleRemoveOnRules, false);
    }
  } catch (err) {
    if (err && err.status === 401) return;
    if (state.rulesData && listEl) {
      renderRulesList(listEl, summaryEl, state.rulesData, handleRuleRemoveOnRules, false);
      if (state.statusData) updateUpdatedMeta(state.statusData.time, true);
    }
    showErrorBanner(err, () => loadRulesScreen());
  } finally {
    setInteractiveDisabled(false);
  }
}

async function loadConnectionScreen() {
  clearBanner();
  try {
    const st = await apiStatus();
    state.statusData = st;
    updateHeaderClock(st.time);
    updateUpdatedMeta(st.time, false);
  } catch (err) {
    if (err && err.status === 401) return;
    showErrorBanner(err, () => loadConnectionScreen());
  }
}

export function loadActiveScreen() {
  showShellMode();
  if (currentRoute === "status") {
    loadStatusScreen();
  } else if (currentRoute === "queries") {
    loadQueriesScreen();
  } else if (currentRoute === "rules") {
    loadRulesScreen();
  } else if (currentRoute === "connection") {
    loadConnectionScreen();
  }
}

function handleRouteChange() {
  const next = parseHashRoute();
  if (window.location.hash !== "#/" + next) {
    window.history.replaceState(null, "", "#/" + next);
  }
  currentRoute = next;
  if (!isLoginVisible) {
    loadActiveScreen();
  }
}

function initApp() {
  state.onUnauthorized = () => {
    showLoginMode();
  };

  initQueriesControls(() => {
    loadQueriesScreen();
  });

  const refreshBtn = document.getElementById("btn-refresh");
  if (refreshBtn) {
    refreshBtn.addEventListener("click", () => {
      loadActiveScreen();
    });
  }

  const logoutBtn = document.getElementById("btn-logout");
  if (logoutBtn) {
    logoutBtn.addEventListener("click", async () => {
      try {
        await apiLogout();
      } catch (_err) {
        showLoginMode();
      }
      showLoginMode();
    });
  }

  const loginForm = document.getElementById("login-form");
  if (loginForm) {
    loginForm.addEventListener("submit", async (ev) => {
      ev.preventDefault();
      const keyInput = document.getElementById("login-key");
      const errEl = document.getElementById("login-error");
      const submitBtn = document.getElementById("login-submit");
      if (!keyInput) return;
      const key = keyInput.value;
      keyInput.value = "";
      if (errEl) {
        errEl.textContent = "";
        errEl.hidden = true;
      }
      if (submitBtn) submitBtn.disabled = true;
      clearBanner();
      try {
        await apiLogin(key);
        loadActiveScreen();
      } catch (err) {
        if (err && err.code === "bad_key") {
          if (errEl) {
            errEl.textContent = "That key is not valid.";
            errEl.hidden = false;
          }
        } else {
          showErrorBanner(err, null);
        }
      } finally {
        if (submitBtn) submitBtn.disabled = false;
      }
    });
  }

  const rulesForm = document.getElementById("rules-form");
  if (rulesForm) {
    rulesForm.addEventListener("submit", (ev) => {
      ev.preventDefault();
      handleRuleSubmitOnRules("block");
    });
  }
  const blockBtn = document.getElementById("btn-rule-block");
  if (blockBtn) {
    blockBtn.addEventListener("click", () => handleRuleSubmitOnRules("block"));
  }
  const allowBtn = document.getElementById("btn-rule-allow");
  if (allowBtn) {
    allowBtn.addEventListener("click", () => handleRuleSubmitOnRules("allow"));
  }

  window.addEventListener("hashchange", handleRouteChange);
  handleRouteChange();
}

initApp();