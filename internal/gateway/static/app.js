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
