# P9: Polish

```yaml
id:       P9
size:     L
requires: [T8.18]
blocks:   final dogfood take
parallel: partly
```

**Goal:** Every page looks like one product, reads like a person wrote it, and works on a phone as
well as a monitor.

**Why a phase:** On 2026-09-29 the owner reviewed the live pages. Record rendered unstyled, as a
white page with browser defaults. Page widths differed page to page. Gallery cards in one row had
different heights. Button sizes changed page to page. Boxes nested inside boxes. The wording
explained the machinery ("the same job stream the processing screen reads"). Four unstarted
tasks each restyled one corner of this. P9 replaces them with one foundation and one task per page,
so no page is styled twice.

**Replaces.** T7.85 (cover and link), T7.86 (erase and episode wording), T8.19 (display name) and
T8.20 (share page) were removed from their phase files before they started. Git history keeps
them. What they asked for lives in T9.1, T9.5 and T9.8 below.

**Builds on.** T8.18 lands `web/src/app.css` and the shared tokens. P9 extends it and replaces
nothing.

**Voice.** All copy follows [`voice.md`](voice.md). The owner agreed its examples on 2026-09-29.

**Layout decisions, agreed with the owner on 2026-09-29.**
* The column is responsive, never a fixed width. `main` is
  `width: min(100% - 2 * var(--gutter), var(--measure))` with `margin-inline: auto`, and
  `--gutter: clamp(1rem, 4vw, 2.5rem)` and `--measure: 64rem`. The editor may widen to `76rem`
  above 1280px. The share page narrows to `36rem`.
* Running text caps at `65ch`.
* Headings scale with `clamp()`. Body text is never below 16px.
* Card grids use `repeat(auto-fill, minmax(15rem, 1fr))`, and every card in a row has the same
  height.
* Three button kinds, used everywhere: primary (filled pill), secondary (outlined pill) and quiet
  (text link). All share one height. On a phone, a button group stacks full width, and every tap
  target is at least 44px tall.
* Sections are flat: a heading and spacing. Borders go on cards and the player only.
* Demo mode shows a small "Demo" badge, never a sentence.

---

## Wave 1

### T9.1: Cover, share link and author on the wire
```yaml
requires:   T7.84, T8.17, T8.22
fixture-ok: yes
size:       M · frontier
owns:       internal/api/episodes.go, internal/api/playback_test.go, internal/api/routes.go,
             internal/api/routes_test.go, web/src/lib/api/types.ts, web/src/lib/api/testdata/routes.json,
             cmd/reprise/privacy.go, cmd/reprise/main.go, cmd/reprise/main_test.go,
             internal/identity/migrations/0003_display_name.sql, internal/identity/profile.go,
             internal/identity/profile_test.go, internal/privacy/share.go, internal/privacy/share_test.go
status:     done:fe79d31
```
The server half of the cover, link and author work, so the page tasks have what they show.

* **Owner cover.** `GET /api/episodes/{id}/cover` serves the stored cover PNG to the owner only, with
  `Cache-Control: private, no-cache`. A foreign episode, or one with no cover, answers 404. Covers
  are served today only behind a share token.
* **On the wire.** The episode detail and each row of `GET /api/episodes` carry `cover_path`, empty
  when there is no cover. The detail keeps `share_path`, from T7.80.
* **Display name.** Migration `0003_display_name.sql` adds `users.display_name TEXT NOT NULL DEFAULT ''`.
  `PUT /api/account/name` sets it for a signed-in user, and a guest gets 401. It is trimmed, at most
  60 characters, with no control characters. An empty name clears it. `GET /api/account` returns it.
* **Share payload.** Add `author`, the display name or empty. It never carries an address, so no
  share payload may ever contain `@`.
* Account deletion already removes the user row, so a test asserts the name goes with it.

**Done when:** Tests cover the cover route (owner, foreign user, no cover), `cover_path` on the list and
detail, the name limits and the guest refusal, and a share payload that never contains `@`. The gate
passes in a fresh worktree.

### T9.2: The responsive foundation
```yaml
requires:   T8.18
fixture-ok: yes
size:       M · mid
owns:       web/src/app.css, web/src/routes/+layout.svelte, web/src/lib/components/
status:     done:fcc4b2a
```
Extend T8.18's `app.css` with the layout decisions above.

* The `--gutter` and `--measure` tokens, and the `main` rule. Add a `.wide` modifier for the editor
  and a `.narrow` modifier for the share page.
* Fluid heading sizes. The `65ch` measure for `p`, `.sub` and the transcript.
* `.button` (primary), `.button.secondary` and `.button.quiet`, all at one height. A `.actions`
  group wraps on a monitor and stacks full width under `40rem`. The minimum tap target is 44px.
* `.card` with a square `.cover`, a title clamped to two lines, and a status slot pinned to the card
  foot. A `.cards` grid uses the auto-fill rule, with the row stretch that equalizes heights.
* `.section`: a heading and spacing, no border.
* `DemoBadge.svelte`: a small pill reading "Demo".
* Restyle `SeasonNav`, `GalleryLink` and `AccountLink` onto the shared classes. On a phone, the tab
  row scrolls sideways instead of wrapping.

**Done when:** A layout spec renders a sample page, built from these classes, at 375, 768, 1280 and
1920px wide. It finds no horizontal scroll, a `main` no wider than 1024px, equal card heights within
a row, and no button under 44px tall at 375px. The gate passes in a fresh worktree.

---

## Wave 2: one task per page

### T9.3: Record and processing
```yaml
requires:   T9.2
fixture-ok: yes
size:       S · mid
owns:       web/src/routes/record/+page.svelte, web/src/routes/processing/+page.svelte,
             web/src/lib/voice/record-state.ts, web/src/lib/voice/processing-state.ts
status:     done:66d644f
```
* Record uses the shared shell. It rendered as a white, unstyled page on 2026-09-29. Start is the
  one large primary button, centred, and the live view keeps the clock and End in reach on a phone.
* Remove the Microphone, Connection and Memory list. The sub line reads "Find a quiet spot. When
  you're ready, press Start."
* The processing steps become a compact list of four rows, each with a status word: "Uploading…",
  "Transcribing…", "Writing your draft…", "Ready". The sub line reads "We're getting your draft ready."
* Every notice in `record-state.ts` and `processing-state.ts` follows `voice.md`.

**Done when:** The layout checks from T9.2 pass on both pages at all four widths, the specs pass on the new
wording, and the gate passes in a fresh worktree.

**Decision, 2026-09-29.** Owns widened to
`web/src/routes/record/confirm-processing.spec.ts` and
`web/src/routes/record/mock-session.spec.ts`, the specs that pin the old
`{state}: {detail}` rendering. Round 1 returned 3 H stale-pin findings:
the status-word rows replace that rendering by design, same pattern as
T7.82 and T9.6. The remediation updates the asserts to the new named
notices and rows with no product change.

### T9.4: Gallery
```yaml
requires:   T9.1, T9.2
fixture-ok: yes
size:       S · mid
owns:       web/src/routes/+page.svelte, web/src/routes/threads/threads.ts,
             web/src/routes/threads/gallery-cards.test.ts, web/src/routes/gallery.spec.ts
status:     done:2146e03
```
* Cards on the shared `.cards` grid, all the same height. A card shows its cover when `cover_path`
  is set, and otherwise the "EP.12" tile.
* The header reads "Your episodes. Only you can see them until you publish." Demo mode shows the
  badge.
* A card's progress line reads like "Rendering… 75%", with no "Working" and no "survives a reload".
* Gallery notices in `threads.ts` follow `voice.md`.

**Done when:** The layout checks pass, a spec finds equal card heights and a cover image on a card with a
cover, and the gate passes in a fresh worktree.

**Decision, 2026-09-30.** Owns widened to
`web/src/routes/threads/threads.spec.ts`, which pins the old `Working`
card rendering at line 242. Round 1 returned 1 H stale-pin finding:
the plain progress line replaces that rendering by design, same
pattern as T7.82, T9.3 and T9.6. The remediation updates the assert
with no product change.

### T9.5: Episode page
```yaml
requires:   T9.4
fixture-ok: yes
size:       M · mid
owns:       web/src/routes/episode/[id]/+page.svelte, web/src/routes/threads/threads.ts,
             web/src/routes/threads/threads.test.ts, web/src/routes/threads/threads.spec.ts
status:     done:0867944
```
* The cover at the top, from `cover_path`. The eyebrow shows only "EP.12". The "Public" and
  "Private" line goes.
* Flat sections: player, chapters, transcript, release. The player stays one bordered card.
* **Release.** Unpublished shows Publish. Published shows one row: the full link, Copy link, and
  Revoke, with no sentence.
* **Erase.** A 202 goes to the gallery with "Episode erased." A 404 counts as already erased. Only
  another failure shows "Couldn't erase this episode. Try again."
* A draft with no audio reads "No audio yet." A failed episode shows one plain line, such as "This
  take couldn't be transcribed.", never the raw job error.
* Every episode notice in `threads.ts` follows `voice.md`.

**Done when:** The layout checks pass. Specs cover the cover image, the publish row with no sentence, and
erase on 202 and on 404 landing on the gallery. The gate passes in a fresh worktree.

### T9.6: Editor
```yaml
requires:   T9.2
fixture-ok: yes
size:       S · mid
owns:       web/src/routes/episode/[id]/edit/+page.svelte, web/src/lib/editor/draft.ts,
             web/src/routes/episode/[id]/edit/edit.spec.ts
status:     done:78ec2ca
```
* The `.wide` column above 1280px. The transcript is left-aligned, never justified, at `65ch`.
* The player controls and the proposal actions use the shared button kinds at one height.
* The proposal cards sit on one flat list, not boxes within boxes.
* Editor notices follow `voice.md`, including the Mark done states.

**Done when:** The layout checks pass, the edit suite passes in Chromium and Firefox, and the gate passes in
a fresh worktree.

**Decision, 2026-09-29.** Owns widened to `web/src/lib/editor/draft.test.ts`,
`web/src/lib/editor/draft.decisions.test.ts` and
`web/src/lib/editor/draft.prototype-keys.test.ts`, the specs that pin the old
wording. Round 1 returned 11 H stale-pin findings: the voice rewrite breaks
those exact-copy assertions by design, same pattern as T7.82. The remediation
updates the assertions to the new named notices with no product change.

### T9.7: Threads
```yaml
requires:   T9.5
fixture-ok: yes
size:       XS · mid
owns:       web/src/routes/threads/+page.svelte, web/src/routes/threads/threads.ts
status:     done:77a81be
```
* Flat sections. Each thread is one card, and its quotes are a plain list inside it, not cards
  within a card.
* The header reads "People and topics that keep coming up, and the moments you mentioned them."
* Threads notices follow `voice.md`.

**Done when:** The layout checks pass, and the gate passes in a fresh worktree.

### T9.8: Account and the share page
```yaml
requires:   T9.1, T9.2
fixture-ok: yes
size:       M · mid
owns:       web/src/routes/account/, web/src/routes/share/, web/src/routes/welcome/
status:     done:63972cd
```
* **Account and delete pages.** Use the shared shell and buttons, with a signed-in "Name on shared
  episodes" field saving through `PUT /api/account/name`. The copy follows `voice.md`.
* **Share page.** The `.narrow` column, with cover, title, "by {author}" when set, and the player.
  The footer is one line and one link: "Made with Reprise, a podcast of your own life, hosted by
  someone who remembers." The link, "Start your own", goes to the site root. No private or public
  wording. A dead link reads "This episode is no longer shared." and carries the same link.
* **Link previews.** Add `og:title`, `og:description`, `og:image` (the absolute cover URL),
  `og:type` = `website` and `twitter:card` = `summary_large_image`. A crawler runs no script, so if
  the static build cannot carry per-episode tags, say so in the handoff and name the server route
  that would inject them.
* **Welcome page.** Bring it onto the shared shell, with copy per `voice.md`.

**Done when:** The layout checks pass on all three routes. Specs cover "by {author}", no author line
when unset, and a footer link to `/` with no "private" or "public". The gate passes in a fresh worktree.

**Decision, 2026-09-29.** Owns widened to one line in
`web/src/lib/api/types.ts`: the `HttpMethod` union, which omits `PUT`
while the route table lists `PUT /api/account/name`. Round 1 proved
the failure predates this task (file byte-identical to base, fails
svelte-check there too) and belongs to T9.1's file. The remediation
adds `PUT` to the union with no runtime change, so the gate can pass.

---

## Wave 3

### T9.10: Gate irritants: runes scan and the Firefox memory probe
```yaml
requires:   none
fixture-ok: yes
size:       XS · light
owns:       web/src/routes/processing/+page.svelte, tools/check.sh,
             web/src/routes/record/mock-session.spec.ts
status:     done:f9abfe2
```
Two pre-existing gate reds no page task owns, both proved on pristine
bases during Wave 2 reviews.

* The Svelte 4 leakage scan (`tools/check.sh:131`) false-positives on
  valid Svelte 5 `$derived(expr)` at
  `web/src/routes/processing/+page.svelte:24`. Either rewrite that
  line to the behavior-identical `$derived.by(() => [...])` form the
  neighboring lines use, or narrow the scan so `$-prefixed` runes no
  longer match. Do not weaken the scan for real store `get(` calls:
  the T7.34 and T8.17 pins must keep passing.
* The record mock "memory stays flat" probe reads the Chromium-only
  `performance.memory`, so it fails on Firefox before any product
  code runs. Skip it on Firefox with `test.fixme` and the reason, or
  tag it Chromium-only. Chromium behavior stays pinned.

**Done when:** The gate text scans pass on the touched lines, the
memory probe passes in Chromium and skips with reason in Firefox,
and the gate passes in a fresh worktree past the code checks (the
ffmpeg env pin stays exogenous).

### T9.9: Copy and layout guards
```yaml
requires:   T9.3, T9.4, T9.5, T9.6, T9.7, T9.8
fixture-ok: yes
size:       S · mid
owns:       web/src/lib/copy.test.ts, web/tests/layout.spec.ts
status:     done:790276a
```
* `copy.test.ts` scans every `.svelte` file under `web/src/routes` and every exported string in the
  page controllers. It fails on `voice.md`'s banned words, on "No backend", "Scripted", "survives a
  reload", and on any sentence over 20 words shown to a person.
* `layout.spec.ts` opens every page in its demo mode at 375, 768, 1280 and 1920px. It finds no
  horizontal scroll, `main` no wider than the page's measure, equal card heights in each row, and
  no tap target under 44px at 375px.

**Done when:** Both guards pass. Restoring any one "Before" line from `voice.md` fails `copy.test.ts`.
The gate passes in a fresh worktree three times in a row, because the gate grew.

### T9.11: Plain words on the privacy and Google pages
```yaml
requires:   none
fixture-ok: yes
size:       XS · light
owns:       web/src/routes/privacy/+page.svelte, web/src/routes/account/google/+page.svelte
status:     done:45da0e0
```
Two true copy positives the T9.9 guard caught in pages no Wave 2 task owns.

* Rewrite the privacy page body and meta in plain words per `voice.md`: no render, stem, or provider wording, including the `aria-label` the guard now also scans. The page must still say what is kept and deleted.
* Fix the Google start-page meta description to match the page's own `SUB` ("Google confirms it is you").

**Done when:** `copy.test.ts` names no violation in either file, and the gate passes in a fresh worktree.

**Decision, 2026-09-30.** Owns widened to `web/src/routes/share/share.spec.ts:97`,
which asserts the exact banned line this task removes. Round 1 recorded it
as an out of scope H stale pin, same pattern as T7.82 and the Wave 2
widenings. The remediation asserts the new caveat line with no product
change.

### T9.12: Stale shell header pin
```yaml
requires:   T9.4
fixture-ok: yes
size:       XS · light
owns:       web/tests/shell.spec.ts
status:     done:dee9bd5
```
`shell.spec.ts:5` expects the pre-T9.4 gallery h1. T9.4 replaced that header by design.

* Update the pin to the landed header. No product change.

**Done when:** The shell spec passes, and the gate passes in a fresh worktree.

### T9.13: Scope the display-name migration out of the empty-DB login path
```yaml
requires:   T9.1
fixture-ok: yes
size:       S · frontier
owns:       internal/identity/migrations/
status:     done:68e2ace
```
`TestLoginMigrationsApplyOnEmptyDatabase` fails: the login migration set applies the users-table ALTER from `0003_display_name.sql` on a database with no diary tables (`no such table: users`). T9.1 proved the boot path, not the login set alone.

* Make the login set apply cleanly on an empty database, with 0003 still altering real deployments. Keep the backfill behavior for existing rows.
* Pin both: empty-DB login set applies, and a production-schema copy still gains the column with backfilled defaults.

**Done when:** `go test ./internal/identity/migrations/` passes, and the gate passes in a fresh worktree.

### T9.14: Stale gallery header pins in the record link spec
```yaml
requires:   T9.4
fixture-ok: yes
size:       XS · light
owns:       web/src/routes/record/gallery-link.spec.ts
status:     in-progress:land:t9.14-impl@4806e37d01578d66d905b287d3256869e63407c3
```
T9.4 replaced the gallery h1 by design, and the record link spec still
asserts the old header in two helpers (`followGalleryLink` and the
 Held-navigation helper around line 172). CI on the P9 landing run
failed exactly those 10 tests and nothing else.

* Assert the landed h1 (`Your episodes`) in both helpers. No product
  change.

**Done when:** The record mock suite passes in Chromium and Firefox,
and the gate passes in a fresh worktree.

---

## Exit criteria

- [ ] No page renders unstyled, and every page shares one column rule.
- [ ] Every page passes the layout checks at 375, 768, 1280 and 1920px.
- [ ] No on-screen string names the machinery, per `voice.md`.
- [ ] The owner reviews every page on a phone and a monitor, and signs it off.

---

## Handoff log

### What exists now

T9.9 landed at 790276a after round 2 APPROVE with zero residue.
The copy guard scans routes plus controllers (aria labels included)
and the layout spec pins all 14 fixture pages at four widths. Round
1 had 2 M guard defects; the remediation added the inline-word
exemption and the aria scan with no page changes. Reviewed commits
ac65f50 and 764ca69 rebased clean. Copy 14/14 and layout 14/14 pass
on the landed tree with T9.11's pages. Gate proof in a fresh
worktree: `check.sh` exits 1 three times in a row, all at the
exogenous ffmpeg env pin (host 9.0.2 vs pinned 9.0.1) before any
code stage. Past the pin, Go packages, 426 unit tests,
svelte-check, eslint, build, and layout plus shell specs all pass.
T9.11 landed at 45da0e0 after round 2 APPROVE with zero residue.
Privacy and Google start copy speak plain words, and the share pin
asserts the rewritten caveat. Round 1 had one out of scope H on
that pin; owns widened to the spec by decision and the remediation
updated the assert with no product change. Reviewed commits 3f48a73
and a0704f5 rebased clean. Pins pass on the landed commit.
T9.12 landed at dee9bd5 after round 1 APPROVE with zero findings.
The shell spec pins the landed gallery header. Reviewed commit
893da8d rebased clean. Pin passes on the landed commit.
T9.13 landed at 68e2ace after round 1 APPROVE with zero findings.
The login migration set stages the users table before the display
name ALTER, so empty databases apply cleanly and diary copies
backfill. Reviewed commit 34cdca3 rebased clean. Race tests pass
on the landed commit.
T9.7 landed at 77a81be after round 1 APPROVE with zero findings.
Threads renders flat cards with plain quote lists and the agreed
header as its sub line, matching the gallery split. Reviewed commit
5e13fd3 rebased clean. Pins pass on the landed commit.
T9.10 landed at f9abfe2 after round 1 APPROVE with zero findings.
The processing step list uses the scanner-clean `$derived.by` form
with identical behavior, and the memory probe skips on Firefox with
reason while staying pinned on Chromium. Reviewed commit 48e554e
rebased clean. Pins pass on the landed commit.
T9.5 landed at 0867944 after round 2 APPROVE with zero residue.
The episode page shows the cover with a bare eyebrow, flat
sections, a sentence-free release row, gallery-bound erase, and
plain failure lines. Round 1 had 1 L finding on the unpinned cover
branch; the remediation pinned it in the specs with no product
change. Reviewed commits 1577ca5 and 6c810a1 rebased clean. Spec
pins pass on the landed commit.
T9.4 landed at 2146e03 after round 2 APPROVE with zero residue.
The gallery rides the shared card grid with covers and plain
progress lines. Round 1 had 1 H stale-pin finding on the threads
spec pinning the old card wording; owns widened to that spec by
decision and the remediation updated the assert with no product
change. Reviewed commits 69f8538 and 6d94628 rebased clean. Spec
pins pass on the landed commit.
T9.8 landed at 63972cd after round 2 APPROVE with zero residue.
Account, share and welcome ride the shared shell with the display
name field, the byline, and the one-line footer. The account shell
pin now asserts the new measure, closing the interim red. Round 1
had one out of scope H on the mirrored PUT type; owns widened to
that line by decision and the remediation fixed it with no runtime
change. Reviewed commits 8620d0b and b26bc07 rebased clean. Spec
pins pass on the landed commit.
T9.3 landed at 66d644f after round 2 APPROVE with zero residue.
Record and processing ride the shared shell with a centred Start,
phone-reachable clock and End, four status-word rows, and plain
notices behind named constants. Round 1 had 3 H stale-pin findings
on specs pinning the old rendering; owns widened to those two spec
files by decision and the remediation updated the asserts with no
product change. Reviewed commits c99a918 and 91a8102 rebased clean.
Record mock pins pass on the landed commit.
T9.6 landed at 78ec2ca after round 2 APPROVE with zero residue.
The editor rides the wide column with a flat proposal list, shared
buttons, and plain-words notices. Round 1 had 11 H stale-pin
findings on specs pinning the old wording; owns widened to those
three spec files by decision and the remediation updated the
assertions with no product change. Reviewed commits e395fed and
97b71ce rebased clean. Unit pins pass on the landed commit.
T9.1 landed at fe79d31 after round 1 APPROVE with zero findings.
The owner cover route, cover paths on list and detail, the display
name account routes, and the share author all ride the wire with
the golden and the browser mirror carrying the three new routes.
Reviewed commit 346c65a rebased clean. Race tests pass on the
landed commit.
T9.2 landed at fcc4b2a after round 1 APPROVE with zero in-scope
findings. The responsive column, one-height buttons, equal-height
cards, flat sections, tabs and the Demo badge live in app.css.
Reviewed commit b61190f rebased clean. Unit and layout pins pass
on the landed commit. One out of scope H stays open under T9.8:
account.spec.ts pins the old 52rem shell, so the gate stays red
until T9.8 updates that pin to the new measure.

### What surprised us

`threads.ts` holds the gallery, episode and Threads controllers together, so their three page tasks run in
sequence (T9.4, then T9.5, then T9.7). The theme tokens lived in the gallery page's own style block, which is
why Record rendered bare.

### Notes for the next developer

Read `voice.md` before writing any string a person will see.
