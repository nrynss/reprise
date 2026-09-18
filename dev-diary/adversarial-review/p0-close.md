# P0 close review

Reviewed head: `064ab8518f025b7bdc7fe1d9bf7f3a247135ea5f` on `main`.
Worktree: `/home/nryn/work/reprise-wt/p0-close`, detached at that head.
The hash was confirmed with `git log -1` before any check ran.
Every check below ran offline. No live probe ran and no spend occurred.
No image was built. Nothing was committed.

The delta from `fa40820` to this head touches one P2 status line only.
Every P0 status line reads the same under both heads.

## Tasks

### T0.1: Repository and checks

Status `done:a02280b68dc30d469ef9f1a841270cbdfdd9c673` exists on `main`.
Round 2 returned APPROVE with zero findings at that same hash.
The landed tree holds 24 files, all inside the owns lines.
The round file records three cold gate passes and the container probes.
This task is clean.

### T0.2: Voice Agent probe

Status `done:b3fc58aab1e9e5818d0598e2033cd68bb9b8ef1b` exists on `main`.
Round 1 returned APPROVE with zero in-scope findings.
The reviewed hash was `b5246abf1ebde4b02d3d4569228f51a691b2ddab`.
The owned paths read identical between reviewed and status hashes.
The diff over `live_test.go`, `voice-agent.md`, `project.md`, and
`testdata/speech/` is empty. Later orchestrator landings moved the base.
The measured content did not move. The `project.md` cap update follows
the task text. The `doc.go` duplicate resolved at landing per the handoff.
This task is clean.

### T0.3: Gemini access probe

Status `done:5ab25cad6d12d68bbd9480fe5c3255fb2287bba2` exists on `main`.
Round 2 returned APPROVE with zero residue against round 1.
The reviewed hash was `63438174233e189439f1f6589e15b15f22934138`.
The owned paths read identical between reviewed and status hashes.
The `doc.go` file and the `genai v1.71.0` pin are recorded exceptions.
The record holds the credential route, the inline size result with token
counts and usage fields, and the listening answer. This task is clean.

### T0.4: AssemblyAI batch probe

Status `done:c87e596a29b542f8428ee61ad4d490aab3218b2e` exists on `main`.
No round file exists. The handoff log discloses the skipped loop in words.
The record carries raw JSON responses, per-word timestamp errors, entity
and phrase shapes, the gateway model `qwen3.5-4b-32k-fast`, the soft DELETE
shape, and the polling choice over webhooks. The owns exception for
`doc.go` is recorded in the same handoff entry. This task passes under
the disclosed-landing rule named in the close brief.

### T0.5: Recorded fixtures

Status `done:d84fc8cd059f1dffc8a19f89c413721234d8fe2e` exists on `main`.
Round 2 returned APPROVE with zero residue at
`a150012312dc78bd3b2f24e31e2e662fe37b3db2`.
`testdata/sessions/` reads identical between approved and status hashes.
All 18 README hashes were recomputed here with zero mismatches.
`ffprobe` reads all six wavs and all three recordings with zero warnings.
Steady, pauses, and barge-in shapes match the README numbers.
The recreate script writes the guest clips offline and names the live step
honestly. This task is clean.

### T0.6: Repair the speech fixtures

Status `done:14d5b3cbdc2f2cefb5a1b9699be45b0b9a791822` exists on `main`.
Round 1 returned APPROVE with zero findings at
`53656ca6697e9795db604cb6d669329f3e7e6c36`.
`testdata/speech/` reads identical between reviewed and status hashes.
Both wavs declare true header sizes with payloads byte-identical to the
measured bytes. Both pcm files stand untouched. `ffprobe` reads clean at
3.58 and 3.98 seconds. All three pins sit inline in `generate.sh`.
This task is clean.

### T0.7: Pin the media tools

Status `done:4d2c372a3c12a0dd702e1be93b2e58729af70a70` exists on `main`.
No round file exists in `adversarial-review/` or anywhere in history.
Commit `9ed53c3` says the work landed in the main checkout with no
worktree and no review round. The handoff log never mentions T0.7.
The landing edits `deploy/README.md`, which sits outside the T0.7 owns
lines and inside done T6.1 ground. See findings F1 and F2.

## Exit criteria

All six boxes read `[x]` on `main` at the reviewed head.

1. Image plus triple-green gate. Evidence is historical: T0.1 round 2 ran
   three cold passes with exit 0 each time. This close did not rebuild
   the image or rerun the full gate. The Go surface is green below.
2. Facts have measurements. Every facts-table row traces to a probe
   record. Token bounds, the socket upgrade, inline config, keyterms,
   24 kHz audio, explicit ending with the resume window, the Sessions
   API, and metered spend each appear in `voice-agent.md`. Timestamps,
   entities, and chapters appear in `batch.md`. Credentials, inline size,
   and listening appear in `gemini.md`.
3. Routes decided. The file credential route sits in `gemini.md`. The
   gateway chapter route and the polling result route sit in `batch.md`.
4. Three sessions. Steady, pauses, and barge-in hold stems, stereo
   recordings, event logs, timelines, and metadata. Hashes match 18 of 18.
5. Scripted fixtures. `generate.sh` and `recreate.sh` sit beside their
   bytes. Eleven audio files were probed here with zero warnings.
6. CI plus digest pin. The workflow and the Dockerfile name one digest.
   `check.sh` refuses any other version by name. The workflow never ran
   because the repository has no remote. This holds on file evidence only.

## Audit

`dev-diary/tools/audit_docs.py` exits 0 at the reviewed head.
It checks requires ids, status values, handoff headings, links, and refs.

## Offline gate

`gofmt -l` over `cmd` and `internal` reports nothing.
`go vet ./...` exits 0. `go test ./...` passes all 12 packages with no
live tag. The full `check.sh` did not run here. It needs `npm ci`,
browser installs, and fetched tools. Live tests did not run because they
spend money. Docker synthesis did not run per the brief.

## Orphan scan

No P0 claim lacks an owner. The AssemblyAI adapter path moves to T3.1.
The shared Gemini client path moves to T3.2, which owns the whole
`internal/gemini/` directory. The `genai` pin sits in T0.1 owned `go.mod`
as a recorded exception. The deploy media text sits in T6.1 owned
`deploy/`. The later sweep appendix in `voice-agent.md` came from a task
outside P0 and lives outside this verdict.

## Ten questions, phase scope

1. Unreserved spend: no. Probes ran behind the live tag with caps.
2. Open sessions: no. Every probe ends with `session.end` plus caps.
3. Private data exposure: no. Fixtures use generated speech only.
4. Irreversible edits: no. Landings are commits with revertible diffs.
5. Invented callbacks: no. Counts come from stored rows and files.
6. Plan cites in code: no. Scans skip planning paths by design.
7. Rebuilt library code: no. Rendering and config ride Keel and Chaaya.
8. Own worktree and hash: yes. Detached head confirmed before reading.
9. Double-spend resume: no. No P0 job kind exists.
10. Human or device checks: no. Every pin reads fixed files or exact text.

## Findings

| Severity | Where | What | Pin | Mutation |
|---|---|---|---|---|
| H | `dev-diary/PHASE-0-ground.md`, T0.7 status line at the reviewed head, plus the missing `dev-diary/adversarial-review/t0.7-round*.md` | T0.7 landed with no review round. The loop lets no task skip a round. The handoff log never records this landing. The gate edit lacks the triple-green evidence the protocol demands for gate changes | `ls` for `t0.7-round*` plus `git log --all --diff-filter=A` for the same pattern both return empty while status reads `done:4d2c372` | A placeholder round file with no APPROVE verdict still fails this pin because the pin requires the verdict text with a zero-residue claim, not presence alone |
| L | `deploy/README.md` through commit `4d2c372a3c12a0dd702e1be93b2e58729af70a70` | That commit edits a path outside T0.7 owns. `deploy/` belongs to done T6.1. The words are correct, so this is process only | `git show --name-only 4d2c372` lists `deploy/README.md` while T0.7 owns names only the workflow, the Dockerfile, and `check.sh` | Moving that hunk into a T6.1 scoped commit clears the pin without changing one word |

## Verdict

HOLD. Six tasks verify clean with hashes on `main`, matching owns, and
APPROVE rounds or the one disclosed T0.4 exception. T0.7 carries two
in-scope findings above. To close: run T0.7 through a review round with
the triple-green gate evidence, widen its owns line or move the README
hunk into owned ground, and record both steps in the handoff log. This
review changed nothing and committed nothing.
