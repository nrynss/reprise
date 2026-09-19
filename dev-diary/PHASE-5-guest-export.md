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
status:     done:a5def2931c8813d19cb5ab00226293b85ef880be
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
requires:   T1.4, T4.1, T4.2
fixture-ok: yes
size:       M · frontier
owns:       internal/seed/, data/season/
status:     in-progress:implement:t5.2-impl
```
The shipped catalog starts empty. This task builds copy, receipt, and drop. It does not wait on
episodes. The operator places one or two finished episodes in `data/season/` after the box is
up, as dogfood. A test fixture pins copy and drop. That fixture does not ship.

Each new guest, and each new account with no season, gets a copy when the catalog has rows. A
returning user never gets a second copy. Sign-up from a guest keeps the copies they already
have, because the user id does not change. The owner does not receive a copy.

**Build on:** `keel/sqlite` and `keel/mediastore`. An empty catalog ships inside the image.

* An empty catalog copies nothing and writes no receipt. After the operator adds files, a user
  with no receipt and no season gets the copy on the next list.
* Catalog media lives once. User copies are diary rows (`seeded = true`) plus mentions and the
  planted callback, so T4.2 still has a stored row to open on once files exist.
* The catalog belongs to one reserved user this package inserts, with kind `seed`, never a
  guest. T5.4 already skips non-guest owners.
* Copy runs on the first request that lists their season, never inside session mint. A receipt
  is written only when at least one row is copied. Dropping the copies does not clear the
  receipt, so the seed does not come back.
* The catalog file names each stable key. Copies set `seeded = 1`. Do not add columns to the
  diary schema. Raise a contract change if an episode column is required.
* User delete of a seeded episode drops that user's copy rows only. Catalog blobs and other
  users stay. Recorded episodes (`seeded = 0`) still take the T4.4 erase path. If the episode
  erase entry point must branch, raise a contract change. Do not edit privacy files from this
  task.
* One query finds every seeded row by the flag. An operator remove deletes the catalog and
  stops new copies. Existing user copies stay until the user drops them or the guest sweep
  takes them.

**Done when:** With an empty catalog, a new guest sees no seeded episodes and can record.
With a fixture catalog, a new guest sees those rows as their own, with ids that differ from a
second guest's copies. Dropping a seeded episode removes it from that user and leaves the
catalog and the other guest intact, including catalog audio. A later list for the same user
does not re-import. A new empty account gets a copy when the catalog has rows. The owner does
not. T4.2's callback still resolves on a copied mention once files exist.

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
status:     done:53922ffd6f175ef6a5c0d6ccdea880f96d8374ba
```
**Build on:** `keel/erase` for the fan-out and `keel/job` for the kind that runs it. Reprise names
the targets, which are the same ones T4.4 registers.

Guest data expires after 90 days, and expiry deletes rather than hides. The sweep does not wait
on a catalog. An empty season, or a guest with only their own recordings, is enough.

* The sweep runs as a job kind, so it resumes and retries like every other durable delete.
* A guest is expired by its own last-seen time, read at sweep time rather than stamped on arrival.
* A guest whose episode the owner has kept is not swept, because the owner's copy is the owner's.
* The retention window comes from settings, so it changes without a deploy. Ninety days is the
  current value.
* Seeded copies, when a catalog exists later, belong to the guest and sweep with them. The
  reserved seed user is not a guest, so the catalog stays.

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

The landing screen explains nothing it can show instead. With no seeded copies it offers one
button to record. With copies it plays thirty seconds of the latest seeded episode, then offers
one button to record the next one.

**Done when:** A new guest with an empty catalog reaches a live session in two clicks. Playwright
confirms the button with no seed, and WebKit plays after the first gesture when copies exist.

---

## Exit criteria

- [ ] An export package carries audio, video, captions, cover and a chapter description.
- [ ] A fresh container lands a guest on an empty season, or on their copy if a catalog exists.
- [ ] The kill switch, guest cap and spend ceiling each refuse correctly, and an honest guest still
      records.
- [ ] Expired guest data is gone, and unexpired guest data survives the same sweep.

---

## Handoff log

### What exists now (Orchestrator-2)
T5.2 is unblocked and not started. The shipped catalog starts empty. Copy, receipt, and drop
do not wait on episodes. The operator places catalog files after first deploy, as dogfood.
T5.4 already landed. The sweep does not wait on a catalog. Retention is 90 days.
T5.3 landed after round 1 APPROVE with zero in-scope findings. Guest caps, kill switch, and the
Keel-backed spend view are on main with both sides pinned. The owner login stays an explicit
stub seam (`StubOwnerAuth` denies all) until the open login decision lands. Opened T1.7 below
for the route mounting the round 1 review records as out of scope.
T5.4 landed (Orchestrator-2) after round 2 APPROVE with zero residue against round 1. The
90-day retention sweep deletes expired guests with owner-kept episodes surviving, and retries
read expiry fresh. Round 1 fixed the frozen-cutoff retry. Retention window decided at 90 days
by the owner this session.
T5.1 landed (Orchestrator-2) after round 2 APPROVE with zero residue against round 1. AAC
master, 1080p waveform video, validated captions, and chaptered descriptions bundle per
episode. Round 1 isolated the zero-pin, halved the duration window with zero flakes, and fixed
the error docs.

### What surprised us
Nothing yet.

### Notes for the next developer
The shipped catalog starts empty. First deploy does not wait on seed files. Copy writes a
receipt only when rows are copied, so a later catalog still reaches users with no season.
User drop removes that user's rows only. Catalog audio stays. The owner does not receive a
copy. T5.4 sweeps guests with or without seeded copies. Retention is 90 days.
