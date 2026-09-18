# Reprise: status and phase graph

How work runs lives only in [`../AGENTS.md`](../AGENTS.md). This file holds facts, not rules.

- [`../product.md`](../product.md): the product.
- [`project.md`](project.md): the build, and every departure from the product spec.
- [`libraries.md`](libraries.md): what Keel and Chaaya carry, and what Reprise builds.

**Current status (Orchestrator-2):** Built on Keel `v0.3.0` and Chaaya `0.2.0`. T0.1, T1.5, T1.6, T1.1, T1.6a, T1.2, T1.3 and T1.4 are
done. P1 foundations are complete. T0.2 and T0.4 are probing live against the AssemblyAI key (other orchestrator).

The libraries carry the general work. Chaaya `0.2.0` carries the live duplex path the recorder needs:
capture on a context the page owns, a take that streams instead of accumulating, a filtered
resampler, a PCM stream player with a flush, a session guard, and the transcript module. Keel
`v0.3.0` carries the server side: budgets per owner, runtime flags, captions, erasure fan-out, the
two media recipes, the paid call seam and session leases. Reprise wires them and builds what is
specific to this product. [`libraries.md`](libraries.md) lists both surfaces.

Sizes were re-estimated on 2026-09-18 against what the libraries carry. Nine tasks were written when
Reprise was going to build the resampler, the stream player, the transcript editor, the erasure
fan-out, the renderer and the captions itself. Six got smaller and four dropped from frontier to
mid.

---

## Phase graph

| Phase | Document | Requires | Runs parallel with | Blocks |
|---|---|---|---|---|
| **P0** Ground and probes | [PHASE-0-ground.md](PHASE-0-ground.md) | none | none | everything |
| **P1** Foundations | [PHASE-1-foundations.md](PHASE-1-foundations.md) | T0.1 | none | P2 to P5 |
| **P2** Live session | [PHASE-2-live-session.md](PHASE-2-live-session.md) | P1, T0.2 | P3 | P4 |
| **P3** Post-production | [PHASE-3-post-production.md](PHASE-3-post-production.md) | P1, T0.3, T0.4, T0.5 | P2 | P4, P5 |
| **P4** Memory and gallery | [PHASE-4-memory-gallery.md](PHASE-4-memory-gallery.md) | T3.5 | P5 | P6 |
| **P5** Guest mode and export | [PHASE-5-guest-export.md](PHASE-5-guest-export.md) | T3.4, T4.1 | P4 | P6 |
| **P6** Ship | [PHASE-6-ship.md](PHASE-6-ship.md) | P4, P5 | none | submission |

```text
  P0 ──▶ P1 ──┬──▶ P2 live session ─────────┐
              │     broker, host prompt,    │
              │     voice path, alignment   ├──▶ P4 memory, gallery ──┐
              │                             │                         ├──▶ P6 ship,
              └──▶ P3 post-production ──────┴──▶ P5 guest, export ────┘    real voices
                    edit transcript, editorial,
                    editor, render, analysis
```

P3 can start on fixtures before P2 records anything, because T0.5 records sessions from generated
speech.

---

## Screens and the mockup

[`mockup/`](mockup) holds the design: an interactive prototype of all eleven views and an infinite
canvas of the same screens with the design system. It is the one part of `dev-diary/` that is
committed, so it opens on any machine that clones the repository.

**Serve it over HTTP.** A `file://` load cannot route between views. From `dev-diary/mockup`, run a
static server and open the prototype from it. Every view has a deep link, and the state lab in the
bottom right carries the states that view can reach.

| View | What it shows | Task that owns the route |
|---|---|---|
| `#/welcome` | First visit, the seeded season, one gesture to play | T5.5 |
| `#/preflight` | Microphone, connection and memory checks; both refusals | T2.4, refusals from T5.3 |
| `#/live` | On air: timer, turn-taking, levels, barge-in, the end gate | T2.4 |
| `#/processing` | Upload, transcription, editorial pass, draft ready | T2.4, drawing T3.1 and T3.2's jobs |
| `#/editor` | Transcript, proposed cuts with reasons, revert, cold open | T3.3 |
| `#/episode` | A finished episode, publish gate, erase, export control | T4.3; publish and erase T4.4; export T5.1 |
| `#/gallery` | The season, with a live job drawn on the card | T4.3 |
| `#/threads` | The memory loop across episodes | T4.3 |
| `#/admin` | Spend, runtime flags, guest limits | T5.3 |
| `#/privacy` | What is kept, what is deleted, in plain words | T4.4 |
| `#/share` | What a visitor sees behind a share link | T4.4, which also mints the token |

**Every route has an owner.** The three that had none were assigned on 2026-09-18. The processing
screen went to T2.4, because it belongs to the session that just ended. The share page and the
privacy page went to T4.4, because publishing that renders nothing is not publishing, and the
privacy page is the erase story in plain words.

**The processing divergence is settled.** The screen is for the session you just finished and the
gallery card is for one you walked away from. Both read the same job through `JobStream`. T2.4 and
T4.3 each say so, so neither implementer builds half of it.

---

## Where the product earns its score

`product.md` names the demo: play the episode where the host calls back. These tasks make that
moment.

| Beat | Task |
|---|---|
| The host opens on something from an earlier episode | T4.2, T2.2 |
| The episode sounds like a podcast, not a call | T2.4, T3.4 |
| The name episode one mangled is right by episode four | T4.1, T2.2 |
| The episode has a cold open, chapters and show notes | T3.2, T3.5 |
| A visitor sees it work without recording four episodes | T5.2 |
