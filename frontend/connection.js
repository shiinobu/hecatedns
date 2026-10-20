import {
  apiRemoveConnection,
  apiRotateConnection,
  clearNode,
  createIcon,
  el,
  mapConnectionError,
  runProbeSequence,
  state
} from "./api.js";
import { showToast } from "./shell.js";

const TEST_FRESH_MS = 120000;
const MASK_BULLETS = "\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022";

let lastProbeRun = null;
let freshnessTimer = null;
let rotatedNewHost = null;
let isBusy = false;

function paintHost(line, host, revealed) {
  clearNode(line);
  const dot = host.indexOf(".");
  const label = dot > 0 ? host.slice(0, dot) : host;
  line.appendChild(el("span", revealed ? "host-label" : "host-mask", revealed ? label : MASK_BULLETS));
  if (dot > 0) line.appendChild(document.createTextNode(host.slice(dot)));
}

function setIcon(btn, name) {
  clearNode(btn);
  btn.appendChild(createIcon(name));
}

function iconButton(name, label) {
  const btn = el("button", "btn btn-soft btn-icon");
  btn.type = "button";
  btn.setAttribute("aria-label", label);
  setIcon(btn, name);
  return btn;
}

function computeProbeAdvice(results) {
  if (!Array.isArray(results) || results.length === 0) return "";
  if (results.every((r) => r.samples > 0 && r.resolved === r.samples)) {
    return "This network resolves every configured domain.";
  }
  const completeEntry = results.find((r) => r.samples > 0 && r.resolved === r.samples);
  const zeroEntry = results.find((r) => r.resolved === 0);
  if (completeEntry && zeroEntry) {
    return "Use " + completeEntry.domain + " on this network. " + zeroEntry.domain + " is not resolved here.";
  }
  if (results.some((r) => r.resolved > 0 && r.resolved < r.samples)) {
    return "Unstable: the resolver answers differently per lookup. Test again before changing anything.";
  }
  if (results.every((r) => r.resolved === 0)) {
    return "This network refuses hecate names. Turn Private DNS off here or use mobile data.";
  }
  return "";
}

function getDomainTestEligibility(domain) {
  if (!lastProbeRun || !lastProbeRun.finishedAtMs) {
    return { ok: false, reason: "Run the network test for this domain before rotating." };
  }
  if (Date.now() - lastProbeRun.finishedAtMs >= TEST_FRESH_MS) {
    return { ok: false, reason: "The last network test is over 120 s old. Run the test again." };
  }
  const entry = lastProbeRun.results.find((r) => r.domain === domain);
  if (!entry || entry.samples <= 0 || entry.resolved < entry.samples) {
    return { ok: false, reason: "The network test did not resolve all samples for this domain." };
  }
  return { ok: true, reason: "" };
}

function appendSection(container, title, card) {
  container.appendChild(el("h3", "section-label", title));
  container.appendChild(card);
}

function buildHostBlock(entry, onRemoveOld, extraNote, isBackup) {
  const secretHost = String((entry && entry.host) || "");
  const isCurrent = Boolean(entry && entry.current);
  let revealed = false;
  const expired = Boolean(entry && entry.expired);
  const row = el("div", expired ? "row row-flush row-block" : "row row-flush");
  const main = el("div", "row-main");
  const hostLine = el("span", "row-mono");
  paintHost(hostLine, secretHost, false);
  main.appendChild(hostLine);

  const meta = el("div", "row-meta");
  meta.appendChild(isCurrent ? el("span", "verdict verdict-pass", "Current") : el("span", "meta-tag meta-muted", "Not used"));
  if (expired) meta.appendChild(el("span", "verdict verdict-block", "Expired"));
  if (isBackup) meta.appendChild(el("span", "meta-tag meta-rule", "Backup"));
  if (entry && entry.source === "config") meta.appendChild(el("span", "meta-tag meta-muted", "Read-only"));
  if (entry && entry.added) meta.appendChild(el("span", "meta-tag meta-muted", "Added " + String(entry.added).slice(0, 10)));
  main.appendChild(meta);
  if (extraNote) main.appendChild(el("span", "card-text", extraNote));

  const copyErrEl = el("p", "field-error");
  copyErrEl.setAttribute("role", "alert");
  copyErrEl.hidden = true;
  main.appendChild(copyErrEl);
  row.appendChild(main);

  const side = el("div", "row-side");
  const revealBtn = iconButton("icon-eye", "Show hostname");
  revealBtn.setAttribute("aria-pressed", "false");
  revealBtn.addEventListener("click", () => {
    revealed = !revealed;
    revealBtn.setAttribute("aria-pressed", revealed ? "true" : "false");
    setIcon(revealBtn, revealed ? "icon-eye-off" : "icon-eye");
    paintHost(hostLine, secretHost, revealed);
  });
  side.appendChild(revealBtn);

  const copyBtn = iconButton("icon-copy", "Copy hostname");
  copyBtn.addEventListener("click", async () => {
    copyErrEl.hidden = true;
    copyErrEl.textContent = "";
    try {
      if (!navigator.clipboard || typeof navigator.clipboard.writeText !== "function") {
        throw new Error("no_clipboard");
      }
      await navigator.clipboard.writeText(secretHost);
      showToast("Hostname copied", null);
      setIcon(copyBtn, "icon-ok");
      setTimeout(() => setIcon(copyBtn, "icon-copy"), 1400);
    } catch (_err) {
      copyErrEl.textContent = "Copy failed. Reveal the name and copy it by hand.";
      copyErrEl.hidden = false;
    }
  });
  side.appendChild(copyBtn);

  if (entry && entry.removable && !entry.current && typeof onRemoveOld === "function") {
    const remBtn = el("button", "btn btn-text btn-soft", "Remove old");
    remBtn.type = "button";
    remBtn.disabled = isBusy;
    remBtn.addEventListener("click", () => {
      if (window.confirm("Remove this old hostname?")) onRemoveOld(secretHost);
    });
    side.appendChild(remBtn);
  }

  row.appendChild(side);
  return row;
}

const PROBE_LABELS = { pass: "OK", block: "Failed", warn: "Partial" };

function probeState(item) {
  if (item.samples > 0 && item.resolved === item.samples) return "pass";
  return item.resolved === 0 ? "block" : "warn";
}

function renderProbeResults(resultsContainer, adviceEl, results) {
  clearNode(resultsContainer);
  if (!Array.isArray(results) || results.length === 0) {
    resultsContainer.hidden = true;
    adviceEl.hidden = true;
    return;
  }
  resultsContainer.hidden = false;
  const states = results.map(probeState);
  results.forEach((item, idx) => {
    const row = el("div", "row row-flush row-" + states[idx]);
    const main = el("div", "row-main");
    main.appendChild(el("span", "row-mono", item.domain));
    main.appendChild(el("span", "row-meta", item.resolved + " of " + item.samples + " resolved"));
    row.appendChild(main);
    row.appendChild(el("span", "verdict verdict-" + states[idx], PROBE_LABELS[states[idx]]));
    resultsContainer.appendChild(row);
  });

  const advice = computeProbeAdvice(results);
  const overall = states.every((st) => st === "pass") ? "pass" : (states.every((st) => st === "block") ? "block" : "warn");
  adviceEl.className = "advice advice-" + overall;
  adviceEl.textContent = advice;
  adviceEl.hidden = !advice;
}

function buildRotateCard(domains, mainDomain, actionErrorEl, onRotated) {
  const rotCard = el("div", "card");

  const field = el("div", "field");
  const labelEl = el("label", "field-label", "Domain");
  labelEl.setAttribute("for", "rotate-domain");
  field.appendChild(labelEl);

  const selectEl = el("select", "field-input field-mono");
  selectEl.id = "rotate-domain";
  domains.forEach((d) => {
    const opt = el("option", "", d);
    opt.value = d;
    selectEl.appendChild(opt);
  });
  field.appendChild(selectEl);
  rotCard.appendChild(field);

  const guardReasonEl = el("p", "card-text");
  rotCard.appendChild(guardReasonEl);

  const rotActions = el("div", "card-actions");
  const rotBtn = el("button", "btn btn-allow", "Rotate");
  rotBtn.type = "button";
  const forceBtn = el("button", "btn btn-text", "Rotate anyway");
  forceBtn.type = "button";
  rotActions.appendChild(rotBtn);
  rotActions.appendChild(forceBtn);
  rotCard.appendChild(rotActions);
  rotCard.appendChild(actionErrorEl);

  const newHostSlot = el("div", "row-list");
  if (rotatedNewHost) {
    const note = "Type it into Settings, Private DNS. The old name keeps working until you remove it.";
    newHostSlot.appendChild(buildHostBlock({ host: rotatedNewHost, current: true, removable: false, source: "file" }, null, note));
  }
  rotCard.appendChild(newHostSlot);

  const syncGuard = () => {
    if (freshnessTimer) clearTimeout(freshnessTimer);
    freshnessTimer = null;
    const chosen = selectEl.value || mainDomain;
    const check = getDomainTestEligibility(chosen);
    rotBtn.disabled = !check.ok || isBusy;
    if (check.ok) {
      guardReasonEl.textContent = "Network test passed for " + chosen + ".";
      const remMs = Math.max(100, TEST_FRESH_MS - (Date.now() - lastProbeRun.finishedAtMs));
      freshnessTimer = setTimeout(syncGuard, remMs);
    } else {
      guardReasonEl.textContent = check.reason;
    }
  };

  const executeRotate = async (targetDomain) => {
    if (isBusy || !targetDomain) return;
    isBusy = true;
    rotBtn.disabled = true;
    forceBtn.disabled = true;
    actionErrorEl.hidden = true;
    try {
      const res = await apiRotateConnection(targetDomain);
      rotatedNewHost = res && res.host ? String(res.host) : null;
      isBusy = false;
      onRotated(res);
    } catch (err) {
      isBusy = false;
      forceBtn.disabled = false;
      syncGuard();
      if (err && err.status === 401) return;
      actionErrorEl.textContent = mapConnectionError(err);
      actionErrorEl.hidden = false;
    }
  };

  selectEl.addEventListener("change", syncGuard);
  rotBtn.addEventListener("click", () => executeRotate(selectEl.value || mainDomain));
  forceBtn.addEventListener("click", () => {
    const chosen = selectEl.value || mainDomain;
    if (window.confirm("Rotate the label for " + chosen + " without a recent passed network test?")) {
      executeRotate(chosen);
    }
  });

  syncGuard();
  return { card: rotCard, syncGuard };
}

export function renderConnectionView(container, data, onReload) {
  if (freshnessTimer) clearTimeout(freshnessTimer);
  freshnessTimer = null;
  clearNode(container);
  if (!data) return;

  const domains = Array.isArray(data.domains) ? data.domains : [];
  const mainDomain = domains[0] || "";
  const names = Array.isArray(data.names) ? data.names : [];
  const mainNames = mainDomain ? names.filter((n) => n && n.base === mainDomain) : names;
  const backupNames = mainDomain ? names.filter((n) => n && n.base !== mainDomain) : [];

  const actionErrorEl = el("p", "field-error");
  actionErrorEl.setAttribute("role", "alert");
  actionErrorEl.hidden = true;

  const applyNamesResult = (res) => {
    if (res && Array.isArray(res.names)) {
      state.connectionData = Object.assign({}, data, { names: res.names });
      renderConnectionView(container, state.connectionData, onReload);
    } else if (typeof onReload === "function") {
      onReload();
    }
  };

  const handleRemoveOld = async (secretHost) => {
    if (isBusy) return;
    isBusy = true;
    actionErrorEl.hidden = true;
    try {
      const res = await apiRemoveConnection(secretHost);
      isBusy = false;
      applyNamesResult(res);
    } catch (err) {
      isBusy = false;
      if (err && err.status === 401) return;
      actionErrorEl.textContent = mapConnectionError(err);
      actionErrorEl.hidden = false;
    }
  };

  const mainCard = el("div", "card card-flush");
  if (mainNames.length === 0) {
    mainCard.appendChild(el("p", "card-text", "No names from the file yet"));
  } else {
    const listWrap = el("div", "row-list");
    mainNames.forEach((entry) => listWrap.appendChild(buildHostBlock(entry, handleRemoveOld, "")));
    mainCard.appendChild(listWrap);
  }
  if (!data.rotation || !data.rotation.available) mainCard.appendChild(actionErrorEl);
  appendSection(container, "DoT hostname", mainCard);

  if (backupNames.length > 0) {
    const backupCard = el("div", "card card-flush panel-glass");
    const bList = el("div", "row-list");
    backupNames.forEach((entry) => bList.appendChild(buildHostBlock(entry, handleRemoveOld, "", true)));
    backupCard.appendChild(bList);
    backupCard.appendChild(el("p", "card-footer-meta", "Use this if the main domain is refused."));
    appendSection(container, "Backup domain", backupCard);
  }

  const probeCard = el("div", "card");
  const probeActions = el("div", "card-actions");
  const runBtn = el("button", "btn btn-primary btn-full", "Run test");
  runBtn.type = "button";
  probeActions.appendChild(runBtn);
  probeCard.appendChild(probeActions);

  const probeResultsWrap = el("div", "row-list result-list");
  probeResultsWrap.hidden = true;
  const probeAdviceEl = el("p", "advice");
  probeAdviceEl.hidden = true;
  probeCard.appendChild(probeResultsWrap);
  probeCard.appendChild(probeAdviceEl);
  const probeNote = "Shows what the browser resolves, which can differ from Android's system path if the browser's Secure DNS is on.";
  probeCard.appendChild(el("p", "card-footer-meta", probeNote));
  container.insertBefore(probeCard, container.firstChild);
  container.insertBefore(el("h3", "section-label", "Test this network"), probeCard);

  let updateRotateGuard = () => {};
  if (lastProbeRun && Array.isArray(lastProbeRun.results)) {
    renderProbeResults(probeResultsWrap, probeAdviceEl, lastProbeRun.results);
  }

  runBtn.addEventListener("click", async () => {
    if (isBusy) return;
    isBusy = true;
    runBtn.disabled = true;
    runBtn.classList.add("is-running");
    runBtn.textContent = "Testing...";
    updateRotateGuard();
    try {
      const results = await runProbeSequence(data.probe || {}, domains);
      lastProbeRun = { finishedAtMs: Date.now(), results };
      renderProbeResults(probeResultsWrap, probeAdviceEl, results);
    } finally {
      isBusy = false;
      runBtn.disabled = false;
      runBtn.classList.remove("is-running");
      runBtn.textContent = "Run test";
      updateRotateGuard();
    }
  });

  if (data.rotation && data.rotation.available) {
    const built = buildRotateCard(domains, mainDomain, actionErrorEl, applyNamesResult);
    updateRotateGuard = built.syncGuard;
    appendSection(container, "Rotate label", built.card);
  }
}
