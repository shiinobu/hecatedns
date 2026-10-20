import {
  clearNode,
  createIcon,
  el,
  formatBytes,
  formatDateShort,
  formatNumber,
  formatRelativeAge
} from "./api.js";

let toastTimer = null;
let rateLimitTimer = null;
let gaugeHasAnimated = false;

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

function buildGaugeStage(counts) {
  const total = Number(counts.total) || 0;
  const blocked = Number(counts.blocked) || 0;
  const passed = Number(counts.passed) || 0;
  const pct = total > 0 ? Math.round((blocked * 100) / total) : 0;

  const stage = el("div", "gauge-stage");
  const wrap = el("div", "gauge-wrap");
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("class", "gauge-svg");
  svg.setAttribute("viewBox", "0 0 200 200");
  svg.setAttribute("aria-hidden", "true");

  const c = 490;
  const track = document.createElementNS(ns, "circle");
  track.setAttribute("class", "gauge-track");
  track.setAttribute("cx", "100");
  track.setAttribute("cy", "100");
  track.setAttribute("r", "78");
  track.setAttribute("fill", "none");
  track.setAttribute("stroke-width", "12");
  svg.appendChild(track);

  const blockLen = total > 0 ? (blocked / total) * c : 0;
  const passLen = total > 0 ? (passed / total) * c : 0;

  const passArc = document.createElementNS(ns, "circle");
  passArc.setAttribute("class", "gauge-arc");
  passArc.setAttribute("cx", "100");
  passArc.setAttribute("cy", "100");
  passArc.setAttribute("r", "78");
  passArc.setAttribute("fill", "none");
  passArc.setAttribute("stroke", "url(#grad-cyan)");
  passArc.setAttribute("stroke-width", "10");
  passArc.setAttribute("stroke-linecap", "round");
  passArc.setAttribute("transform", "rotate(-90 100 100)");
  passArc.setAttribute("stroke-dashoffset", String(-blockLen));

  const blockArc = document.createElementNS(ns, "circle");
  blockArc.setAttribute("class", "gauge-arc");
  blockArc.setAttribute("cx", "100");
  blockArc.setAttribute("cy", "100");
  blockArc.setAttribute("r", "78");
  blockArc.setAttribute("fill", "none");
  blockArc.setAttribute("stroke", "url(#grad-block-arc)");
  blockArc.setAttribute("stroke-width", "14");
  blockArc.setAttribute("stroke-linecap", "round");
  blockArc.setAttribute("transform", "rotate(-90 100 100)");

  const targetBlockDash = blockLen.toFixed(1) + " " + c;
  const targetPassDash = passLen.toFixed(1) + " " + c;

  if (!gaugeHasAnimated) {
    blockArc.setAttribute("stroke-dasharray", "0 " + c);
    passArc.setAttribute("stroke-dasharray", "0 " + c);
    gaugeHasAnimated = true;
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        blockArc.setAttribute("stroke-dasharray", targetBlockDash);
        passArc.setAttribute("stroke-dasharray", targetPassDash);
      });
    });
  } else {
    blockArc.setAttribute("stroke-dasharray", targetBlockDash);
    passArc.setAttribute("stroke-dasharray", targetPassDash);
  }

  svg.appendChild(passArc);
  svg.appendChild(blockArc);
  wrap.appendChild(svg);

  const center = el("div", "gauge-center");
  center.appendChild(el("span", "gauge-pct", pct + "%"));
  center.appendChild(el("span", "gauge-sub", "Blocked"));
  wrap.appendChild(center);
  stage.appendChild(wrap);

  const strip = el("div", "metric-strip");
  const cells = [
    ["Total", formatNumber(total)],
    ["Blocked", formatNumber(blocked)],
    ["Passed", formatNumber(passed)]
  ];
  cells.forEach(([lbl, val]) => {
    const cell = el("div", "metric-cell");
    cell.appendChild(el("span", "metric-val", val));
    cell.appendChild(el("span", "metric-lbl", lbl));
    strip.appendChild(cell);
  });
  stage.appendChild(strip);

  return stage;
}

export function renderStatusView(container, data) {
  clearNode(container);
  if (!data) return;

  const countsCard = el("div", "card");
  const countsHead = el("div", "card-header");
  countsHead.appendChild(el("h3", "card-title", "Last 24 hours"));
  if (data.counts && data.counts.partial) {
    const partChip = el("span", "chip chip-badge chip-warn");
    partChip.appendChild(createIcon("icon-warn"));
    partChip.appendChild(el("span", "", "Partial count"));
    countsHead.appendChild(partChip);
  }
  countsCard.appendChild(countsHead);

  if (!data.counts || !data.log_enabled) {
    countsCard.appendChild(el("p", "card-text", "Query log is off"));
  } else {
    countsCard.appendChild(buildGaugeStage(data.counts));
  }
  container.appendChild(countsCard);

  const sysCard = el("div", "card card-inset");
  const sysList = el("div", "row-list");

  const resRow = el("div", "row");
  const resMain = el("div", "row-main");
  resMain.appendChild(el("span", "row-title", "Resolver"));
  resMain.appendChild(el("span", "row-mono", (data.resolver && data.resolver.name) || "--"));
  if (data.resolver && !data.resolver.ok && data.resolver.error) {
    resMain.appendChild(el("span", "field-error", data.resolver.error));
  }
  resRow.appendChild(resMain);
  const resSide = el("div", "row-side");
  const resOk = Boolean(data.resolver && data.resolver.ok);
  if (resOk && data.resolver) {
    resSide.appendChild(el("span", "row-meta", data.resolver.ms + " ms"));
  }
  const resChip = el("span", resOk ? "chip chip-badge chip-allow" : "chip chip-badge chip-block");
  resChip.appendChild(createIcon(resOk ? "icon-ok" : "icon-warn"));
  resChip.appendChild(el("span", "", resOk ? "OK" : "Error"));
  resSide.appendChild(resChip);
  resRow.appendChild(resSide);
  sysList.appendChild(resRow);

  if (data.certificate !== null && data.certificate !== undefined) {
    const certRow = el("div", "row");
    const certMain = el("div", "row-main");
    certMain.appendChild(el("span", "row-title", "Certificate"));
    certMain.appendChild(el("span", "row-meta", "Expires " + formatDateShort(data.certificate.expires)));
    certRow.appendChild(certMain);

    const certSide = el("div", "row-side");
    const days = Number(data.certificate.days_left) || 0;
    const isWarn = days < 14;
    const certChip = el("span", isWarn ? "chip chip-badge chip-warn" : "chip chip-badge chip-allow");
    certChip.appendChild(createIcon(isWarn ? "icon-warn" : "icon-ok"));
    certChip.appendChild(el("span", "", days + " days left"));
    certSide.appendChild(certChip);
    certRow.appendChild(certSide);
    sysList.appendChild(certRow);
  }

  if (data.rules) {
    const rulesRow = el("div", "row");
    const rulesMain = el("div", "row-main");
    rulesMain.appendChild(el("span", "row-title", "Rules file"));
    rulesMain.appendChild(
      el("span", "row-meta", formatBytes(data.rules.bytes) + " of " + formatBytes(data.rules.max_bytes))
    );
    rulesRow.appendChild(rulesMain);
    rulesRow.appendChild(el("div", "row-side", formatNumber(data.rules.count) + " rules"));
    sysList.appendChild(rulesRow);
  }

  sysCard.appendChild(sysList);
  container.appendChild(sysCard);

  const listCard = el("div", "card");
  listCard.appendChild(el("h3", "card-title", "Block lists"));
  const listWrap = el("div", "row-list");
  const lists = Array.isArray(data.lists) ? data.lists : [];
  if (lists.length === 0) {
    listWrap.appendChild(el("p", "card-text", "No block lists configured."));
  } else {
    lists.forEach((item) => {
      const row = el("div", "row");
      const main = el("div", "row-main");
      main.appendChild(el("span", "row-mono", item.name || ""));
      if (item.state === "ok") {
        main.appendChild(el("span", "row-meta", formatNumber(item.domains) + " domains"));
      }
      row.appendChild(main);

      const side = el("div", "row-side");
      if (item.state === "ok") {
        const age = formatRelativeAge(item.updated, data.time);
        const staleList = age.hours > 72;
        const ageChip = el("span", staleList ? "chip chip-badge chip-warn" : "chip chip-badge");
        if (staleList) ageChip.appendChild(createIcon("icon-warn"));
        ageChip.appendChild(el("span", "", age.text));
        side.appendChild(ageChip);
      } else {
        const stateChip = el("span", "chip chip-badge chip-warn");
        stateChip.appendChild(createIcon("icon-warn"));
        stateChip.appendChild(el("span", "", item.state || "unknown"));
        side.appendChild(stateChip);
      }
      row.appendChild(side);
      listWrap.appendChild(row);
    });
  }
  listCard.appendChild(listWrap);
  container.appendChild(listCard);
}

export function renderRulesList(listEl, summaryEl, data, onRemove, disabled) {
  clearNode(listEl);
  if (!data) return;

  const rules = Array.isArray(data.rules) ? data.rules : [];
  if (rules.length === 0) {
    listEl.appendChild(el("p", "card-text", "No custom rules yet"));
  } else {
    rules.forEach((rule) => {
      const row = el("div", "row");
      const main = el("div", "row-main");
      main.appendChild(el("span", "row-mono", rule.domain));
      const meta = el("div", "row-meta");
      const isBlock = rule.action === "block";
      const pill = el("span", isBlock ? "chip chip-badge chip-block" : "chip chip-badge chip-allow");
      pill.appendChild(createIcon(isBlock ? "icon-block" : "icon-allow"));
      pill.appendChild(el("span", "", rule.action));
      meta.appendChild(pill);
      main.appendChild(meta);
      row.appendChild(main);

      const side = el("div", "row-side");
      const remBtn = el("button", "btn btn-text", "Remove");
      remBtn.type = "button";
      remBtn.disabled = Boolean(disabled);
      remBtn.addEventListener("click", () => {
        onRemove(rule.domain);
      });
      side.appendChild(remBtn);
      row.appendChild(side);
      listEl.appendChild(row);
    });
  }

  if (summaryEl) {
    summaryEl.textContent =
      formatNumber(data.count) +
      " rules - " +
      formatBytes(data.bytes) +
      " of " +
      formatBytes(data.max_bytes);
  }
}