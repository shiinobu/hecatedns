import {
  clearNode,
  el,
  formatBytes,
  formatDateShort,
  formatNumber,
  formatRelativeAge
} from "./api.js";

let gaugeHasAnimated = false;

const SCOPE_DAY = "day";
const SCOPE_LIFETIME = "lifetime";
let statsScope = SCOPE_DAY;

const CERT_WARN_DAYS = 14;
const LIST_STALE_HOURS = 72;

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

  const r = 74;
  const c = 2 * Math.PI * r;
  const track = document.createElementNS(ns, "circle");
  track.setAttribute("class", "gauge-track");
  track.setAttribute("cx", "100");
  track.setAttribute("cy", "100");
  track.setAttribute("r", String(r));
  svg.appendChild(track);

  const blockLen = total > 0 ? (blocked / total) * c : 0;
  const passLen = total > 0 ? (passed / total) * c : 0;
  const seamGap = blocked > 0 && passed > 0 ? 3.5 : 0;
  const blockDraw = Math.max(0, blockLen - seamGap);
  const passDraw = Math.max(0, passLen - seamGap);

  const passArc = document.createElementNS(ns, "circle");
  passArc.setAttribute("class", "gauge-arc");
  passArc.setAttribute("cx", "100");
  passArc.setAttribute("cy", "100");
  passArc.setAttribute("r", String(r));
  passArc.setAttribute("stroke", "url(#grad-cyan)");
  passArc.setAttribute("transform", "rotate(-90 100 100)");
  passArc.setAttribute("stroke-dashoffset", String(-(blockLen + seamGap / 2)));

  const blockArc = document.createElementNS(ns, "circle");
  blockArc.setAttribute("class", "gauge-arc");
  blockArc.setAttribute("cx", "100");
  blockArc.setAttribute("cy", "100");
  blockArc.setAttribute("r", String(r));
  blockArc.setAttribute("stroke", "url(#grad-block-arc)");
  blockArc.setAttribute("transform", "rotate(-90 100 100)");
  blockArc.setAttribute("stroke-dashoffset", String(-(seamGap / 2)));

  const targetBlockDash = blockDraw.toFixed(2) + " " + c.toFixed(2);
  const targetPassDash = passDraw.toFixed(2) + " " + c.toFixed(2);

  if (!gaugeHasAnimated) {
    blockArc.setAttribute("stroke-dasharray", "0 " + c.toFixed(2));
    passArc.setAttribute("stroke-dasharray", "0 " + c.toFixed(2));
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

function rowTone(tone) {
  return "row row-flush row-" + tone;
}

function lifetimeCounts(data) {
  const life = data.lifetime;
  return life && Number(life.total) > 0 && formatDateShort(life.since) ? life : null;
}

function buildScopeFooter(scope, counts) {
  const footer = el("p", "card-footer-meta");
  const text = scope === SCOPE_LIFETIME ? "Since " + formatDateShort(counts.since) : "Last 24 hours";
  footer.appendChild(document.createTextNode(text));
  if (counts.partial) {
    footer.appendChild(document.createTextNode(" "));
    footer.appendChild(el("span", "meta-tag meta-warn", "Partial count"));
  }
  return footer;
}

function buildScopeTabs(scope, onPick) {
  const group = el("div", "seg-group");
  group.dataset.count = "2";
  group.dataset.active = scope === SCOPE_LIFETIME ? "1" : "0";
  group.setAttribute("role", "group");
  group.setAttribute("aria-label", "Statistics range");
  const buttons = [];
  [
    [SCOPE_DAY, "24h"],
    [SCOPE_LIFETIME, "Lifetime"]
  ].forEach(([value, label], index) => {
    const btn = el("button", "seg-btn", label);
    btn.type = "button";
    btn.setAttribute("aria-pressed", value === scope ? "true" : "false");
    btn.addEventListener("click", () => {
      if (statsScope === value) return;
      statsScope = value;
      group.dataset.active = String(index);
      buttons.forEach((b) => b.setAttribute("aria-pressed", b === btn ? "true" : "false"));
      onPick(value);
    });
    buttons.push(btn);
    group.appendChild(btn);
  });
  const toolbar = el("div", "seg-toolbar");
  toolbar.appendChild(group);
  return toolbar;
}

function appendStatisticsSection(container, data) {
  container.appendChild(el("h3", "section-label", "Statistics"));
  const card = el("div", "card panel-glass");
  container.appendChild(card);
  if (!data.counts || !data.log_enabled) {
    card.appendChild(el("p", "card-text", "Query log is off"));
    return;
  }
  const life = lifetimeCounts(data);
  const countsOf = { [SCOPE_DAY]: data.counts, [SCOPE_LIFETIME]: life };
  const content = el("div");
  content.setAttribute("aria-live", "polite");
  const show = (scope) => {
    const counts = countsOf[scope];
    clearNode(content);
    content.appendChild(buildGaugeStage(counts));
    content.appendChild(buildScopeFooter(scope, counts));
  };
  const scope = life ? statsScope : SCOPE_DAY;
  if (life) card.appendChild(buildScopeTabs(scope, show));
  card.appendChild(content);
  show(scope);
}

function buildResolverRow(resolver) {
  const ok = Boolean(resolver && resolver.ok);
  const tone = ok ? "pass" : "block";
  const row = el("div", rowTone(tone));
  const main = el("div", "row-main");
  main.appendChild(el("span", "row-title", "Resolver"));
  main.appendChild(el("span", "row-mono", (resolver && resolver.name) || "--"));
  if (resolver && !ok && resolver.error) {
    main.appendChild(el("span", "field-error", resolver.error));
  }
  row.appendChild(main);

  const side = el("div", "row-side");
  if (ok) side.appendChild(el("span", "meta-tag meta-muted", resolver.ms + " ms"));
  side.appendChild(el("span", "verdict verdict-" + tone, ok ? "OK" : "Error"));
  row.appendChild(side);
  return row;
}

function certDays(certificate) {
  return Number(certificate.days_left) || 0;
}

function certTone(days) {
  if (days < 0) return "block";
  return days < CERT_WARN_DAYS ? "warn" : "pass";
}

function daysText(days) {
  return days + (days === 1 ? " day" : " days");
}

function buildCertificateRow(certificate) {
  const days = certDays(certificate);
  const expired = days < 0;
  const tone = certTone(days);
  const row = el("div", rowTone(tone));
  const main = el("div", "row-main");
  main.appendChild(el("span", "row-title", "Certificate"));
  main.appendChild(el("span", "row-meta", "Expires " + formatDateShort(certificate.expires)));
  row.appendChild(main);

  const side = el("div", "row-side");
  side.appendChild(el("span", "verdict verdict-" + tone, expired ? "Expired" : daysText(days) + " left"));
  row.appendChild(side);
  return row;
}

function buildRulesFileRow(rules) {
  const row = el("div", rowTone("pass"));
  const main = el("div", "row-main");
  main.appendChild(el("span", "row-title", "Rules file"));
  main.appendChild(el("span", "row-meta", formatBytes(rules.bytes) + " of " + formatBytes(rules.max_bytes)));
  row.appendChild(main);

  const side = el("div", "row-side");
  side.appendChild(el("span", "meta-tag meta-rule", formatNumber(rules.count) + " rules"));
  row.appendChild(side);
  return row;
}

function buildSystemCard(data) {
  const card = el("div", "card card-flush");
  const list = el("div", "row-list");
  list.appendChild(buildResolverRow(data.resolver));
  if (data.certificate !== null && data.certificate !== undefined) {
    list.appendChild(buildCertificateRow(data.certificate));
  }
  if (data.rules) list.appendChild(buildRulesFileRow(data.rules));
  card.appendChild(list);
  return card;
}

function listsOf(data) {
  return Array.isArray(data.lists) ? data.lists : [];
}

function listHealth(item, referenceTime) {
  if (item.state !== "ok") return { tone: "warn", text: item.state || "unknown", loaded: false, hours: 0 };
  const age = formatRelativeAge(item.updated, referenceTime);
  return { tone: age.hours > LIST_STALE_HOURS ? "warn" : "pass", text: age.text, loaded: true, hours: age.hours };
}

function buildListRow(item, referenceTime) {
  const health = listHealth(item, referenceTime);
  const row = el("div", rowTone(health.tone));
  const main = el("div", "row-main");
  main.appendChild(el("span", "row-mono", item.name || ""));
  if (health.loaded) {
    const meta = el("div", "row-meta");
    meta.appendChild(el("span", "meta-tag meta-list", formatNumber(item.domains) + " domains"));
    main.appendChild(meta);
  }
  row.appendChild(main);

  const side = el("div", "row-side");
  const warn = health.tone === "warn";
  side.appendChild(el("span", warn ? "verdict verdict-warn" : "meta-tag meta-muted", health.text));
  row.appendChild(side);
  return row;
}

function buildListsCard(data) {
  const card = el("div", "card card-flush");
  const wrap = el("div", "row-list");
  const lists = listsOf(data);
  if (lists.length === 0) {
    wrap.appendChild(el("p", "card-text", "No block lists configured."));
  } else {
    lists.forEach((item) => wrap.appendChild(buildListRow(item, data.time)));
  }
  card.appendChild(wrap);
  return card;
}

function collectIssues(data) {
  const issues = [];
  if (!data.resolver || !data.resolver.ok) issues.push({ tone: "block", text: "Resolver error" });
  if (data.certificate) {
    const days = certDays(data.certificate);
    const tone = certTone(days);
    if (tone !== "pass") {
      issues.push({ tone, text: days < 0 ? "Certificate expired" : "Certificate " + daysText(days) + " left" });
    }
  }
  listsOf(data).forEach((item) => {
    const health = listHealth(item, data.time);
    if (health.tone === "pass") return;
    const detail = health.loaded ? health.text.replace(" ago", " old") : health.text;
    issues.push({ tone: health.tone, text: item.name + " " + detail });
  });
  return issues;
}

function issuesTone(issues) {
  if (issues.length === 0) return "pass";
  return issues.some((issue) => issue.tone === "block") ? "block" : "warn";
}

function summaryTitle(count) {
  if (count === 0) return "All systems normal";
  return count === 1 ? "1 thing needs attention" : count + " things need attention";
}

function normalLine(data) {
  const parts = [];
  if (data.resolver && data.resolver.ok) parts.push("Resolver answers in " + data.resolver.ms + " ms.");
  if (data.certificate) parts.push("Certificate expires in " + daysText(certDays(data.certificate)) + ".");
  const loaded = listsOf(data).map((item) => listHealth(item, data.time)).filter((health) => health.loaded);
  if (loaded.length > 0) {
    const oldest = loaded.reduce((a, b) => (b.hours > a.hours ? b : a));
    parts.push("Oldest block list updated " + oldest.text + ".");
  }
  return parts.join(" ");
}

function buildSummary(data) {
  const issues = collectIssues(data);
  const tone = issuesTone(issues);
  const card = el("div", "sum verdict-" + tone);
  card.appendChild(el("div", "sum-head", summaryTitle(issues.length)));
  if (issues.length === 0) {
    card.appendChild(el("p", "card-text", normalLine(data)));
    return card;
  }
  const tags = el("div", "chip-row");
  issues.forEach((issue) => tags.appendChild(el("span", "verdict verdict-" + issue.tone, issue.text)));
  card.appendChild(tags);
  return card;
}

export function renderStatusView(container, data) {
  clearNode(container);
  if (!data) return;

  container.appendChild(buildSummary(data));
  appendStatisticsSection(container, data);
  container.appendChild(el("h3", "section-label", "System"));
  container.appendChild(buildSystemCard(data));
  container.appendChild(el("h3", "section-label", "Block lists"));
  container.appendChild(buildListsCard(data));
}

export function renderRulesList(listEl, summaryEl, data, onRemove, disabled) {
  clearNode(listEl);
  if (!data) return;

  const rules = Array.isArray(data.rules) ? data.rules : [];
  if (rules.length === 0) {
    listEl.appendChild(el("p", "card-text", "No custom rules yet"));
  } else {
    rules.forEach((rule) => {
      const isBlock = rule.action === "block";
      const row = el("div", isBlock ? "row row-flush row-block" : "row row-flush row-pass");
      const main = el("div", "row-main");
      main.appendChild(el("span", "row-mono", rule.domain));
      const meta = el("div", "row-meta");
      meta.appendChild(
        el("span", isBlock ? "verdict verdict-block" : "verdict verdict-pass", isBlock ? "Block" : "Allow")
      );
      main.appendChild(meta);
      row.appendChild(main);

      const side = el("div", "row-side");
      const remBtn = el("button", "btn btn-text btn-soft btn-row-action", "Remove");
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
