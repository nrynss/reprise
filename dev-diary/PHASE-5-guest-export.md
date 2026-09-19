# P5: Guest mode and export

```yaml
id:       P5
size:     M
requires: [T3.4, T4.1]
blocks:   P6
parallel: yes
runs-parallel-with: P4
```

**Goal:** A visitor understands Reprise in ninety seconds, and a finished episode goes to YouTube as
it is.

---

### T5.1: YouTube export
```yaml
requires:   T3.4, T3.5, T3.6
fixture-ok: yes
size:       S · mid
owns:       internal/export/
status:     in-progress:review-r2:t5.1-rev-r2@68d9eeb261c73de271e39299c2f260670f6ffe97
```
**Build on:** `keel/caption` for the SRT and WebVTT files, `keel/waveform` for the video, and
`keel/ffmpeg` for the audio. Reprise supplies the word timings and the still. It writes neither
format itself.

One download with exactly what YouTube's upload form asks for.

* The AAC audio.
* An MP4 of the cover with a waveform over it, at 1920 by 1080.
* Captions as SRT and WebVTT from the rendered words. `keel/mediastore` already accepts both types.
* A description with show notes, then chapters as `0:00 Title` lines.
* The cover at 1:1.

**Done when:** `ffprobe` shows the MP4 at 1920 by 1080 with audio matching the render's duration. The
captions parse in a validator. The description's chapter lines follow YouTube's rules: the first at
0:00, at least three, each at least 10 seconds.

---

### T5.2: Seeded season ★
```yaml
requires:   T4.1, T4.2
fixture-ok: yes
size:       L · frontier
owns:       internal/seed/, data/season/
status:     blocked:seeded season voice and sharing decisions in project.md
```
Four finished episodes a visitor lands on. Episode one plants the dread. Episode four still has not
resolved it. Episode five, recorded by the visitor, opens on it.

**Build on:** `keel/sqlite` and `keel/mediastore`. Exporting and importing finished artifacts is
Reprise's, because the fixtures are this product's own content.

* Each seeded row carries `seeded = true` and a stable key, so an import re-runs safely and one query
  finds everything seeded.
* The season ships inside the image, so a fresh deploy already has it.
* The seeded episodes go through the real pipeline once. Their proposals, renders, analyses and
  mentions are real outputs, exported as fixtures.
* A remove command deletes every seeded row and blob.

**Done when:** A fresh container shows four episodes to a new guest. Removing the seed leaves no
seeded row. The season's threads drive T4.2's callback.

---

### T5.3: Guest limits and kill switch ★
```yaml
requires:   T2.1, T1.4, T1.5
fixture-ok: yes
size:       S · mid
owns:       internal/limits/, web/src/routes/admin/
status:     done:55d2e30d3c56899605e545dea518dda045097cea
```
**Mockup:** the `admin` view ([`mockup`](mockup), `#/admin`), and the two refusals it produces,
both on `#/preflight` in the state lab: recording paused, and guest limit reached.

**Build on:** `keel/flag` for the switches, `keel/cost/sqlitestore` for the global and per-owner
ceilings, and the limits in settings from T1.5. Reprise declares which flags exist and what the admin
page does with them.

* A cap on sessions per guest and on session length, both read from settings.
* A daily global spend ceiling through the Keel budget store.
* A kill switch that stops minting tokens at once, without a restart.
* A small admin page behind the owner's login to flip it and see today's spend.

**Today's spend is read from Keel, not from a Reprise table.** The keyed budget answers per owner:
the ceiling from `SetLimit`, the headroom from `Remaining`, and spend is the difference. The ceiling
is a daily one, so that difference is today's spend. Keel's ledger carries no timestamp on a charge,
so a history across days is not available and this page does not claim one.

**Done when:** Both sides are pinned, because a limiter that refuses everyone satisfies the refusals
alone. **Passes:** with the switch off and a guest under the cap, a session starts, and the admin page
shows a spend figure that matches the reservation the session settled. **Refuses:** flipping the
switch refuses the next session with `sessions_paused`, and a guest past the cap gets `guest_limit`.
Retention is T5.4's.

---

### T5.4: Guest retention sweep ★
```yaml
requires:   T1.4, T4.4
fixture-ok: yes
size:       S · frontier
owns:       internal/retention/
status:     in-progress:land:t5.4-rem-r1@326a2e6960206951f5d9010435ed0eb994f59c6a
```
**Build on:** `keel/erase` for the fan-out and `keel/job` for the kind that runs it. Reprise names
the targets, which are the same ones T4.4 registers.

Guest data expires after the retention the owner chooses, and expiry deletes rather than hides.

* The sweep runs as a job kind, so it resumes and retries like every other durable delete.
* A guest is expired by its own last-seen time, read at sweep time rather than stamped on arrival.
* A guest whose episode the owner has kept is not swept, because the owner's copy is the owner's.
* The retention window comes from settings, so it changes without a deploy.

**Done when:** Both sides are pinned, because a sweep that deletes everything satisfies the deletion
alone. **Deletes:** an expired guest's rows and media are gone, and its provider copies return not
found or soft-deleted, pinned the way T4.4 pins an erase. **Keeps:** every unexpired guest's rows and
media survive the same run, pinned by count before and after, and a kept episode survives its guest's
expiry. A restart mid-sweep resumes and finishes.

---

### T5.5: First visit
```yaml
requires:   T5.2, T2.4, T4.3
fixture-ok: yes
size:       M · mid
owns:       web/src/routes/welcome/
status:     not-started
```
**Mockup:** the `welcome` view ([`mockup`](mockup), `#/welcome`).

**Build on:** Chaaya's `AudioPlayer`, whose first gesture unlocks playback.

The landing screen explains nothing it can show instead. It plays thirty seconds of episode four,
then offers one button: record episode five.

**Done when:** A new guest reaches a live session in two clicks. Playwright confirms the seeded
episodes and the button, and WebKit plays after the first gesture.

---

## Exit criteria

- [ ] An export package carries audio, video, captions, cover and a chapter description.
- [ ] A fresh container lands a guest on a four-episode season.
- [ ] The kill switch, guest cap and spend ceiling each refuse correctly, and an honest guest still
      records.
- [ ] Expired guest data is gone, and unexpired guest data survives the same sweep.

---

## Handoff log

### What exists now (Orchestrator-2)
T5.3 landed after round 1 APPROVE with zero in-scope findings. Guest caps, kill switch, and the
Keel-backed spend view are on main with both sides pinned. The owner login stays an explicit
stub seam (`StubOwnerAuth` denies all) until the open login decision lands. Opened T1.7 below
for the route mounting the round 1 review records as out of scope.

### What surprised us
Nothing yet.

### Notes for the next developer
Nothing yet.
