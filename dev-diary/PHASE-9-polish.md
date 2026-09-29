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
status:     not-started
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
status:     in-progress:land:t9.2-impl@b61190fcc99644c8b2554f15611886caa36610ae
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
status:     not-started
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

### T9.4: Gallery
```yaml
requires:   T9.1, T9.2
fixture-ok: yes
size:       S · mid
owns:       web/src/routes/+page.svelte, web/src/routes/threads/threads.ts,
             web/src/routes/threads/gallery-cards.test.ts, web/src/routes/gallery.spec.ts
status:     not-started
```
* Cards on the shared `.cards` grid, all the same height. A card shows its cover when `cover_path`
  is set, and otherwise the "EP.12" tile.
* The header reads "Your episodes. Only you can see them until you publish." Demo mode shows the
  badge.
* A card's progress line reads like "Rendering… 75%", with no "Working" and no "survives a reload".
* Gallery notices in `threads.ts` follow `voice.md`.

**Done when:** The layout checks pass, a spec finds equal card heights and a cover image on a card with a
cover, and the gate passes in a fresh worktree.

### T9.5: Episode page
```yaml
requires:   T9.4
fixture-ok: yes
size:       M · mid
owns:       web/src/routes/episode/[id]/+page.svelte, web/src/routes/threads/threads.ts,
             web/src/routes/threads/threads.test.ts, web/src/routes/threads/threads.spec.ts
status:     not-started
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
status:     not-started
```
* The `.wide` column above 1280px. The transcript is left-aligned, never justified, at `65ch`.
* The player controls and the proposal actions use the shared button kinds at one height.
* The proposal cards sit on one flat list, not boxes within boxes.
* Editor notices follow `voice.md`, including the Mark done states.

**Done when:** The layout checks pass, the edit suite passes in Chromium and Firefox, and the gate passes in
a fresh worktree.

### T9.7: Threads
```yaml
requires:   T9.5
fixture-ok: yes
size:       XS · mid
owns:       web/src/routes/threads/+page.svelte, web/src/routes/threads/threads.ts
status:     not-started
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
status:     not-started
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

---

## Wave 3

### T9.9: Copy and layout guards
```yaml
requires:   T9.3, T9.4, T9.5, T9.6, T9.7, T9.8
fixture-ok: yes
size:       S · mid
owns:       web/src/lib/copy.test.ts, web/tests/layout.spec.ts
status:     not-started
```
* `copy.test.ts` scans every `.svelte` file under `web/src/routes` and every exported string in the
  page controllers. It fails on `voice.md`'s banned words, on "No backend", "Scripted", "survives a
  reload", and on any sentence over 20 words shown to a person.
* `layout.spec.ts` opens every page in its demo mode at 375, 768, 1280 and 1920px. It finds no
  horizontal scroll, `main` no wider than the page's measure, equal card heights in each row, and
  no tap target under 44px at 375px.

**Done when:** Both guards pass. Restoring any one "Before" line from `voice.md` fails `copy.test.ts`.
The gate passes in a fresh worktree three times in a row, because the gate grew.

---

## Exit criteria

- [ ] No page renders unstyled, and every page shares one column rule.
- [ ] Every page passes the layout checks at 375, 768, 1280 and 1920px.
- [ ] No on-screen string names the machinery, per `voice.md`.
- [ ] The owner reviews every page on a phone and a monitor, and signs it off.

---

## Handoff log

### What exists now

Nothing yet. The phase opened on 2026-09-29.

### What surprised us

`threads.ts` holds the gallery, episode and Threads controllers together, so their three page tasks run in
sequence (T9.4, then T9.5, then T9.7). The theme tokens lived in the gallery page's own style block, which is
why Record rendered bare.

### Notes for the next developer

Read `voice.md` before writing any string a person will see.
