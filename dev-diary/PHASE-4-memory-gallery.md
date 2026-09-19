# P4: Memory and gallery

```yaml
id:       P4
size:     L
requires: [T3.5]
blocks:   P6
parallel: partly
runs-parallel-with: P5
```

**Goal:** Make the memory real. The index across episodes, the callback it plants, the gallery that
shows it, and the delete that ends it.

---

### T4.1: Memory index ★
```yaml
requires:   T3.5
fixture-ok: yes
size:       M · frontier
owns:       internal/memory/
status:     in-progress:implement:t4.1-impl
```
Derive threads from `mentions` in SQL.

* **People and places that recur.** Grouped by normalised name, with every episode and quote.
* **Commitments.** Things the user said they would do. Gemini marks candidates from the rendered
  transcript with exact quotes. A commitment counts only if its quote exists in `words`.
* **Resolution.** A later mention of doing the thing closes it, with that quote as evidence.
* **Circled topics.** Key phrases in three or more episodes with no resolution.
* **Keyterms.** The recurring names, ranked for T2.2.

A spoken count such as "three times in 34 days" is a query result, never model output.

**Done when:** On the seeded season, the queries return the planted threads. A commitment whose quote
is not in `words` is rejected.

---

### T4.2: Callback selection ★
```yaml
requires:   T4.1, T3.2
fixture-ok: yes
size:       S · frontier
owns:       internal/memory/callback.go
status:     not-started
```
Choose what the next episode opens on.

* Prefer an open commitment over a recurring person, and a recurring person over a circled topic.
* Prefer the editorial model's planted callback when it agrees with the data.
* Never pick the same callback twice in a row.
* Store the choice in `callbacks` with its source mention.

**Done when:** On the seeded season, episode five's greeting names the conversation from episode one,
with the right episode number.

---

### T4.3: Gallery and thread panel ★
```yaml
requires:   T4.1, T1.3
fixture-ok: yes
size:       L · mid
owns:       web/src/routes/+page.svelte, web/src/routes/episode/[id]/+page.svelte,
            web/src/routes/threads/
status:     not-started
```
**Mockup:** the `gallery`, `episode` and `threads` views ([`mockup`](mockup), `#/gallery`,
`#/episode`, `#/threads`). The state lab carries the rendering, ready and draft card states.

**Build on:** Chaaya's `JobStream` for live progress, `AudioPlayer` for playback, its transcript
playback sync for the episode transcript, and `testing` for both gates.

* **Gallery.** Episodes newest first, with cover, title, state and duration. A running job shows live
  progress on its card, which is how a guest who walked away from the processing screen sees the same
  work. The card and T2.4's screen read one job through `JobStream`, never two mechanisms.
* **Episode.** Player, chapters, show notes, and a transcript that follows playback.
* **Threads.** People, open commitments and circled topics, each linking to the moment it was said.

No sentiment gauges, no charts, no scores. The thread panel shows quotes and links, nothing else.

**Done when:** Playwright opens a thread item and playback starts at its quote. Progress never moves
backwards across a reload. The pages pass `a11yGate` and `contrastGate`.

---

### T4.4: Publish and erase ★
```yaml
requires:   T1.4, T3.5
fixture-ok: yes
size:       S · frontier
owns:       internal/privacy/, web/src/routes/share/, web/src/routes/privacy/
status:     in-progress:remediate-r2:t4.4-rem-r1
```
**Mockup:** the publish gate and the erase confirmation on `#/episode`, the public page the share
link opens at `#/share`, and the `privacy` view ([`mockup`](mockup)).

**Publishing that renders nothing is not publishing**, so the share route lands here with the token
that opens it. It serves the render and the cover to a signed-out visitor and nothing else: no
stems, no transcripts, no threads, and no route to the owner's other episodes.

**The privacy page is the erase story in plain words**, so it lands here too, where the erase runs.
It states what is kept, what is deleted, and that the provider's own deletion is soft, which
`project.md` fixes the wording for.

**Build on:** `keel/mediastore` visibility, `keel/id` for share tokens, and `keel/erase` for the
fan-out, which retries every target until it confirms and survives a restart. Reprise registers the
targets, because the set of places a recording lands is this product's. It does not own the fan-out.

* **Publish** creates an unguessable share token and marks the render and cover public. Stems,
  transcripts and threads stay private always.
* **Unpublish** revokes the token and returns the media to private.
* **Erase** removes every row, stem, render and cover. It deletes the AssemblyAI session and every
  remaining batch transcript.

**Done when:** **Publish passes:** a published share link serves the render and the cover to a
signed-out visitor from a workstation, while the stems, transcripts and threads behind it still
return 404. **Erase passes:** after erase, SQLite holds no content row, the media directory holds no
file, and the provider APIs return not found or soft-deleted for each id, while a second owner's
episode is untouched, pinned row by row. **Refuses:** an unpublished share link returns 404 from a
workstation. A restart mid-erase resumes and finishes.

---

## Exit criteria

- [ ] Threads come from stored quotes only.
- [ ] Episode five's greeting calls back to episode one on the seeded season.
- [ ] The gallery shows the thread, not a dashboard.
- [ ] Erase leaves nothing behind locally and clears the provider copies it can.

---

## Handoff log

### What exists now
Not started.

### What surprised us
Nothing yet.

### Notes for the next developer
Nothing yet.
