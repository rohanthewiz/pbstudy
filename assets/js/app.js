// pbstudy client-side behaviour.
//
// Deliberately tiny and framework-free. Every page works with JavaScript
// disabled — forms round-trip to the server and links navigate. What lives
// here is strictly ergonomic: keyboard shortcuts and a book/chapter picker
// that avoids a page load to change chapters.

(function () {
  "use strict";

  // ---- reader keyboard navigation ----------------------------------------
  // Left/right arrows page through chapters. Bound only when the reader
  // supplied the target URLs, so other pages are unaffected.
  document.addEventListener("keydown", function (ev) {
    // Never steal keys while the user is typing.
    var t = ev.target;
    if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) {
      return;
    }
    if (ev.metaKey || ev.ctrlKey || ev.altKey) return;

    if (ev.key === "ArrowLeft") {
      var prev = document.querySelector("[data-nav-prev]");
      if (prev) { window.location.href = prev.getAttribute("href"); }
    } else if (ev.key === "ArrowRight") {
      var next = document.querySelector("[data-nav-next]");
      if (next) { window.location.href = next.getAttribute("href"); }
    } else if (ev.key === "/") {
      // "/" focuses the search box, the one shortcut worth stealing.
      var search = document.querySelector("[data-search-input]");
      if (search) { ev.preventDefault(); search.focus(); search.select(); }
    }
  });

  // ---- chapter picker ----------------------------------------------------
  // Changing the book select repopulates the chapter select from the book's
  // chapter count (carried in a data attribute), then the form submits
  // normally. Without JS the chapter select still holds the current book's
  // chapters and the Go button works — just no live repopulation.
  var bookSel = document.querySelector("[data-book-select]");
  var chapSel = document.querySelector("[data-chapter-select]");

  if (bookSel && chapSel) {
    bookSel.addEventListener("change", function () {
      var opt = bookSel.options[bookSel.selectedIndex];
      var count = parseInt(opt.getAttribute("data-chapters"), 10) || 1;
      var current = parseInt(chapSel.value, 10) || 1;

      chapSel.innerHTML = "";
      for (var i = 1; i <= count; i++) {
        var o = document.createElement("option");
        o.value = String(i);
        o.textContent = String(i);
        if (i === current) { o.selected = true; }
        chapSel.appendChild(o);
      }
    });
  }

  // ---- unsaved-text guard -------------------------------------------------
  // A note can be an hour of typing, and nothing saves it but the Save button.
  // Without this, a click on a nav link, the back button or a closed tab
  // discarded it without a word.
  //
  // Forms opt in with data-guard-unsaved. Each one tracks a single flag:
  //
  //   page loads ── input ──▶ dirty ── submit ──▶ leaving is fine (saving)
  //                              │
  //                              └── any other way off the page ──▶ browser asks
  //
  // data-guard-unsaved="dirty" starts the flag set: the server re-rendered the
  // form after refusing a save, so its text is unsaved before a key is pressed.
  //
  // The prompt itself is the browser's ("Leave site? Changes you made may not
  // be saved"); pages cannot word it. It is requested only while something is
  // actually unsaved, because a beforeunload handler that always asks trains
  // people to click through it.
  var guarded = document.querySelectorAll("form[data-guard-unsaved]");
  var unsaved = 0;        // how many guarded forms hold unsaved text
  var submitting = false; // a guarded form is on its way to the server

  Array.prototype.forEach.call(guarded, function (form) {
    var dirty = false;
    var markDirty = function () {
      if (!dirty) { dirty = true; unsaved++; }
    };

    if (form.getAttribute("data-guard-unsaved") === "dirty") { markDirty(); }
    form.addEventListener("input", markDirty);

    // Saving is the one legitimate way to leave with text in the form.
    form.addEventListener("submit", function () { submitting = true; });

    // Cmd+S / Ctrl+S saves, as it does in every editor the hand already
    // knows. Bound to the form, not the document, so it acts on the form
    // being typed in and leaves the browser's own "save page" alone
    // everywhere else. requestSubmit runs the form's validation and fires
    // the submit event above; submit() is the fallback for old browsers and
    // does neither, hence setting the flag by hand.
    form.addEventListener("keydown", function (ev) {
      if (!(ev.metaKey || ev.ctrlKey) || ev.altKey) { return; }
      if (ev.key !== "s" && ev.key !== "S") { return; }
      ev.preventDefault();
      if (form.requestSubmit) {
        form.requestSubmit();
      } else {
        submitting = true;
        form.submit();
      }
    });
  });

  if (guarded.length > 0) {
    window.addEventListener("beforeunload", function (ev) {
      if (unsaved === 0 || submitting) { return; }
      // Both lines are needed: preventDefault is the standard way to ask for
      // the prompt, and returnValue is what older Chrome and Safari read.
      ev.preventDefault();
      ev.returnValue = "";
    });

    // A submit that never leaves the page (the server was unreachable, the
    // browser's own validation stopped it) must not leave the guard switched
    // off. pageshow also fires when the page comes back from the back/forward
    // cache, where the old flag would otherwise still say "submitting".
    window.addEventListener("pageshow", function () { submitting = false; });
  }

  // ---- AI sermon draft stream --------------------------------------------
  // The only part of this app that needs JavaScript to work at all. Everything
  // else round-trips through a form; a draft arrives a few words at a time over
  // several minutes, and there is no HTML-only way to show that.
  //
  // The panel carries its own endpoint in data-draft-url, so this script holds
  // no URLs and the server injects no script.
  var panel = document.querySelector("[data-draft-url]");

  if (panel && window.EventSource) {
    var statusEl = panel.querySelector("[data-draft-status]");
    var textEl = panel.querySelector("[data-draft-text]");
    var doneEl = panel.querySelector("[data-draft-done]");

    var source = new EventSource(panel.getAttribute("data-draft-url"));
    var settled = false;
    var writing = false;

    var setStatus = function (msg) {
      if (statusEl && msg) { statusEl.textContent = msg; }
    };

    // Every terminal path goes through here, and every terminal path closes the
    // socket. That close is load-bearing: EventSource reconnects on its own
    // when a stream ends, and a reconnect would ask the server for a second
    // draft of the same sermon. (The server refuses one, but the right place to
    // not ask is here.)
    var settle = function (msg) {
      settled = true;
      setStatus(msg);
      if (doneEl) { doneEl.hidden = false; }
      source.close();
    };

    var payload = function (ev) {
      try { return JSON.parse(ev.data) || {}; } catch (e) { return {}; }
    };

    source.addEventListener("status", function (ev) {
      setStatus(payload(ev).text);
    });

    source.addEventListener("delta", function (ev) {
      var text = payload(ev).text;
      if (!text || !textEl) { return; }

      if (!writing) { writing = true; setStatus("Writing…"); }

      // Follow the text only while the reader is already at the bottom, so
      // scrolling back to re-read something is not yanked away by the next
      // fragment. The 24px slack absorbs sub-pixel scroll positions.
      var atBottom =
        textEl.scrollHeight - textEl.scrollTop - textEl.clientHeight < 24;

      // A text node, never innerHTML: this is model output landing in the DOM.
      textEl.appendChild(document.createTextNode(text));

      if (atBottom) { textEl.scrollTop = textEl.scrollHeight; }
    });

    source.addEventListener("done", function () {
      settle("Draft finished and saved.");
    });

    source.addEventListener("fail", function (ev) {
      settle(payload(ev).text || "Drafting failed.");
    });

    source.onerror = function () {
      // Fires both for "could not connect" and for "the stream ended" — after
      // a done/fail we have already closed, so anything reaching here is a real
      // interruption.
      if (settled) { return; }
      settle("The connection to the draft stream was lost. Reload to see what was saved.");
    };
  }
})();
