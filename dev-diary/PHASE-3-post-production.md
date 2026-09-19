# P3: Post-production

```yaml
id:       P3
size:     L
requires: [P1, T0.3, T0.4, T0.5]
blocks:   P4, P5
parallel: partly
runs-parallel-with: P2
```

**Goal:** Turn two stems into an episode worth playing untouched. The model proposes, the user
disposes, and the render is what everyone hears.

**Why it can start before P2:** `testdata/sessions/` holds recorded stems. Every task here runs
against them offline.

**Job kinds.** Every long step is a `keel/job` kind. A step that calls a paid API is not idempotent,
so a restart marks it `interrupted` and the episode `failed` rather than paying twice. A step that
spends nothing may resume.

| Kind | Limit | Idempotent | Why |
|---|---|---|---|
| `edit_transcript` | 2 | no | A batch transcription costs money |
| `editorial` | 2 | no | A Gemini call costs money |
| `render` | 1 | yes | Pure ffmpeg, named by an input hash, so a rerun reuses the output |
| `analysis` | 2 | no | A batch transcription and LLM Gateway call cost money |
| `cover` | 1 | no | An image call costs money |

---

### T3.1: Edit transcript ★
```yaml
requires:   T1.1, T0.4
fixture-ok: yes
size:       M · mid
owns:       internal/assemblyai/batch.go, internal/transcript/
status:     done:0e3d5bdf102f9718dbdc4040c07b1dbf7079efb7
```
**Mockup:** the `processing` view ([`mockup`](mockup), `#/processing`) draws this step and the
ones around it, and its state lab replays a transcription failure. No task owns that route yet.

Build one word timeline on the episode clock for the editor.

* Batch-transcribe the **user stem** with Universal-3.5 Pro. Keyterms come from `mentions`.
* Take the host's words from `transcript.agent.delta` timings, offset by each reply's start on the
  host stem.
* Merge both into `words` with source `edit`, shifted by each stem's alignment offset.
* Persist the provider response on receipt. Delete the provider transcript once stored.
* Reserve budget before the call and settle after, per invariant 6.

**Done when:** On fixture sessions, every word lands within 50 ms of the generated clip's recorded
boundary. The provider transcript is gone after the job, confirmed with a `GET`.

---

### T3.2: Editorial proposals ★
```yaml
requires:   T3.1, T0.3
fixture-ok: yes
size:       L · frontier
owns:       internal/gemini/, internal/editorial/
status:     done:f05fea6cd43059bb4daa62aa683a4f21c196bdfa
```
One Gemini request hears both stems and reads the word timeline. It returns proposals that point at
word ids, validated against a JSON schema.

| Proposal | Rule |
|---|---|
| Cold open | One range of 10 to 20 seconds. Chosen on delivery, a laugh, a pause, a voice going thin. |
| Cuts | False starts, dead tangents, lost threads. Each with a one-line reason. |
| Title | Short, specific to this episode. |
| Show notes | A few sentences in the user's words. |
| Callback | One unresolved thing to open next time, with its quote. |

* Proposals land in `proposals`. Cuts default to accepted with a `decisions` row, so zero edits still
  gives a good episode.
* A proposal pointing at a word that does not exist is dropped and logged.
* If the model fails, the episode still renders with no cuts and a plain title.
* The model id comes from settings, never from code.

**Build on:** `internal/gemini` from T0.3, with the credential route it chose.

**Done when:** On fixture sessions, every proposal references real words. A forced model failure
still produces a renderable draft. The listening test from T0.3 picks the same cold open here.

---

### T3.3: Editor ★
```yaml
requires:   T3.2, T1.3
fixture-ok: yes
size:       M · mid
owns:       web/src/routes/episode/[id]/edit/, web/src/lib/editor/
status:     in-progress:implement:t3.3-impl
```
**Mockup:** the `editor` view ([`mockup`](mockup), `#/editor`), including the revert affordance
and the cold-open preview.

**Build on:** Chaaya `0.2.0` throughout.

Its `transcript` module carries the timed words, the ranges, the cuts with their reasons and the
revert in `TranscriptEditor`. `TranscriptFollower` carries the word under the playhead and click to
seek, and `regionsFromCuts` carries the waveform marks. Then `AudioPlayer` for playback and seeking,
`computePeaksInWorker` for the peaks, `JobStream` for render progress, and `testing` for both gates.

This task owns the screen, not the editing behaviour. Keyboard operation and accessible names come
from the library, so a gap in either is a Chaaya finding rather than a workaround here.

* Proposed cuts show as strikethrough ranges with their reason. Each one reverts with one action.
* The cold open plays as a preview from its range.
* Clicking a word seeks playback. The word under the playhead highlights.
* The waveform marks removed ranges.
* "Mark done" confirms, then starts the render. It is the only way to leave the draft.
* Every control works by keyboard and has an accessible name.

**Done when:** Playwright reverts a cut and the decision row appears. A keyboard-only run completes
the edit and marks done. The page passes `a11yGate` and `contrastGate`.

---

### T3.4: Render ★
```yaml
requires:   T3.2
fixture-ok: yes
size:       M · mid
owns:       internal/render/
status:     in-progress:implement:t3.4-impl
```
**Build on:** `keel/edl` for the cut list, the crossfades and the two-pass loudness, `keel/ffmpeg`
underneath it, and the `render` job kind. This task owns the edit model and the job, not the
rendering.

* Inputs: both stems, alignment offsets, accepted cuts, the cold open. Their hash names the render.
  The same hash reuses the existing render, which is what makes the kind safe to resume.
* Remove each accepted range from both stems together, with a 10 ms crossfade at every boundary.
* Place the cold open first, then a short gap, then the episode.
* Mix the stems. Normalise to -16 LUFS integrated, -1 dBTP, in two passes.
* Write Opus for streaming and AAC for export, persisted private in `keel/mediastore`.

**Done when:** `ffmpeg -af ebur128` on the fixture render reads -16 LUFS within 1 LU. No cut boundary shows a click: `astats` peak deltas at every
boundary stay under the largest delta the same render shows away from a boundary, so the bar comes
from the material rather than from a number somebody picked. Rendering twice reuses the first
output, and a restart mid-render resumes to the same hash.

---

### T3.5: Analysis ★
```yaml
requires:   T3.4, T0.4
fixture-ok: yes
size:       M · mid
owns:       internal/analysis/
status:     not-started
```
A `keel/job` of kind `analysis` on the **rendered** file.

* Batch-transcribe the render into `words` with source `rendered`.
* Summarization, entity detection and key phrases on the same request.
* Chapters through LLM Gateway over the rendered transcript. The first starts at 0:00, every chapter
  lasts at least 10 seconds, and there are at least three when the episode allows.
* Entities and key phrases become `mentions` with their rendered word offsets.
* Persist on receipt. Delete the provider transcript once stored.

**Done when:** Every chapter timestamp, seeked to in the rendered fixture, lands on the words it
names, checked against the generated clip's recorded boundaries. The provider transcript is gone.

---

### T3.6: Cover art
```yaml
requires:   T3.2, T0.3
fixture-ok: yes
size:       S · mid
owns:       internal/cover/
status:     in-progress:implement:t3.6-impl
```
One square image per episode from the title and show notes. No faces and no text in the image. The
app sets the title, not the model. If generation fails, a plain generated cover from the episode
number takes its place.

**Done when:** Every fixture episode has a 1:1 cover. A forced failure produces the fallback.

---

## Exit criteria

- [ ] A fixture session becomes a ready episode offline, with every proposal pointing at real words.
- [ ] The untouched render measures -16 LUFS and has no clicks at cuts.
- [ ] Chapter timestamps land on the rendered audio.
- [ ] Provider transcripts are deleted after storage.
- [ ] No paid job runs twice across a restart.

---

## Handoff log

### What exists now (Orchestrator-2)

T3.1 landed after round 3 APPROVE with zero residue against rounds 1 and 2. The batch client plus
the merge, store, and budgeted `edit_transcript` job build the episode-clock word timeline. Landing
took two remediations: the receipt error names the transcript id, the wire body is a named struct,
and the batch client is `BatchClient` after a rebase collision with the landed token client. The
50 ms bar runs on a generated clip plus probe shapes until fixture sessions land for a live
remeasure. The other orchestrator's PHASE-0 dirt was present through this landing and left
untouched.

T3.2 landed after round 3 APPROVE with zero residue against rounds 1 and 2. One Gemini request
hears both stems and returns validated proposals on stable word offsets, with accepted cuts,
stored callbacks, and a renderable fallback on model failure. Three settle-before-return fixes
cover every post-spend error path. Landing survived a self-inflicted orphan scare: worktrees were
removed before a fast-forward that failed on my own newer commits, the tip was recovered from the
object store, rebased again with a green gate, and only then fast-forwarded and cleaned up. Never
remove worktrees before the fast-forward succeeds.

### What surprised us
Nothing yet.

### Notes for the next developer
Nothing yet.
