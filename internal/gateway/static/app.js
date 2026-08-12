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
