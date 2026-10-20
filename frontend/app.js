import {
  apiAddRule,
  apiConnection,
  apiLogin,
  apiLogout,
  apiOps,
  apiQueries,
  apiRemoveRule,
  apiRules,
  apiStatus,
  mapRuleError,
  state
} from "./api.js";
import { renderConnectionView } from "./connection.js";
import { renderControlView, stopControlPolling } from "./control.js";
import { initQueriesControls, renderQueriesView } from "./queries.js";
import {
  clearBanner,
  parseHashRoute,
  renderSkeletonCards,
  setInteractiveDisabled,
  shellState,
  showErrorBanner,
  showLoginMode,
  showShellMode,
  showToast,
  syncServerTime,
  updateUpdatedMeta
} from "./shell.js";
import { renderRulesList, renderStatusView } from "./views.js";

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
    syncServerTime(data.time);
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
      syncServerTime(st.time);
    }
    const data = await apiQueries(state.queriesFilter);
    state.queriesData = data;
    syncServerTime(data.generated);
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
  if (!input || shellState.isLoading) return;
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
    syncServerTime(st.time);
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
  const container = document.getElementById("connection-content");
  clearBanner();
  setInteractiveDisabled(true);
  if (!state.connectionData && container) {
    renderSkeletonCards(container, 3);
  }
  try {
    const st = await apiStatus();
    state.statusData = st;
    syncServerTime(st.time);
    const data = await apiConnection();
    state.connectionData = data;
    updateUpdatedMeta(st.time, false);
    if (container) {
      renderConnectionView(container, data, () => loadConnectionScreen());
    }
  } catch (err) {
    if (err && err.status === 401) return;
    if (state.connectionData && container) {
      renderConnectionView(container, state.connectionData, () => loadConnectionScreen());
      if (state.statusData) updateUpdatedMeta(state.statusData.time, true);
    }
    showErrorBanner(err, () => loadConnectionScreen());
  } finally {
    setInteractiveDisabled(false);
  }
}

async function loadControlScreen(silent) {
  const container = document.getElementById("control-content");
  if (!silent) {
    clearBanner();
    setInteractiveDisabled(true);
    if (!state.controlData && container) {
      renderSkeletonCards(container, 3);
    }
  }
  try {
    const st = await apiStatus();
    state.statusData = st;
    syncServerTime(st.time);
    const data = await apiOps();
    state.controlData = data;
    updateUpdatedMeta(st.time, false);
    if (container && shellState.currentRoute === "control") {
      renderControlView(container, data, (quiet) => loadControlScreen(quiet));
    }
  } catch (err) {
    if (err && err.status === 401) return;
    if (silent) return;
    if (state.controlData && container) {
      renderControlView(container, state.controlData, (quiet) => loadControlScreen(quiet));
      if (state.statusData) updateUpdatedMeta(state.statusData.time, true);
    }
    showErrorBanner(err, () => loadControlScreen(false));
  } finally {
    if (!silent) setInteractiveDisabled(false);
  }
}

export function loadActiveScreen() {
  showShellMode();
  stopControlPolling();
  const route = shellState.currentRoute;
  if (route === "status") {
    loadStatusScreen();
  } else if (route === "queries") {
    loadQueriesScreen();
  } else if (route === "rules") {
    loadRulesScreen();
  } else if (route === "connection") {
    loadConnectionScreen();
  } else if (route === "control") {
    loadControlScreen(false);
  }
}

function handleRouteChange() {
  const next = parseHashRoute();
  if (window.location.hash !== "#/" + next) {
    window.history.replaceState(null, "", "#/" + next);
  }
  shellState.currentRoute = next;
  if (!shellState.isLoginVisible) {
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
      if (!window.confirm("Sign out?")) return;
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
