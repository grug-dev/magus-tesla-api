// app.js — the gateway's only hand-written client script. Keep it tiny: htmx does
// the work, this file only teaches htmx the one thing it cannot know on its own.
//
// Why this exists
// ---------------
// htmx's default config is
//   responseHandling: [{code:"204",swap:false},{code:"[23]..",swap:true},
//                      {code:"[45]..",swap:false,error:true}]
// so a 4xx/5xx response body is NEVER swapped into the DOM. That default is right
// in general (you don't want a proxy's 502 page swapped into a table row), but it
// silently discards the error fragments our handlers deliberately render — a form
// re-rendered with per-field validation messages, an error row, etc. The result was
// a PUT that returned 422 with a fully-rendered form underneath it and NOTHING
// appearing on screen.
//
// HX-Reswap cannot fix this: it only overrides the swap STYLE (swapOverride). The
// shouldSwap decision comes from responseHandling alone and is mutable only here,
// from an htmx:beforeSwap listener.
//
// So handlers opt a response in by setting the HX-Error-Fragment header (see
// renderError / renderFragmentError in internal/gateway/handlers/handlers.go), and
// this listener honours it. Opt-in per response, never a blanket "swap all 4xx".
document.body.addEventListener("htmx:beforeSwap", function (evt) {
  if (evt.detail.xhr && evt.detail.xhr.getResponseHeader("HX-Error-Fragment") === "true") {
    // Swap the fragment in so the user actually sees the validation messages, and
    // clear isError so an expected validation failure doesn't log as a JS error.
    evt.detail.shouldSwap = true;
    evt.detail.isError = false;
  }
});

// --- Confirmation modal: replace window.confirm() app-wide --------------------
//
// htmx fires a CANCELABLE `htmx:confirm` event before every request, carrying the
// element's hx-confirm text as detail.question and a detail.issueRequest(skip)
// callback. Its internal default is `if (question && !skip) { if (!confirm(question)) return }`
// — the browser's native, unstyleable dialog. Preventing the event and calling
// issueRequest(true) hands us the decision instead, with htmx resuming the exact
// same request afterwards.
//
// Because this hooks htmx's own event rather than any page's markup, EVERY element
// carrying hx-confirm — on any page, including ones not written yet — gets the
// shared ui.ConfirmDialog. Per-use content comes from attributes on the triggering
// element: hx-confirm (message), data-confirm-title, data-confirm-label,
// data-confirm-variant="danger".
(function () {
  function parts() {
    var dlg = document.getElementById("confirm-dialog");
    // No dialog on this page, or a browser without <dialog>: return null and let
    // htmx fall through to its native confirm() — degraded, but never a lost guard.
    if (!dlg || typeof dlg.showModal !== "function") return null;
    return {
      dlg: dlg,
      title: dlg.querySelector("#confirm-dialog-title"),
      message: dlg.querySelector("#confirm-dialog-message"),
      cancel: dlg.querySelector("#confirm-dialog-cancel"),
      ok: dlg.querySelector('[data-confirm-ok="default"]'),
      danger: dlg.querySelector('[data-confirm-ok="danger"]'),
    };
  }

  document.body.addEventListener("htmx:confirm", function (evt) {
    var question = evt.detail.question;
    if (!question) return; // element has no hx-confirm — nothing to confirm
    var p = parts();
    if (!p) return;
    evt.preventDefault();

    var elt = evt.detail.elt;
    var isDanger = elt.getAttribute("data-confirm-variant") === "danger";
    var ok = isDanger ? p.danger : p.ok;
    var hide = isDanger ? p.ok : p.danger;

    // Each button remembers its own authored default label, so a per-use
    // data-confirm-label never leaks into the next dialog that omits one.
    if (!ok.dataset.defaultLabel) ok.dataset.defaultLabel = ok.textContent.trim();
    ok.textContent = elt.getAttribute("data-confirm-label") || ok.dataset.defaultLabel;
    p.title.textContent = elt.getAttribute("data-confirm-title") || "Are you sure?";
    p.message.textContent = question;
    ok.hidden = false;
    hide.hidden = true;

    function cleanup() {
      ok.removeEventListener("click", onOk);
      p.cancel.removeEventListener("click", onCancel);
      p.dlg.removeEventListener("close", cleanup);
    }
    function onOk() {
      cleanup();
      p.dlg.close();
      evt.detail.issueRequest(true); // true = skip htmx's native confirm
    }
    function onCancel() {
      cleanup();
      p.dlg.close();
    }

    ok.addEventListener("click", onOk);
    p.cancel.addEventListener("click", onCancel);
    // Covers Esc and backdrop-click dismissal, which never reach onCancel.
    p.dlg.addEventListener("close", cleanup);
    p.dlg.showModal();
  });
})();

// --- Charge form: date-sync + status-required + location-label toggles (RD12/RD13/RD14) ---
//
// All listeners below use document.body-scoped event delegation, never
// per-element addEventListener at render time — the manual-charge create form
// and any number of simultaneously-open inline edit rows all need this
// behavior with no re-binding step when htmx swaps a row in. See
// internal/gateway/AGENTS.md RD12/RD13/RD14 for the full rationale and the
// rejected alternatives (design.md §D-JS, RM33-gateway-update-charge-form).

// RD12 — changing the "charged_on" date input rewrites only the DATE portion
// of started_at/ended_at within the same <form>, preserving whatever time the
// user already set. A datetime-local value is always "YYYY-MM-DDTHH:MM", so
// slice(10) is exactly "THH:MM". An empty started_at/ended_at is left empty —
// never auto-filled (roadmap D12 explicitly rejects "clobber to midnight").
document.body.addEventListener("change", function (evt) {
  var dateInput = evt.target;
  if (!dateInput || !dateInput.matches || !dateInput.matches('input[name="charged_on"]')) return;
  var form = dateInput.closest("form");
  if (!form) return;
  var newDate = dateInput.value;
  if (!newDate) return;
  ["started_at", "ended_at"].forEach(function (name) {
    var input = form.querySelector('input[name="' + name + '"]');
    if (input && input.value) {
      input.value = newDate + input.value.slice(10);
    }
  });
});

// RD13 — the "status" select drives whether ended_at/end_battery_pct are
// required, live, with no htmx round-trip (D-RM33-6). Applied on "change" AND
// on "htmx:load" (fires on the initial full page load AND on every
// htmx-swapped fragment — verified via Context7 against the htmx source,
// 2026-08-29: htmx dispatches "htmx:load" on document.body once at initial
// DOMContentLoaded-deferred init, and again on the swapped-in root element
// after every settled swap; both bubble to document.body, which is exactly
// what this delegated listener relies on). Running on load as well as change
// means a freshly-rendered or freshly-swapped form is always correct
// immediately, duplicating the server-rendered initial `required` state from
// design.md §D-Fields on purpose — if the two ever disagree, the JS state
// wins in the live DOM and the disagreement is inert.
function applyChargeStatusRequiredToggle(select) {
  var form = select.closest("form");
  if (!form) return;
  var isDone = select.value === "DONE";
  var endedAt = form.querySelector('input[name="ended_at"]');
  var endBatteryPct = form.querySelector('input[name="end_battery_pct"]');
  if (endedAt) endedAt.required = isDone;
  if (endBatteryPct) endBatteryPct.required = isDone;
}

document.body.addEventListener("change", function (evt) {
  var select = evt.target;
  if (!select || !select.matches || !select.matches('select[name="status"]')) return;
  applyChargeStatusRequiredToggle(select);
});

document.body.addEventListener("htmx:load", function (evt) {
  var root = evt.target;
  if (!root || !root.querySelectorAll) return;
  if (root.matches && root.matches('select[name="status"]')) {
    applyChargeStatusRequiredToggle(root);
  }
  var selects = root.querySelectorAll('select[name="status"]');
  for (var i = 0; i < selects.length; i++) {
    applyChargeStatusRequiredToggle(selects[i]);
  }
});

// RD14 — the "location_kind" select drives whether location_label is enabled:
// the free-text label is only meaningful when the kind is OTHER, so the input
// is disabled otherwise. A disabled input is NOT submitted, so switching away
// from OTHER drops the label from the save — deliberate (the label has no
// meaning for HOME/WORK). Same change + htmx:load shape as RD13 above,
// duplicating the server-rendered initial `disabled` state on purpose.
function applyChargeLocationLabelToggle(select) {
  var form = select.closest("form");
  if (!form) return;
  var label = form.querySelector('input[name="location_label"]');
  if (label) label.disabled = select.value !== "OTHER";
}

document.body.addEventListener("change", function (evt) {
  var select = evt.target;
  if (!select || !select.matches || !select.matches('select[name="location_kind"]')) return;
  applyChargeLocationLabelToggle(select);
  // Focus the freshly-enabled label: picking OTHER is the only path that
  // enables it (a change event implies the value actually moved, so the input
  // was disabled a moment ago), and the user's next action is typing into it.
  // The htmx:load path below deliberately does NOT focus — a page load or row
  // swap must never steal focus from where the user already is.
  var form = select.closest("form");
  if (form && select.value === "OTHER") {
    var label = form.querySelector('input[name="location_label"]');
    if (label) label.focus();
  }
});

document.body.addEventListener("htmx:load", function (evt) {
  var root = evt.target;
  if (!root || !root.querySelectorAll) return;
  if (root.matches && root.matches('select[name="location_kind"]')) {
    applyChargeLocationLabelToggle(root);
  }
  var selects = root.querySelectorAll('select[name="location_kind"]');
  for (var i = 0; i < selects.length; i++) {
    applyChargeLocationLabelToggle(selects[i]);
  }
});

// --- Theme switcher: instant client-side apply (RD15) --------------------
//
// See internal/gateway/AGENTS.md RD15 and design.md D6
// (RM42-gateway-add-theme-selector tier 2) for the full rationale and the
// rejected alternatives. Same document.body-delegated shape as RD9-RD14
// above (kept for consistency with every other listener in this file, even
// though the switcher's own markup never gets swapped — D6 explicitly
// avoids any htmx swap for this control, so re-binding was never actually a
// concern here).
//
// Clicking a theme option applies it to the DOM immediately, before the
// background hx-post (hx-swap="none") that persists it has resolved. D6
// explicitly rejects HX-Location/any reload here: a theme is pure CSS, so
// there is nothing to re-render, unlike the language switch's
// server-rendered text. The value comes out of the clicked button's own
// hx-vals JSON (rather than a duplicate data-theme="apex" attribute), so
// the value the server receives and the value the DOM applies come from ONE
// literal per option, authored once in theme_switcher.templ.
document.body.addEventListener("click", function (evt) {
  var btn = evt.target.closest('button[hx-post="/ui/theme/switch"]');
  if (!btn) return;
  var vals;
  try {
    vals = JSON.parse(btn.getAttribute("hx-vals") || "{}");
  } catch (e) {
    return;
  }
  var theme = vals.theme;
  if (!theme) return;
  document.documentElement.dataset.themePrevious = document.documentElement.dataset.theme;
  document.documentElement.dataset.theme = theme;

  // Close the dropdown. It is CSS-only and stays open while focus is inside it
  // (DaisyUI hides .dropdown-content on :not(:focus-within)), so picking an
  // option left the menu hanging open over the page.
  //
  // Blur whatever is focused inside this dropdown, not the clicked button:
  // Chrome and Firefox focus a button on click, Safari does not. In Safari
  // focus stays on the trigger, so btn.blur() alone would close nothing there.
  var dropdown = btn.closest(".dropdown");
  if (dropdown && dropdown.contains(document.activeElement)) {
    document.activeElement.blur();
  }

  // Rewrite the trigger's theme name. The trigger is server-rendered, so without
  // this it kept naming the OLD theme while the page was already painted in the
  // new one.
  //
  // The new text is the clicked option's OWN text, not a title-cased copy of the
  // theme code: Go's titleCase already produced that word when it rendered the
  // option, so reading it back keeps one source of truth for the casing, exactly
  // as the theme value is read back out of the option's hx-vals above.
  var current = dropdown && dropdown.querySelector("[data-theme-current]");
  if (current) {
    current.dataset.previousLabel = current.textContent;
    current.textContent = btn.textContent.trim();
  }
});

// If the background persist request failed, revert to the value the DOM
// held before the optimistic apply above (design.md D6): the server stays
// the source of truth for what renders on the NEXT full page load (via
// PreferencesMiddleware -> data-theme on base.templ), so leaving a
// never-persisted theme applied would only have it silently flip back on
// the user's very next navigation, with no explanation. Reverting
// immediately, in the same interaction, is the client-side mirror of
// LangSwitch's own failure-path rule ("do NOT still send HX-Location, so
// the client does not reload into a state the persisted write never
// actually reached") — made necessary here specifically because this
// optimistic apply happens BEFORE the server confirms anything, which
// LangSwitch's server-driven HX-Location never did.
document.body.addEventListener("htmx:afterRequest", function (evt) {
  var elt = evt.detail.elt;
  if (!elt || !elt.matches || !elt.matches('button[hx-post="/ui/theme/switch"]')) return;
  // The trigger label is reverted alongside the theme itself: a button naming a
  // theme the server never stored is the same lie, in words instead of colour.
  var current = elt.closest(".dropdown");
  current = current && current.querySelector("[data-theme-current]");

  if (evt.detail.successful) {
    delete document.documentElement.dataset.themePrevious;
    if (current) delete current.dataset.previousLabel;
    return;
  }
  var prev = document.documentElement.dataset.themePrevious;
  if (prev) document.documentElement.dataset.theme = prev;
  delete document.documentElement.dataset.themePrevious;
  if (current && current.dataset.previousLabel) {
    current.textContent = current.dataset.previousLabel;
    delete current.dataset.previousLabel;
  }
});

// --- iOS install hint: show ui.InstallHint on iOS only (RD16) ----------------
//
// See internal/gateway/AGENTS.md RD16 and
// kkpa/context/architecture/gateway-client-side-js.md for the full rationale and
// the rejected alternatives.
//
// /site.webmanifest makes the app installable. Android Chrome reads it and
// raises its own install prompt, so Android needs NO code here. Safari never
// fires `beforeinstallprompt` and offers nothing, so on iOS the only install
// path is Share -> "Add to Home Screen" — a row buried under the sharing apps
// that a user cannot discover. No API can open that sheet, so pointing at it is
// the only thing a page can do.
//
// This is the one listener in this file that is NOT purely delegated: it runs
// once at load to decide whether to unhide the hint. The dismiss click IS
// delegated, like every other listener here.
(function () {
  var HINT_ID = "ios-install-hint";
  // Versioned key: if the copy is ever rewritten into a different hint, bump the
  // suffix to show it again to people who dismissed the old one.
  var DISMISSED_KEY = "magus.install-hint.dismissed.v1";

  // iPadOS 13+ reports itself as "Macintosh" and is indistinguishable from a
  // real Mac by User-Agent alone; multi-touch is the tell. A desktop Safari has
  // maxTouchPoints 0.
  function isIOS() {
    var ua = navigator.userAgent || "";
    if (/iPhone|iPad|iPod/.test(ua)) return true;
    return /Macintosh/.test(ua) && navigator.maxTouchPoints > 1;
  }

  // Already installed: iOS sets the legacy navigator.standalone inside a
  // home-screen app; display-mode covers every other engine and future Safari.
  // Showing "install this" inside the installed app is the worst failure this
  // hint has, so both are checked.
  function isInstalled() {
    if (window.navigator.standalone === true) return true;
    return !!(window.matchMedia && window.matchMedia("(display-mode: standalone)").matches);
  }

  // localStorage, not a cookie and not the account settings table: "I have seen
  // this hint" is a per-BROWSER fact about a device, not a preference that
  // belongs to the user's account — the same person on a desktop must never
  // inherit a dismissal made on their iPhone. A cookie would also ride along on
  // every request, including /static, to tell the server something the server
  // never reads. Private mode can throw on access, so both calls are guarded and
  // a failure degrades to "show it" / "cannot remember", never to a broken page.
  function dismissed() {
    try {
      return localStorage.getItem(DISMISSED_KEY) === "1";
    } catch (e) {
      return false;
    }
  }
  function rememberDismissal() {
    try {
      localStorage.setItem(DISMISSED_KEY, "1");
    } catch (e) {}
  }

  var hint = document.getElementById(HINT_ID);
  if (hint && isIOS() && !isInstalled() && !dismissed()) {
    hint.hidden = false;
  }

  // Delegated so the button works whatever re-renders around it.
  document.body.addEventListener("click", function (evt) {
    if (!evt.target.closest) return;
    if (!evt.target.closest("[data-install-hint-dismiss]")) return;
    var el = document.getElementById(HINT_ID);
    if (el) el.hidden = true;
    rememberDismissal();
  });
})();
