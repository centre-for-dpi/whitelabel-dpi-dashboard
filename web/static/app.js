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
   */
  register("dpi-disclosure", {
    click(el, e) {
      e.preventDefault();
      el.setAttribute("aria-expanded", el.getAttribute("aria-expanded") !== "true");
    },
  });

  /* --- smooth scroll to a section ------------------------------------------ */

  register("dpi-scroll-to", {
    click(el, e) {
      const target = document.querySelector(el.getAttribute("href"));
      if (!target) return;
      e.preventDefault();
      target.scrollIntoView({ behavior: "smooth", block: "start" });
      // Focus follows the scroll, or a keyboard user is left where they were
      // while the page moves without them.
      const focusable = target.querySelector("input, select, a, button") || target;
      focusable.focus?.({ preventScroll: true });
    },
  });

  /* --- table rows ----------------------------------------------------------
   *
   * The row carries the HTMX attributes, so clicking anywhere in it opens the
   * service. This handler exists only to stop a click that landed on the real
   * link inside from being handled twice.
   */
  register("dpi-row-link", {
    click(el, e) {
      if (e.target.closest("a")) e.stopPropagation();
    },
  });

  /* --- drawer --------------------------------------------------------------
   *
   * A <dialog> element, so the browser provides the focus trap, the Escape
   * key and the inert backdrop. Reimplementing those in JavaScript is a
   * well-known way to get them subtly wrong.
   */
  register("dpi-drawer", {
    keydown(el, e) {
      if (e.key === "Escape") closeDrawer();
    },
    click(el, e) {
      // A click on the dialog itself rather than its panel is a backdrop click.
      if (e.target === el) closeDrawer();
    },
  });

  register("dpi-drawer-close", {
    click(el, e) { e.preventDefault(); closeDrawer(); },
  });

  function closeDrawer() {
    const host = document.getElementById("drawer-host");
    if (host) host.innerHTML = "";
    // Restore the address bar, so the back button and a copied link agree with
    // what is on screen.
    if (window.location.pathname.startsWith("/service/")) {
      history.pushState({}, "", "/" + window.location.search);
    }
  }

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
