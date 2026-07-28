# Accessibility

This dashboard targets **WCAG 2.2 Level AA**, and the claim is enforced by tests
rather than asserted in a document. What follows is how it is checked, what is
deliberately out of scope, and where the checks live.

```sh
make validate      # contrast, from your palette. Fails startup.
make test          # structure, over every route and locale. No browser.
make a11y          # both of the above, named
make a11y-browser  # axe-core in headless Chrome across 26 states
make a11y-report   # the same, plus a screenshot per state
```

## Why three layers

Each layer exists because there is a class of failure the others cannot see.

| Layer | Where | Sees | Runs |
|---|---|---|---|
| Contrast | `internal/a11y/contrast.go`, called from config validation | Your palette, before a page exists | Every startup |
| Structure | `internal/a11y/structure.go`, called from `a11y_e2e_test.go` | The HTML, without rendering it | Every commit |
| Browser | `test/a11y/` | The rendered result | On demand |

The boundary is about what each layer *can decide*. A rule that can be checked
without a browser must not live in the browser layer, because that layer is
slower and needs a binary a contributor may not have — putting a cheap check
there means it runs less often than it could.

## Contrast is a startup gate, not a review item

A white-label product hands its palette to every deployment, so every deployment
can break WCAG 1.4.3 and 1.4.11 without touching a line of the dashboard. The
only moment anyone can catch that is when the palette is loaded.

`internal/a11y` declares every contrast obligation the templates actually create
— body text on both surfaces, links, the primary button, each status chip, each
legend entry, each highlighted phrase, the focus ring, control boundaries, and
the dividers between bar segments — and config validation evaluates all of them.
A theme that fails does not start, and the error names the success criterion and
where on screen the pair appears:

```
theme.yaml:24:3: light.--border-strong: light: --border-strong on --bg is 1.63:1,
  below the 3.0:1 required by WCAG 1.4.11 Non-text Contrast for non-text
  (the boundary of selects, inputs and toggles on the page)
```

This works only because of a property the rest of the codebase maintains: the
stylesheet consumes `var(--token)` exclusively and no template contains a colour,
so the token map really is the whole palette. A hardcoded colour in CSS would
pass this check and still fail the page — which is why the structural layer
asserts separately that no template contains one.

Two obligations this found that review had not: `--border-strong` was 1.63:1
against the page while being the visible boundary of every select, input and
toggle; and the segmented bar had no divider at all, so since adjacent fills are
pastels that cannot contrast with each other, its proportions — which are its
content — could not be read.

### Changing the palette

Run `make validate`. If it passes, the palette is compliant for the combinations
the templates produce. If you add a combination to a template, add the obligation
to `a11y.Contract()` in the same change; an unchecked pair is an unchecked pair.

## Structure

`internal/a11y.Audit` applies thirteen rules to rendered HTML: title, `lang`,
`dir`, dangling `aria-*` references, `aria-label` on an element with no role that
accepts one, positive `tabindex`, unnamed links and buttons, unlabelled form
controls, images with no `alt` decision, tables with no caption, `th` without
scope, duplicate ids, skipped heading levels, and zero or multiple `h1`.

Every rule earned its place by having been broken here at least once. The
`aria-label`-on-a-roleless-`span` rule exists because the rank-movement indicator
carried its label that way in four rows of every locale, and nothing noticed until
axe-core was pointed at a running server.

Alongside it, `TermIDLeaks` catches unresolved term ids. The i18n resolver returns
the id itself when a term is missing anywhere — deliberately, so that literal text
in a config file simply works — which means a typo is rendered instead of
reported. It inspects text nodes and the attributes that become announcements,
and deliberately not `value`, `name`, `href` or `data-*`: a checkbox whose value
is `metric.errorRate` is naming a metric to the server, not showing a string to
anyone. That distinction is why it walks the parsed tree rather than grepping.

The suite audits every page, every fragment, and all eight locales. Locale matters
structurally, not just textually: a term that resolves in English and not in
Arabic can empty an accessible name.

## The browser layer

26 cells: four locales including a right-to-left one, three theme paths
(explicit dark, system dark, explicit light overriding system dark), five
viewports down to the 320px reflow floor plus a 200%-zoom equivalent, three
reader preferences (`prefers-contrast`, `forced-colors`, `prefers-reduced-motion`),
and ten interaction states including each drawer tab and the empty result.

Media preferences are emulated rather than inherited, because otherwise the host
machine's own colour scheme decides which half of the stylesheet is ever tested,
and a CI box set to dark would never exercise the light palette.

Then the assertions axe cannot make:

- **Focus containment.** A full tab cycle in the open drawer, on the tab whose
  panel contains a `<details>`. This is the regression that matters: the design
  prototype's hand-rolled focus trap enumerated focusable elements with a
  selector list that omitted `<summary>`, so any panel with a disclosure leaked
  focus to the document while still claiming `aria-modal="true"`. The drawer is
  now a real `<dialog>` promoted with `showModal()`, so the browser provides
  containment, Escape, the backdrop and inertness.
- **Focus restoration.** Escape closes the drawer and focus returns to the link
  that opened it.
- **The live region survives a swap.** A live region inside the fragment it
  reports on is replaced rather than updated, and announces nothing — the most
  expensive kind of accessibility bug, because it reads as correct in markup and
  is silent in use. The announcer lives in the page shell.
- **The search caret survives filtering.** htmx restores focus and selection only
  for an element with an `id`, and the search box re-renders on every keystroke.
- **Scripted scrolling honours reduced motion.** The stylesheet's media query
  cannot reach `behavior: "smooth"`, which is an argument rather than a
  declaration, so the script consults the query itself.
- **Status survives forced colours.** Every fill composites to Canvas, so what
  the design says with a tone stops being said; the bar keeps its dividers and
  every segment keeps its text.
- **Reflow, text spacing, target size, and a visible focus ring** on every
  focusable element.

## Deliberately out of scope

Being explicit about this is part of the claim.

- **AAA.** Not targeted. 1.4.6 would need 7:1 text contrast, which the current
  palette does not meet at `--muted-fg` (5.27:1 light), and reaching it means a
  new palette rather than a tweak. Two AAA criteria are met incidentally: 2.3.3
  Animation from Interactions, because scripted scrolling is gated on reduced
  motion, and 2.4.13 Focus Appearance, because the ring is 2px at ≥3:1 with an
  offset.
- **2.4.11 Focus Not Obscured** and **3.3.7 Redundant Entry** are AA criteria in
  2.2 that cannot be automated. They are checked by hand where they apply — the
  drawer's sticky header is the only candidate for the first, and the dashboard
  has no multi-step forms, so the second does not arise. They are not counted as
  passes merely because a tool did not check them.
- **Charts have no keyboard crosshair.** Each is paired with a data table
  (`data-table` in `layout.yaml`), which is the accessible equivalent and is also
  copyable and checkable. A keyboard-navigable chart would be better; the table
  is what 1.1.1 requires.
- **Screen-reader testing** is not automated here. axe-core checks what can be
  computed; it cannot tell you whether an announcement makes sense. The
  accessible names in this codebase are written to be read aloud, but that is a
  claim only a person with a screen reader can confirm.
- **The demonstration data** is generated, so its service names are the example
  domain's rather than a real deployment's, and their translations are not
  reviewed. Replacing `domain.yaml` replaces them.

## Reporting a problem

An accessibility failure here is a bug like any other, and one that the tests
above should have caught. If you find one they did not, the useful thing is not
just the fix but the missing rule — `internal/a11y` is where a new one goes, and
`test/a11y/matrix_test.go` is where a new state or behaviour goes.
