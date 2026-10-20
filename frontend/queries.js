import {
  apiAddRule,
  apiRemoveRule,
  clearNode,
  createIcon,
  el,
  formatTime24,
  mapRuleError,
  state
} from "./api.js";
import { showToast } from "./views.js";

let searchTimer = null;

export function initQueriesControls(onFilterChange) {
  const view = document.getElementById("view-queries");
  if (!view) return;

  const winButtons = view.querySelectorAll("[data-window]");
  winButtons.forEach((btn) => {
    btn.addEventListener("click", () => {
      const val = btn.getAttribute("data-window") || "15m";
      if (state.queriesFilter.window === val) return;
      state.queriesFilter.window = val;
      winButtons.forEach((b) => {
        b.setAttribute("aria-pressed", b === btn ? "true" : "false");
      });
      onFilterChange();
    });
  });

  const decButtons = view.querySelectorAll("[data-decision]");
  decButtons.forEach((btn) => {
    btn.addEventListener("click", () => {
      const val = btn.getAttribute("data-decision") || "all";
      if (state.queriesFilter.decision === val) return;
      state.queriesFilter.decision = val;
      decButtons.forEach((b) => {
        b.setAttribute("aria-pressed", b === btn ? "true" : "false");
      });
      onFilterChange();
    });
  });

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

  if (rows.length === 0) {
    const emptyCard = el("div", "card");
    emptyCard.appendChild(el("p", "card-text", "No queries in the last " + winLabel));
    listEl.appendChild(emptyCard);
    return;
  }

  const card = el("div", "card card-inset");
  const rowList = el("div", "row-list");

  rows.forEach((row, rowIdx) => {
    const item = el("div", "row row-query");
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
    const isBlocked = row.decision === "block";
    const pill = el(
      "span",
      isBlocked ? "chip chip-badge chip-block" : "chip chip-badge chip-allow"
    );
    pill.appendChild(createIcon(isBlocked ? "icon-block" : "icon-allow"));
    pill.appendChild(el("span", "", isBlocked ? "Blocked" : "Passed"));
    meta.appendChild(pill);

    meta.appendChild(el("span", "", "x" + (row.count || 0) + " - " + formatTime24(row.last)));

    if (row.by === "rule") {
      meta.appendChild(el("span", "", "custom rule"));
    } else if (row.by === "list" && row.list) {
      meta.appendChild(el("span", "", row.list));
    }
    main.appendChild(meta);
    mainBar.appendChild(main);

    const side = el("div", "row-side");
    if (row.by === "rule") {
      const btn = el("button", "btn btn-text", "Remove rule");
      btn.type = "button";
      btn.disabled = Boolean(disabled);
      btn.addEventListener("click", () => {
        handleQueryRuleRemove(row.name, reloadQueries);
      });
      side.appendChild(btn);
    } else if (row.decision === "pass" && !row.by) {
      const btn = el("button", "btn btn-block");
      btn.type = "button";
      btn.disabled = Boolean(disabled);
      btn.appendChild(createIcon("icon-block"));
      btn.appendChild(el("span", "", "Block"));
      btn.addEventListener("click", () => {
        handleQueryRuleAction("block", selectedDomain, reloadQueries);
      });
      side.appendChild(btn);
    } else if (row.decision === "block" && row.by === "list") {
      const btn = el("button", "btn btn-allow");
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