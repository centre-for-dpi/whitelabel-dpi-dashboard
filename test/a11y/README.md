# The browser layer of the accessibility suite

Runs axe-core in a headless Chrome across the states a reader can actually be
in, and asserts the behaviour axe cannot see.

```sh
make a11y-browser      # start a server, audit it, stop it
make a11y-report       # the same, writing a screenshot per matrix cell
```

Nothing needs installing but a Chrome. There is no `node_modules`, no
`package.json`, and no Go dependency: the DevTools connection is a WebSocket
client written against the standard library, and axe-core is vendored.

## Why this is a module of its own

`go.mod` here is separate so that nothing this suite needs can reach the
production build. The dashboard ships as one static binary with no runtime
dependencies; a browser-automation dependency in the main module would
contradict that even if it were never linked in.

## What belongs here, and what does not

Three layers audit accessibility, and the boundary between them is about what
each *can* decide:

| Layer | Where | Decides |
|---|---|---|
| Contrast | `internal/a11y` via `make validate` | Whether a palette can be read, from `theme.yaml` alone |
| Structure | `internal/a11y` via `make test` | Whether the markup is right, from the HTML alone |
| This one | `make a11y-browser` | Whether the rendered result works, which needs a render |

A rule that can be checked without a browser must not live here. The first two
layers gate every commit; this one is slower and needs a binary that a
contributor may not have, so putting a cheap check in it means the check runs
less often than it could.

## The matrix

A page is not one artefact. It is a locale times a theme times a viewport times
whatever the reader has filtered, and a violation can exist in one cell and not
another — Arabic is where an unresolved term empties an accessible name, 320px is
where the table becomes cards, `forced-colors` is where everything said with a
tone stops being said. Cells cover four locales, three theme paths, five
viewports, three reader preferences, and ten interaction states.

Media preferences are emulated rather than inherited. Otherwise the host
machine's own colour scheme would decide which half of the stylesheet ever gets
tested, and a CI box set to dark would silently never exercise the light palette.

## The assertions axe cannot make

Focus containment in the modal, Escape restoring focus to the invoking control,
the live region surviving a fragment swap, the search caret surviving a debounce,
scripted scrolling honouring `prefers-reduced-motion`, bar segments staying
distinguishable when colour is stripped, no horizontal scrolling at the reflow
floor, no clipping under the required text-spacing overrides, target size, and a
visible focus ring on every focusable element.

WCAG 2.2 added criteria that cannot be automated at all — 2.4.11 Focus Not
Obscured, 3.3.7 Redundant Entry. Those are asserted by hand where they apply
rather than being quietly counted as passes because a tool did not check them.
