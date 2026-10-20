import {
  apiAddRule,
  apiRemoveRule,
  clearNode,
  createIcon,
  el,
  formatTime24,
  formatNumber,
  mapRuleError,
  state
} from "./api.js";
import { showToast } from "./shell.js";

let searchTimer = null;

function bindSegment(view, attr, key, fallback, onFilterChange) {
  const buttons = Array.from(view.querySelectorAll("[" + attr + "]"));
  if (buttons.length === 0) return;
  const group = buttons[0].parentElement;

  const setActive = (btn) => {
    buttons.forEach((b) => {
      b.setAttribute("aria-pressed", b === btn ? "true" : "false");
    });
    group.dataset.active = String(buttons.indexOf(btn));
  };

  const current = buttons.find((b) => b.getAttribute(attr) === state.queriesFilter[key]);
  setActive(current || buttons[0]);

  buttons.forEach((btn) => {
    btn.addEventListener("click", () => {
      const val = btn.getAttribute(attr) || fallback;
      if (state.queriesFilter[key] === val) return;
      state.queriesFilter[key] = val;
      setActive(btn);
      onFilterChange();
    });
  });
}

function updatePager(total, pages) {
  const page = state.queriesFilter.page;
  const label = document.getElementById("queries-page");
  const count = document.getElementById("queries-total");
  const prev = document.getElementById("queries-prev");
  const next = document.getElementById("queries-next");
  if (label) label.textContent = "Page " + (page + 1) + " of " + pages;
  if (count) count.textContent = formatNumber(total) + " results";
  if (prev) prev.disabled = page <= 0;
  if (next) next.disabled = page >= pages - 1;
}

export function initQueriesControls(onReload) {
  const view = document.getElementById("view-queries");
  if (!view) return;

  const onFilterChange = () => {
    state.queriesFilter.page = 0;
    onReload();
  };

  bindSegment(view, "data-window", "window", "15m", onFilterChange);
  bindSegment(view, "data-decision", "decision", "all", onFilterChange);
  bindSegment(view, "data-size", "pageSize", "25", onFilterChange);

  const turnPage = (step) => () => {
    state.queriesFilter.page = Math.max(0, state.queriesFilter.page + step);
    onReload();
  };
  const prevBtn = document.getElementById("queries-prev");
  const nextBtn = document.getElementById("queries-next");
  if (prevBtn) prevBtn.addEventListener("click", turnPage(-1));
  if (nextBtn) nextBtn.addEventListener("click", turnPage(1));

  const searchInput = document.getElementById("queries-search");
  if (searchInput) {
    const applySearch = () => {
      if (searchTimer) {
        clearTimeout(searchTimer);
        searchTimer = null;
      }
      const nextQ = searchInput.value.trim().slice(0, 100);
      if (state.queriesFilter.q !== nextQ) {
        state.queriesFilter.q = nextQ;
        onFilterChange();
      }
    };

    searchInput.addEventListener("input", () => {
      if (searchTimer) clearTimeout(searchTimer);
      searchTimer = setTimeout(applySearch, 400);
    });

    searchInput.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter") {
        ev.preventDefault();
        applySearch();
      }
    });

    const clearBtn = document.getElementById("queries-clear");
    if (clearBtn) {
      clearBtn.addEventListener("click", () => {
        searchInput.value = "";
        applySearch();
        searchInput.focus();
      });
    }
  }
}

function setQueriesInlineError(text) {
  const errEl = document.getElementById("queries-error");
  if (!errEl) return;
  if (!text) {
    errEl.textContent = "";
    errEl.hidden = true;
  } else {
    errEl.textContent = text;
    errEl.hidden = false;
  }
}

async function handleQueryRuleAction(action, domain, reloadQueries) {
  setQueriesInlineError("");
  try {
    const res = await apiAddRule(action, domain);
    const added = res && res.rule ? res.rule : { action, domain };
    const replaced = res ? res.replaced : null;
    showToast("Rule added: " + added.action + " " + added.domain, async () => {
      try {
        if (replaced === null || replaced === undefined) {
          await apiRemoveRule(added.domain);
        } else {
          await apiAddRule(replaced.action, replaced.domain);
        }
        reloadQueries();
      } catch (undoErr) {
        setQueriesInlineError(mapRuleError(undoErr));
      }
    });
    reloadQueries();
  } catch (err) {
    setQueriesInlineError(mapRuleError(err));
  }
}

async function handleQueryRuleRemove(domain, reloadQueries) {
  setQueriesInlineError("");
  try {
    const res = await apiRemoveRule(domain);
    const removed = res && res.removed ? res.removed : { action: "block", domain };
    showToast("Rule removed: " + removed.domain, async () => {
      try {
        await apiAddRule(removed.action, removed.domain);
        reloadQueries();
      } catch (undoErr) {
        setQueriesInlineError(mapRuleError(undoErr));
      }
    });
    reloadQueries();
  } catch (err) {
    setQueriesInlineError(mapRuleError(err));
  }
}

export function renderQueriesView(data, reloadQueries, disabled) {
  const listEl = document.getElementById("queries-list");
  const partialEl = document.getElementById("queries-partial");
  if (!listEl) return;

  setQueriesInlineError("");
  clearNode(listEl);

  if (partialEl) {
    partialEl.hidden = !(data && data.partial);
  }

  if (!data) return;
  const rows = Array.isArray(data.rows) ? data.rows : [];
  const winLabel = data.window || state.queriesFilter.window || "15m";
  const size = Number(state.queriesFilter.pageSize) || 25;
  const total = Number.isFinite(data.total) ? data.total : rows.length;
  const pages = Math.max(1, Math.ceil(total / size));
  if (rows.length === 0 && total > 0 && state.queriesFilter.page > 0) {
    state.queriesFilter.page = pages - 1;
    reloadQueries();
    return;
  }
  updatePager(total, pages);
  listEl.scrollTop = 0;

  if (rows.length === 0) {
    const emptyCard = el("div", "card");
    emptyCard.appendChild(el("p", "card-text", "No queries in the last " + winLabel));
    listEl.appendChild(emptyCard);
    return;
  }

  const card = el("div", "card card-flush");
  const rowList = el("div", "row-list");

  rows.forEach((row, rowIdx) => {
    const isBlocked = row.decision === "block";
    const item = el("div", isBlocked ? "row row-query row-flush row-block" : "row row-query row-flush row-pass");
    let selectedDomain = row.name || "";
    const parents = Array.isArray(row.parents) ? row.parents : [];
    const hasParents = parents.length > 0;
    const drawerId = "query-drawer-" + rowIdx;

    const mainBar = el("div", "query-main-bar");
    const main = hasParents
      ? el("button", "row-main query-toggle")
      : el("div", "row-main");

    if (hasParents) {
      main.type = "button";
      main.setAttribute("aria-expanded", "false");
      main.setAttribute("aria-controls", drawerId);
    }

    main.appendChild(el("span", "row-mono", row.name || ""));

    const meta = el("div", "row-meta");
    meta.appendChild(
      el(
        "span",
        isBlocked ? "verdict verdict-block" : "verdict verdict-pass",
        isBlocked ? "Blocked" : "Passed"
      )
    );
    meta.appendChild(el("span", "meta-tag meta-count", String(row.count || 0)));
    meta.appendChild(el("span", "meta-time", formatTime24(row.last)));

    if (row.by === "rule") {
      meta.appendChild(el("span", "meta-tag meta-rule", "custom rule"));
    } else if (row.by === "list" && row.list) {
      meta.appendChild(el("span", "meta-tag meta-list", row.list));
    }
    main.appendChild(meta);
    mainBar.appendChild(main);

    const side = el("div", "row-side");
    if (row.by === "rule") {
      const btn = el("button", "btn btn-text btn-soft btn-row-action", "Remove rule");
      btn.type = "button";
      btn.disabled = Boolean(disabled);
      btn.addEventListener("click", () => {
        handleQueryRuleRemove(row.name, reloadQueries);
      });
      side.appendChild(btn);
    } else if (row.decision === "pass" && !row.by) {
      const btn = el("button", "btn btn-block btn-soft btn-row-action");
      btn.type = "button";
      btn.disabled = Boolean(disabled);
      btn.appendChild(createIcon("icon-block"));
      btn.appendChild(el("span", "", "Block"));
      btn.addEventListener("click", () => {
        handleQueryRuleAction("block", selectedDomain, reloadQueries);
      });
      side.appendChild(btn);
    } else if (row.decision === "block" && row.by === "list") {
      const btn = el("button", "btn btn-allow btn-soft btn-row-action");
      btn.type = "button";
      btn.disabled = Boolean(disabled);
      btn.appendChild(createIcon("icon-allow"));
      btn.appendChild(el("span", "", "Allow"));
      btn.addEventListener("click", () => {
        handleQueryRuleAction("allow", selectedDomain, reloadQueries);
      });
      side.appendChild(btn);
    }
    mainBar.appendChild(side);
    item.appendChild(mainBar);

    if (hasParents) {
      const drawer = el("div", "query-drawer");
      drawer.id = drawerId;
      drawer.hidden = true;
      drawer.appendChild(el("span", "drawer-label", "Rule for"));
      const chipRow = el("div", "chip-row");
      const candidates = [row.name].concat(parents);
      const chipBtns = [];

      candidates.forEach((cand, idx) => {
        const cBtn = el("button", "chip field-mono", cand);
        cBtn.type = "button";
        cBtn.disabled = Boolean(disabled);
        cBtn.setAttribute("aria-pressed", idx === 0 ? "true" : "false");
        cBtn.addEventListener("click", () => {
          selectedDomain = cand;
          chipBtns.forEach((b) => {
            b.setAttribute("aria-pressed", b === cBtn ? "true" : "false");
          });
        });
        chipBtns.push(cBtn);
        chipRow.appendChild(cBtn);
      });

      drawer.appendChild(chipRow);
      item.appendChild(drawer);

      main.addEventListener("click", () => {
        drawer.hidden = !drawer.hidden;
        main.setAttribute("aria-expanded", drawer.hidden ? "false" : "true");
      });
    }

    rowList.appendChild(item);
  });

  card.appendChild(rowList);
  listEl.appendChild(card);
}