import { apiOps, apiRunOp, clearNode, el, formatBytes, formatDateShort, formatRelativeAge, state } from "./api.js";
import { shellState, showToast } from "./shell.js";

const POLL_MS = 2000;
const POLL_LIMIT_MS = 300000;
const SHORT_COOLDOWN_S = 60;

const CARDS = [
  {
    title: "Health",
    note: "Reads the server state. Nothing changes.",
    rows: [{ id: "health", name: "Health report", button: "Run" }]
  },
  {
    title: "Backup",
    note: "Archives hold the secret names and stay on the server.",
    rows: [{ id: "backup", name: "Configuration and rules", button: "Back up" }],
    backups: true
  },
  {
    title: "Certificate",
    note: "Renew only acts when 30 days or fewer remain. Force asks for a new one now.",
    rows: [
      { id: "renew-test", name: "Test the renewal path", button: "Test", hint: "Staging CA, nothing changes" },
      { id: "renew", name: "Renew if due", button: "Renew" },
      {
        id: "renew-force",
        name: "Force a renewal",
        button: "Force",
        confirm: "Request a new production certificate now? Let's Encrypt allows only a few per week."
      }
    ],
    days: true
  }
];

const openOutputs = new Map();

let pollTimer = null;
let pollSince = 0;

export function stopControlPolling() {
  if (pollTimer) {
    clearTimeout(pollTimer);
    pollTimer = null;
  }
}

function isPending(action) {
  if (action.state === "queued" || action.state === "running") return true;
  return action.cooldown_s > 0 && action.cooldown_s <= SHORT_COOLDOWN_S;
}

function schedulePoll(data, reload) {
  stopControlPolling();
  const pending = data.actions.some(isPending);
  if (!pending) {
    pollSince = 0;
    return;
  }
  if (!pollSince) pollSince = Date.now();
  if (Date.now() - pollSince > POLL_LIMIT_MS) return;
  pollTimer = setTimeout(() => {
    if (shellState.currentRoute === "control") reload(true);
  }, POLL_MS);
}

function waitText(seconds) {
  if (seconds < 90) return "Ready in " + seconds + " s";
  if (seconds < 5400) return "Ready in " + Math.ceil(seconds / 60) + " min";
  return "Ready in " + Math.ceil(seconds / 3600) + " h";
}

function stateTag(action) {
  if (action.state === "queued") return el("span", "meta-tag meta-muted", "Queued");
  if (action.state === "running") return el("span", "meta-tag meta-count", "Running");
  if (action.state === "ok") return el("span", "verdict", "OK");
  if (action.state === "failed") return el("span", "verdict verdict-block", "Failed");
  return el("span", "meta-tag meta-muted", "Not run yet");
}

function buildOutputPanel() {
  const panel = el("pre", "op-output");
  panel.hidden = true;
  return panel;
}

function showOutput(panel, button, text) {
  panel.textContent = text;
  panel.hidden = false;
  button.textContent = "Hide";
}

async function toggleOutput(id, finished, panel, button) {
  if (!panel.hidden) {
    panel.hidden = true;
    button.textContent = "Output";
    openOutputs.delete(id);
    return;
  }
  button.disabled = true;
  try {
    const data = await apiOps(id);
    const text = data.output && data.output.text ? data.output.text : "No output yet.";
    openOutputs.set(id, { finished, text });
    showOutput(panel, button, text);
  } catch (err) {
    if (err && err.status === 401) return;
    showToast(err.message, null);
  } finally {
    button.disabled = false;
  }
}

async function runAction(spec, reload) {
  if (spec.confirm && !window.confirm(spec.confirm)) return;
  try {
    await apiRunOp(spec.id);
    showToast(spec.name + " queued", null);
  } catch (err) {
    if (err && err.status === 401) return;
    showToast(err.message, null);
  }
  reload(true);
}

function buildActionRow(spec, action, reload) {
  const row = el("div", action.state === "failed" ? "row row-flush row-block" : "row row-flush");
  const main = el("div", "row-main");
  main.appendChild(el("span", "row-title", spec.name));
  const meta = el("div", "row-meta");
  meta.appendChild(stateTag(action));
  if (action.finished) meta.appendChild(el("span", "meta-tag meta-muted", formatRelativeAge(action.finished).text));
  if (action.cooldown_s > 0) meta.appendChild(el("span", "meta-tag meta-muted", waitText(action.cooldown_s)));
  if (spec.hint) meta.appendChild(el("span", "meta-tag meta-muted", spec.hint));
  main.appendChild(meta);
  const panel = buildOutputPanel();
  row.appendChild(main);

  const side = el("div", "row-side");
  if (action.finished) {
    const view = el("button", "btn btn-text btn-soft", "Output");
    view.type = "button";
    view.addEventListener("click", () => toggleOutput(spec.id, action.finished, panel, view));
    const kept = openOutputs.get(spec.id);
    if (kept && kept.finished === action.finished) showOutput(panel, view, kept.text);
    side.appendChild(view);
  }
  const run = el("button", "btn btn-soft", spec.button);
  run.type = "button";
  run.disabled = action.state === "queued" || action.state === "running" || action.cooldown_s > 0;
  run.addEventListener("click", () => runAction(spec, reload));
  side.appendChild(run);
  row.appendChild(side);
  row.appendChild(panel);
  return row;
}

function buildBackupRows(backups) {
  const wrap = el("div", "row-list");
  if (backups.length === 0) {
    wrap.appendChild(el("p", "card-text", "No archives yet."));
    return wrap;
  }
  backups.forEach((backup) => {
    const row = el("div", "row row-flush");
    const main = el("div", "row-main");
    main.appendChild(el("span", "row-mono", backup.name));
    const meta = el("div", "row-meta");
    meta.appendChild(el("span", "meta-tag meta-muted", formatBytes(backup.bytes)));
    meta.appendChild(el("span", "meta-tag meta-muted", formatDateShort(backup.time)));
    main.appendChild(meta);
    row.appendChild(main);
    wrap.appendChild(row);
  });
  return wrap;
}

function certificateRow() {
  const cert = state.statusData && state.statusData.certificate;
  if (!cert) return null;
  const days = Number(cert.days_left) || 0;
  const row = el("div", days < 14 ? "row row-flush row-warn" : "row row-flush");
  const main = el("div", "row-main");
  main.appendChild(el("span", "row-title", "Certificate"));
  const meta = el("div", "row-meta");
  meta.appendChild(el("span", "meta-tag meta-muted", days < 0 ? "Expired" : days + " days left"));
  main.appendChild(meta);
  row.appendChild(main);
  return row;
}

function buildCard(card, data, reload) {
  const frame = el("div", "card card-flush");
  const list = el("div", "row-list");
  if (card.days) {
    const cert = certificateRow();
    if (cert) list.appendChild(cert);
  }
  card.rows.forEach((spec) => {
    const action = data.actions.find((a) => a.action === spec.id);
    if (action) list.appendChild(buildActionRow(spec, action, reload));
  });
  frame.appendChild(list);
  if (card.backups) frame.appendChild(buildBackupRows(data.backups));
  frame.appendChild(el("p", "card-footer-meta", card.note));
  return frame;
}

export function renderControlView(container, data, reload) {
  clearNode(container);
  if (!data.enabled) {
    const card = el("div", "card card-flush");
    card.appendChild(el("p", "card-text", "Operations are not available on this host."));
    container.appendChild(card);
    stopControlPolling();
    return;
  }
  CARDS.forEach((card) => {
    container.appendChild(el("h3", "section-label", card.title));
    container.appendChild(buildCard(card, data, reload));
  });
  schedulePoll(data, reload);
}
