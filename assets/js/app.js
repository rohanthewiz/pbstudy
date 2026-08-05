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
})();
