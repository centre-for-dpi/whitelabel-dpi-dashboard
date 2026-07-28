/* The dashboard's client-side behaviour.
 *
 * Everything here is delegated from `document`, not bound to elements. That is
 * the single decision that makes this file work with HTMX: content swapped into
 * the page needs no re-initialisation, because nothing was ever initialised
 * against it. It is also the reason this is ~150 lines of vanilla JavaScript
 * rather than a framework — the swap-survival problem a framework would solve
 * does not exist once handlers live on the document.
 *
 * Behaviours are keyed by a `data-dpi-*` attribute. Templates emit only those
 * attributes, never inline handlers and never behaviour-bearing classes, which
 * is what would let Alpine bind to the same markup if this deployment ever
 * chose `client.runtime: alpine`.
 *
 * Nothing here is load-bearing. Every control is a real link or form control
 * with a real target, so the dashboard works with this file blocked, failed, or
 * disabled entirely. This layer only ever removes a page reload.
 */
(() => {
  "use strict";

  /** Behaviour registry, keyed by attribute name. */
  const behaviours = {};
  const register = (attr, handlers) => { behaviours[attr] = handlers; };

  /** Dispatch an event to whichever behaviour owns the nearest matching element. */
  const dispatch = (type, event) => {
    for (const [attr, handlers] of Object.entries(behaviours)) {
      const fn = handlers[type];
      if (!fn) continue;
      const el = event.target.closest?.(`[data-${attr}]`);
      if (el) fn(el, event);
    }
  };

  for (const type of ["click", "change", "input", "keydown", "pointermove", "pointerleave"]) {
    document.addEventListener(type, (e) => dispatch(type, e), type === "pointermove" ? { passive: true } : false);
  }

  /* --- theme ---------------------------------------------------------------
   *
   * The toggle flips the attribute immediately and records the choice, so the
   * change is instant rather than a round trip. The cookie is what lets the
   * SERVER render the right theme on the next page load: without it the page
   * would arrive light and flash dark, which is worse than no toggle at all.
   */
  register("dpi-theme-toggle", {
    click(el, e) {
      e.preventDefault();
      const root = document.documentElement;
      const next = root.dataset.theme === "dark" ? "light" : "dark";
      root.dataset.theme = next;
      document.cookie = `theme=${next}; path=/; max-age=31536000; samesite=lax`;
    },
  });

  /* --- form controls that submit themselves -------------------------------- */

  register("dpi-submit", {
    change(el) { el.form?.requestSubmit?.(); },
  });

  /* --- disclosure ----------------------------------------------------------
   *
   * aria-expanded is the state, not a class: it is what a screen reader reads
   * and what the stylesheet keys off, so there is only one source of truth.
   *
   * The hidden field beside it carries that same fact into the next request. It
   * has to, because the fragment that comes back replaces this whole panel — and
   * without it the panel collapsed itself every time a reader on a narrow screen
   * changed a filter, so making two changes meant reopening it in between.
   */
  register("dpi-disclosure", {
    click(el, e) {
      e.preventDefault();
      const open = el.getAttribute("aria-expanded") !== "true";
      el.setAttribute("aria-expanded", open);
      const field = el.form?.querySelector("[data-dpi-disclosure-state]");
      if (field) field.value = open ? "open" : "";
    },
  });

  /* --- motion --------------------------------------------------------------
   *
   * The stylesheet's prefers-reduced-motion rule cannot reach scripted
   * scrolling: behavior:"smooth" is an argument, not a declaration, so it
   * animates regardless of what the CSS says. Every scripted scroll goes
   * through this one helper so the two cannot disagree.
   *
   * Read per call rather than cached, because a reader can change the setting
   * without reloading and the next scroll should already respect it.
   */
  function scrollBehaviour() {
    return window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth";
  }

  /* --- scroll to a section -------------------------------------------------- */

  register("dpi-scroll-to", {
    click(el, e) {
      const target = document.querySelector(el.getAttribute("href"));
      if (!target) return;
      e.preventDefault();
      target.scrollIntoView({ behavior: scrollBehaviour(), block: "start" });
      // Focus follows the scroll, or a keyboard user is left where they were
      // while the page moves without them.
      const focusable = target.querySelector("input, select, a, button") || target;
      focusable.focus?.({ preventScroll: true });
    },
  });

  /* --- table rows ----------------------------------------------------------
   *
   * The name link carries the HTMX attributes and is the only path to the
   * drawer; the row forwards a click anywhere in it to that link. Doing it this
   * way round is what makes the pointer and the keyboard behave identically —
   * when the row held the attributes itself, a click swapped the drawer in and
   * a keyboard user, who can only reach the link, got a whole new page. It also
   * means the element that opened the drawer can hold focus, so there is
   * somewhere to put it back on close.
   */
  register("dpi-row-link", {
    click(el, e) {
      // A click that already landed on a link is that link's business.
      if (e.target.closest("a")) return;
      const link = el.querySelector(".lb-name");
      if (link) link.click();
    },
  });

  /* --- drawer --------------------------------------------------------------
   *
   * A <dialog> promoted with showModal(), so the browser provides the focus
   * trap, the Escape key, the ::backdrop and inertness for everything behind
   * it. Reimplementing those in JavaScript is a well-known way to get them
   * subtly wrong — a hand-rolled trap that enumerates focusable elements with a
   * selector list, for instance, misses <summary> and leaks focus to the body
   * from any panel that contains a disclosure.
   *
   * The markup deliberately omits the `open` attribute, because `open` shows a
   * dialog non-modally and provides none of the above.
   */

  // The control that opened the drawer, so focus can go back to it. Losing this
  // is what strands a keyboard reader at the top of the document on close.
  let drawerOpener = null;

  function showDrawer(dialog) {
    if (!dialog || dialog.open) return;
    dialog.showModal();
    // Announce the panel from its heading rather than dumping focus on the
    // close button, which tells a screen reader only that there is a way out.
    const heading = dialog.querySelector("#drawer-title");
    if (heading) {
      heading.setAttribute("tabindex", "-1");
      heading.focus({ preventScroll: true });
    }
  }

  register("dpi-drawer", {
    click(el, e) {
      // With showModal() the backdrop is part of the dialog's own box, so a
      // click landing on the element itself rather than the panel inside it is
      // a click on the backdrop.
      if (e.target === el) closeDrawer();
    },
  });

  register("dpi-drawer-close", {
    click(el, e) { e.preventDefault(); closeDrawer(); },
  });

  function closeDrawer() {
    const dialog = document.getElementById("drawer");
    // close() fires a close event, which is where the teardown happens — so
    // Escape, the close button and a backdrop click all follow one path.
    if (dialog && dialog.open) dialog.close();
    else teardownDrawer();
  }

  function teardownDrawer() {
    const host = document.getElementById("drawer-host");
    if (host) host.innerHTML = "";
    // Restore the address bar, so the back button and a copied link agree with
    // what is on screen. `tab` names a panel of the drawer, so it goes with it —
    // otherwise closing leaves /?tab=errors, a URL describing a panel that is no
    // longer open.
    if (window.location.pathname.startsWith("/service/")) {
      const params = new URLSearchParams(window.location.search);
      params.delete("tab");
      const query = params.toString();
      history.pushState({}, "", query ? "/?" + query : "/");
    }
    // Put the reader back where they were. Checking isConnected matters because
    // the row may have been replaced by a fragment swap while the drawer was up.
    if (drawerOpener && drawerOpener.isConnected) drawerOpener.focus({ preventScroll: true });
    drawerOpener = null;
  }

  // Escape and the close button both end here.
  document.addEventListener("close", (e) => {
    if (e.target.id === "drawer") teardownDrawer();
  }, true);

  // Remember the opener before the request goes out, while the trigger is still
  // the element the reader activated.
  //
  // Only when the drawer is not already open. Switching tabs is also a request
  // targeting #drawer-host, so without this guard the opener would be
  // overwritten by the tab link — and that link is inside the drawer, so by the
  // time it closed there would be nothing left to return focus to.
  document.body.addEventListener("htmx:beforeRequest", (e) => {
    const t = e.detail && e.detail.target;
    if (!t || t.id !== "drawer-host") return;
    const dialog = document.getElementById("drawer");
    if (dialog && dialog.open) return;
    drawerOpener = e.detail.elt;
  });

  // Promote the dialog once the fragment is in the document. A tab switch
  // re-renders the whole drawer, so this runs again on an already-open dialog,
  // which showDrawer guards against.
  document.body.addEventListener("htmx:afterSwap", (e) => {
    if (e.detail && e.detail.target && e.detail.target.id === "drawer-host") {
      showDrawer(document.getElementById("drawer"));
    }
    announce();
  });

  /* --- the announcer -------------------------------------------------------
   *
   * Copies the swapped-in result count into the one live region that survives
   * swaps. See the comment on #a11y-status in page.html for why it cannot
   * simply live inside the fragment.
   */
  function announce() {
    const region = document.getElementById("a11y-status");
    const source = document.querySelector("[data-dpi-announce]");
    if (!region || !source) return;
    const text = source.textContent.trim();
    // An identical string is not a change, so nothing would be announced. This
    // matters when a reader narrows a filter and the count happens to land on
    // the same number: clearing first guarantees the region mutates.
    if (region.textContent === text) region.textContent = "";
    region.textContent = text;
  }

  // A drawer present in the initial document — a shared /service/{id} link —
  // has no swap to hook, so promote it on load.
  if (document.readyState !== "loading") showDrawer(document.getElementById("drawer"));
  else document.addEventListener("DOMContentLoaded", () => showDrawer(document.getElementById("drawer")));

  /* --- chart crosshair -----------------------------------------------------
   *
   * The only genuinely imperative behaviour here. The server sends the plotted
   * positions in `data-points`, so this finds the nearest one rather than
   * re-deriving the scale from data it does not have.
   */
  register("dpi-chart", {
    pointermove(svg, e) {
      const points = parsePoints(svg);
      if (!points.length) return;

      const box = svg.getBoundingClientRect();
      let fraction = (e.clientX - box.left) / box.width;
      if (document.documentElement.dir === "rtl") fraction = 1 - fraction;
      fraction = Math.min(Math.max(fraction, 0), 1);

      const point = points[Math.round(fraction * (points.length - 1))];
      const viewBox = svg.viewBox.baseVal;
      showCrosshair(svg, (point.x / viewBox.width) * 100, (point.y / viewBox.height) * 100);
    },
    pointerleave(svg) { hideCrosshair(svg); },
  });

  function parsePoints(svg) {
    if (svg._points) return svg._points;
    const raw = svg.dataset.points || "";
    svg._points = raw ? raw.split(";").map((p) => {
      const [x, y] = p.split(",");
      return { x: parseFloat(x), y: parseFloat(y) };
    }) : [];
    return svg._points;
  }

  function showCrosshair(svg, leftPct, topPct) {
    const figure = svg.closest(".chart");
    if (!figure) return;

    let line = figure.querySelector(".crosshair");
    let dot = figure.querySelector(".crosshair-dot");
    if (!line) {
      line = Object.assign(document.createElement("span"), { className: "crosshair" });
      dot = Object.assign(document.createElement("span"), { className: "crosshair-dot" });
      figure.append(line, dot);
    }
    line.style.insetInlineStart = `${leftPct}%`;
    dot.style.insetInlineStart = `${leftPct}%`;
    dot.style.insetBlockStart = `${topPct}%`;
  }

  function hideCrosshair(svg) {
    svg.closest(".chart")?.querySelectorAll(".crosshair, .crosshair-dot").forEach((n) => n.remove());
  }

  /* --- HTMX -----------------------------------------------------------------
   *
   * A swap replaces markup, and a crosshair drawn against markup that is now
   * gone would linger over the new chart.
   */
  document.body?.addEventListener("htmx:beforeSwap", () => {
    document.querySelectorAll(".crosshair, .crosshair-dot").forEach((n) => n.remove());
  });
})();
