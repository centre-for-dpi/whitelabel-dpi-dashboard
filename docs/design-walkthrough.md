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

## Layer 2 · The leaderboard

**Rank 1 is the service that needs attention most**, not the one performing best.
A leaderboard whose top entry is the thing already working asks the reader to
scroll to find the problem.

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

Four panels, and they are **navigation rather than tabs**: each is a real URL that
`hx-push-url` puts in the address bar, so "the incidents tab of this service" can
be bookmarked and sent. Marking them up as an ARIA tablist would erase that,
announce "tab" for something that is a link, and oblige arrow-key handling that
fights the browser.

- **Overview** — a tile per metric, and a sparkline.
- **Errors** — a bar per error code, with its meaning in words.
- **Traffic** — volume with the error rate overlaid, so a spike in one against a
  flat line in the other is visible.
- **Incidents** — a timeline per incident, with its stages and their times.

Every chart is paired with a data table. A line on a screen is not accessible to a
screen reader, not copyable and not checkable.

## What the prototype has that the shipped app does not

Stated plainly, because a design document that quietly omits this is misleading:

- **The role dimension.** The prototype offers Issuers and Requestors, where a
  requestor is a consumer of a published service, with its own subscription type
  and a share of errors that are its own rather than the publisher's. That is a
  data-model change — the seed generator produces no consumers, and role would
  have to thread through scoping, filtering and the drawer — and it is not
  implemented.
- **The platforms strip.** A band under the header listing the platforms whose
  exchanges the dashboard covers, each an external link with its logo. The
  `chrome.yaml` `link` kind covers the shape of this; the logo slot does not exist.
- **Downtime-framed copy.** The three verdict sentences are still framed around
  uptime in all eight locales, and the prototype's are framed around downtime to
  match the rest of the redesign. The mechanism to render them is in place; the
  rewrite is a translation task rather than an engineering one.

## Accessibility

Not a section of this document, because it is not a feature. See
[accessibility.md](accessibility.md) for the conformance claim and how it is
tested.
