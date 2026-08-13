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
