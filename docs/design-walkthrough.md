# The design, layer by layer

What the dashboard shows, in the order it shows it, and why each decision is the
way it is. The reference for this is the redesigned prototype in the repository
root — `index.html`, a self-extracting bundle that is the design source of truth
rather than part of the shipped product.

The whole page answers one question first and then lets a reader find out why. The
layers below are that sequence.

## Layer 0 · The chrome

A header bar: brand mark and wordmark, then a spacer, then the controls. What is
in it and in what order comes from `config/chrome.yaml`, so a deployment can add a
link to its own service desk, drop a control it does not need, or move the
language switcher to the far end.

The wordmark and the document title are different strings. A wordmark is a name
and wants to be short; a title competes for attention in a list of twenty browser
tabs and wants to say what the page answers.

Everything in the bar is inside one GET form with real form fields, so the page
works with no JavaScript at all. HTMX upgrades that; it does not enable it.

Under it sits a second bar, the **strip** — not controls for the page but facts
about it. On the left, the platforms whose exchanges these numbers describe, each
an external link carrying its own mark. On the right, the **role switch**:
Issuers or Requestors. Both come from `chrome.yaml` in the same way the header
does, and both omit themselves when the deployment declares nothing to put in
them.

## Layer 1 · The verdict

**Are these services working right now?**

A serif `h1` that changes with the scope — "the national data sharing services",
"the sub-national data sharing services" — because a heading that does not name
what it is counting makes the reader check.

Then, in order:

- **One line of tally.** "24 Operational · 5 Partial outage · 3 Major outage · 2
  Under maintenance", or a plain "All clear" when there is nothing to report,
  which is worth saying rather than making a reader infer it from a row of zeros.
- **Three sentences of prose** saying what the words mean in civic terms, with the
  load-bearing word picked out. This is the `prose` widget: the copy is term ids
  in `layout.yaml`, and a locale string may emphasise a word with
  `<mark tone="…">`. The emphasis travels inside the sentence, so a translator
  puts it on whichever word carries the meaning in their language.
- **A proportional bar.** One segment per status, width by count. Its proportions
  are its content, so the hairline between segments is load-bearing: adjacent
  fills are pastels that cannot contrast with each other, and without a divider
  the bar reads as one undivided block. Below 480px it is hidden entirely, because
  at that width it is a row of slivers and the legend below says the same thing in
  text.
- **A legend**, which unlike the bar includes the statuses at zero — a band of no
  width is not informative, a legend entry reading "0" is.
- **A call to action** that scrolls to the leaderboard and puts focus in the
  search box, so a keyboard reader arrives where the pointer reader is looking.
- **When it was last updated, and how much is covered.** Data quietly going stale
  is the failure a status dashboard can least afford, so after fifteen minutes it
  says so.
- **A disclosure of the rules**, with the live threshold numbers interpolated from
  `domain.yaml`. The rule shown is provably the rule applied, because both read
  the same values.

Status is deliberately **not** windowed. An average over ninety days cannot answer
"right now", and a verdict bar that tried would be answering a question nobody
asked.

## Layer 1.5 · Signals

What needs attention, and why. Each card names the literal rule that produced it —
"availability under its target on each of the last 7 days" — because a dashboard
that says "this needs attention" has to say why, and a blended score cannot.

Each card's action applies the filter it describes and scrolls to the results, so
the claim and the evidence are one click apart.

Beside the band's name is a second set of findings: **where the opportunities are**.
The same contract, mirrored — issuance nobody is consuming rather than service
nobody is getting. A document published at scale with four requestors, a live API
with none at all, a whole category resting on one caller, the fastest-growing
demand on the estate. Each still prints the rule it applied. Some of them are
about the other side of the exchange, so their action switches the board's role
as well as its filter.

## Layer 2 · The leaderboard

**Rank 1 is the best performer**, and a service whose availability cannot be read
ranks last — it has not been shown to be failing, and it cannot claim to be
working either. The caption above the board says which end is which, because a
leaderboard is ambiguous about that on its own.

The board is read one **role** at a time: requestors are ranked against
requestors, counted against requestors, and filtered within requestors. The
verdict above it stays role-agnostic — whether the country's services are working
is not a question the reader changes by looking at who is calling them.

The published rule is arithmetic: **attention = downtime + error rate**. Both are
percentages of the same population of requests, so they add — a service down for
2% of the window and erroring on 1% of what got through is failing 3% of the
people who came to it.

A service with **no availability reading at all ranks first**, ahead of every
measured failure. Not because it is necessarily worse, but because nobody can say
whether citizens are being served, and that is the most urgent thing on the page.
Scoring it zero would sort it among the healthy — the specific mistake
`model.OptFloat` exists to prevent.

The board speaks in **downtime**, not availability: "0.6% downtime" is the figure a
reader looking for a problem wants, and "99.4% available" makes them do the
subtraction. Downtime is declared in `domain.yaml` as the complement of
availability, and its target is **derived** from the availability target, so
"availability should reach 99.5%" and "downtime should stay under 0.5%" cannot
become two numbers that disagree.

Everything on the board follows the reader's selected period, and the ordering and
the figures are read over the same window — so switching to 7 days genuinely
re-orders it rather than re-labelling it.

Below 760px the table becomes cards. Both are in the document and CSS chooses;
swapping markup at a breakpoint would mean the server guessing at a viewport it
cannot see.

The filter bar is real form controls — checkboxes for the chips, because a filter
is a state rather than an action and a checkbox is what a keyboard and a screen
reader already understand. The result count is visible in the bar and announced
from a live region in the page shell.

## Layer 3 · The drawer

One service in depth, in a modal `<dialog>` that slides in from the inline-end
edge — the left in a right-to-left locale, from one logical property rather than a
second stylesheet.

Its header carries the status, the category and region, the name, and **the rule
that produced this verdict**, so a reader who disagrees with it can see exactly
what decided it.

Five panels, and they are **navigation rather than tabs**: each is a real URL that
`hx-push-url` puts in the address bar, so "the incidents tab of this service" can
be bookmarked and sent. Marking them up as an ARIA tablist would erase that,
announce "tab" for something that is a link, and oblige arrow-key handling that
fights the browser.

- **Overview** — a tile per metric, and ninety days of downtime traced against
  the limit it is held to. For a requestor it also carries what it calls, how it
  subscribes, and which half of its failures are its own — the figure it can
  actually act on, which an error rate alone does not give it.
- **Opportunity** — who pulls this document, as shares of its demand, and how
  that compares with everything like it. The peer bars are the backbone rather
  than a footnote: most APIs have one requestor or none, so a mix of callers has
  nothing to say about them, while every service — including one nobody has
  onboarded against — can be read against its own category's median and best.
- **Errors** — a bar per error code, with its meaning in words.
- **Traffic** — volume with the error rate overlaid, so a spike in one against a
  flat line in the other is visible.
- **Incidents** — a timeline per incident, with its stages and their times.

Every chart is paired with a data table. A line on a screen is not accessible to a
screen reader, not copyable and not checkable.

## Where the shipped app departs from the prototype

Stated plainly, because a design document that quietly omits this is misleading.
Every item below is a deliberate choice, not drift.

- **Outlines are heavier.** The prototype's `--border-strong` is `#C9C5BC` in
  light and `#464335` in dark. Both sit under 3:1 against the surface behind
  them, which is the contrast a control boundary owes a reader under WCAG
  1.4.11. The shipped tokens are `#8A867E` and `#8C8871`, which pass. Chips,
  selects and the segmented switches therefore read a shade more defined than
  the prototype's. That is the trade: the design intent is a lighter hairline,
  and the obligation is a visible one.
- **The drawer title takes focus, and does not show a ring.** Opening the panel
  moves focus to its heading rather than to the close button, so a screen reader
  announces what opened instead of only how to leave. The heading is not in the
  tab order, so the ring the browser would paint on that programmatic focus
  indicates nothing a reader can act on, and is suppressed.
- **Drawer tabs are links, not `role="tab"`.** Each is a real URL that
  `hx-push-url` puts in the address bar, so "the incidents tab of this service"
  can be bookmarked and sent. The rationale is at the tablist in
  `web/templates/widgets/drawer.html`.
- **Emphasis in the verdict copy is markup, not substring matching.** The
  prototype finds the word to highlight by matching the English string. The
  shipped build marks it in the locale file itself, through the closed
  vocabulary in `internal/prose`, so a translator decides which word carries the
  emphasis in their language.
- **Platform marks are served assets.** The prototype inlines its logo as a data
  URI because it has to stay one file. Here it is a file under `web/static`,
  referenced from `domain.yaml` — cacheable, themable, and inspectable.
- **The sparkline is pinned to ninety days.** The prototype's is too, but the
  shipped one says so: the heading, the summary and the window agree, and the
  period control deliberately does not move it.

## Accessibility

Not a section of this document, because it is not a feature. See
[accessibility.md](accessibility.md) for the conformance claim and how it is
tested.
