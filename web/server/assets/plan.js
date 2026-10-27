// The plan page's filters. Without this script every knot shows, and the
// trees still open and close, because they are details elements.
(function () {
  "use strict";
  const controls = document.getElementById("filters");
  const box = document.getElementById("milestones");
  if (!controls || !box) return;
  const buttons = Array.from(controls.querySelectorAll("button"));
  const showDone = document.getElementById("show-done");
  let filter = "all";
  try {
    filter = localStorage.getItem("valley-filter") || "all";
    showDone.checked = localStorage.getItem("valley-done") === "1";
  } catch (e) {}
  if (!buttons.some(function (b) { return b.dataset.filter === filter; })) filter = "all";

  function matches(li) {
    const s = li.dataset.state;
    if (s === "done" && !showDone.checked) return false;
    return filter === "all" || s === filter;
  }
  // A knot stays in view when it matches, or when a knot under it does.
  function apply() {
    buttons.forEach(function (b) { b.setAttribute("aria-pressed", String(b.dataset.filter === filter)); });
    const kept = new Set();
    Array.from(box.querySelectorAll("li")).reverse().forEach(function (li) {
      const kids = li.querySelectorAll(":scope > details > ul > li, :scope > ul > li");
      if (matches(li) || Array.from(kids).some(function (c) { return kept.has(c); })) kept.add(li);
      li.hidden = !kept.has(li);
    });
    box.querySelectorAll("article").forEach(function (a) {
      const shown = Array.from(a.querySelectorAll(":scope > ul > li")).some(function (li) { return !li.hidden; });
      a.hidden = !shown && filter !== "all";
    });
  }
  buttons.forEach(function (b) {
    b.addEventListener("click", function () {
      filter = b.dataset.filter;
      try { localStorage.setItem("valley-filter", filter); } catch (e) {}
      apply();
    });
  });
  showDone.addEventListener("change", function () {
    try { localStorage.setItem("valley-done", showDone.checked ? "1" : "0"); } catch (e) {}
    apply();
  });
  controls.hidden = false;
  apply();
})();
