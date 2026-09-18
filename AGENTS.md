# Reprise: agent protocol

Binding for every human and coding agent working in this repository. It governs how work runs.
The product lives in [`product.md`](product.md). How the build departs from it, and why, lives in
[`dev-diary/project.md`](dev-diary/project.md). The work breakdown lives in
[`dev-diary/`](dev-diary/).

Read this file in full before you change anything. **This is the only file that says how work runs.**
No other document repeats these rules. If one ever does, this file wins and the copy gets deleted.

## The loop

Every task runs through the same cycle. Implement, review, remediate, re-review. Repeat until a
round returns APPROVE with zero findings across all severities. No task skips a round.

1. **Implement.** The orchestrator marks the task in progress with its worktree, then dispatches an
   implementer. In that worktree the implementer edits only the paths in the task's `owns` line,
   commits, and hands back the commit hash.
2. **Review.** The orchestrator updates the status with the review worktree and the hash, then hands
   a fresh reviewer that hash. The reviewer reviews that commit and nothing else, and writes its
   review file.
3. **Remediate.** On REMEDIATE, the orchestrator updates the status with the remediation worktree,
   then dispatches a remediator. It fixes every finding as new commits on the reviewed commit, writes
   its remediation file, and hands back the new hash.
4. **Re-review.** Steps 2 and 3 repeat with fresh agents until a round returns APPROVE, with an
   explicit claim of zero residue against every prior round, severity by severity.
5. **Land.** The orchestrator lands the approved commit on `main`, removes the worktrees, and marks
   the task done with the landed hash. It then records the landing in lambo, as "Memory" below
   describes.

"Task status" below defines every status the orchestrator writes.

**Severities.**

| Level | Meaning |
|---|---|
| **C** | Breaks the demo, loses a recording, leaks spend, or exposes a private episode. |
| **H** | A real defect the demo survives. |
| **M** | A real defect with a workaround. |
| **L** | Polish. |

**Every severity gets fixed.** "It is only an L" does not close a finding. A reviewer may record a
finding as a false positive. That judgement decides whether the defect is real, never whether a real
defect deserves a fix.

### The loop cannot see what no task owns

A reviewer reads a commit against a specification. A claim that no task owns appears in no commit,
so no reviewer ever reads it. Run `dev-diary/tools/audit_docs.py` at every phase close, once T0.1
lands it. Fix the doc or write the task. Never close a finding by deleting the claim.

A reviewer who finds a real defect outside the task's `owns` paths records it as `OUT_OF_SCOPE`,
with its severity, its pin, and the path that owns it. It never blocks an APPROVE. The orchestrator
scopes that path into a task or opens one.

## Task status

The orchestrator alone writes a task's `status` line, in its phase file in the main checkout. It
writes the new status **before** each handoff, never after. An orchestrator resuming after a crash
reads the status lines to find every live worktree, so a handoff the status does not record is lost
work.

| Status | Written when |
|---|---|
| `not-started` | The task exists and nothing has run |
| `in-progress:implement:<worktree>` | Just before dispatching the implementer |
| `in-progress:review-r<K>:<worktree>@<hash>` | Just before handing a reviewer round K's hash |
| `in-progress:remediate-r<K>:<worktree>` | Just before dispatching a remediator after round K |
| `in-progress:land:<worktree>@<hash>` | Just before rebasing and landing the approved hash |
| `done:<hash>` | After `main` carries the landed hash and every worktree is removed |
| `blocked:<reason>` | The task cannot proceed, with the reason in words |

`<worktree>` is the directory name under `/home/nryn/work/reprise-wt/`, such as `t2.1-rev-r1`.
`<hash>` is the full commit hash.

- **One line, the present only.** A status says what is running now. The review and remediation
  files hold the history.
- **Every worktree on disk appears in exactly one status.** At the start of every session the
  orchestrator runs `git worktree list` and compares. A worktree with no status, or a status whose
  worktree is gone, is resolved before any new dispatch.
- **Only a live status names a worktree.** `done` and `blocked` name none, because none should exist.

## Files a round produces

Every file lives in `/home/nryn/work/reprise/dev-diary/adversarial-review/`.

| File | Written by |
|---|---|
| `t<N.M>-impl-handoff.md` | The implementer. What it built, what surprised it, any contract change it needs. |
| `t<N.M>-round<K>.md` | The round K reviewer. The verdict, the counts, and one row per finding. |
| `t<N.M>-remediation-round<K>.md` | The round K remediator. One row per finding it fixed. |
| `p<N>-close.md` | The phase close reviewer. |

A review file opens with the hash it reviewed and the worktree it ran in. Every finding carries five
columns.

| Column | Meaning |
|---|---|
| **Severity** | C, H, M or L. |
| **Where** | File path and line number at the reviewed hash. |
| **What** | The observable defect or false claim. |
| **Pin** | A failing test or probe that measures the defect independently. |
| **Mutation** | The one or two line edit that reintroduces the defect. It proves the pin is load-bearing. |

A finding without a pin is not actionable. A finding without a mutation is not load-bearing.

Every review also answers these questions, in its file, before the verdict.

1. Does any path spend money without reserving budget first?
2. Can any path leave an AssemblyAI session open without `session.end` or a token cap?
3. Can a private episode, stem or transcript reach anyone but its owner?
4. Does any edit apply without a revertible record?
5. Does any spoken callback or count come from the prompt instead of a stored row?
6. Does any code, comment, test or migration cite a phase, a task, or a planning file?
7. Did this task rebuild something Keel or Chaaya provides?
8. Was this review run on the handed hash, in a worktree of its own?
9. Can any paid job kind resume after a restart and spend twice?
10. Does any development check need a person, a microphone, or a wall-clock threshold?

## Commits and worktrees

**Every writer works in its own git worktree and commits.** Two agents editing one checkout clobber
each other, and neither can tell its own change from a sibling's. Keel and Chaaya both lost rounds
to exactly that. A commit is the only handoff between roles.

### Who writes where

| Role | Works in | Writes | Hands over |
|---|---|---|---|
| **Orchestrator** | The main checkout at `/home/nryn/work/reprise` | `main`, phase file status lines, handoff logs | A commit hash to each reviewer |
| **Implementation** | A fresh worktree on a task branch | Code inside `owns`, one or more commits | The final commit hash |
| **Review** | A fresh detached worktree at the handed hash | Its review file only | A verdict |
| **Remediation** | A fresh worktree on the task branch at the reviewed hash | Fixes as new commits | The new commit hash |

**Nobody but the orchestrator touches the main checkout.** Not to edit, not to run the gate, not to
build. A process that writes `node_modules`, `.svelte-kit` or a test cache into the main checkout is
a clobber too.

### The commands

The orchestrator opens each worktree, so paths and branches stay predictable.

```bash
git -C /home/nryn/work/reprise worktree add -b task/t2.1 /home/nryn/work/reprise-wt/t2.1-impl main
```

```bash
git -C /home/nryn/work/reprise worktree add --detach /home/nryn/work/reprise-wt/t2.1-rev-r1 <hash>
```

```bash
git -C /home/nryn/work/reprise worktree add -b task/t2.1-rem-r1 /home/nryn/work/reprise-wt/t2.1-rem-r1 <hash>
```

Worktrees live under `/home/nryn/work/reprise-wt/`, outside the repository, so no tool that walks
the tree ever reaches one.

### Rules each role keeps

- **Implementation and remediation commit before they report.** An uncommitted change does not
  exist. The report is the hash plus a one-line summary, never a description of files to go and read.
- **A commit passes the gate in its own worktree first.** Run `npm ci` in the worktree's `web/`, then
  `./tools/check.sh`. A worktree shares git objects, never `node_modules` or build caches, so the
  gate there measures a clean tree.
- **A reviewer reviews the hash it was handed.** It runs `git log -1` to confirm the hash, reads the
  diff against the task's base with `git diff <base>..<hash>`, and runs every pin inside its own
  worktree. It never commits code and never reviews a working tree.
- **Remediation builds on the reviewed commit.** It never rebases or amends what a reviewer saw, so
  the next reviewer can diff the remediation alone.
- **Landing is the orchestrator's alone.** After APPROVE it rebases the task branch onto current
  `main` in the task worktree. If anything moved, it reruns the gate there, and a conflict goes back
  to a remediator as a new round. Then it fast-forwards `main`, pushes, and removes every worktree and
  branch the task opened.
- **One task, one landing.** Two approved tasks never land in one commit, and one task's work never
  rides along in another's.
- **Two orchestrators share main through git, never through side files.** No gitignored flag, lock
  file, or out-of-band signal coordinates orchestrators. An uncommitted marker is invisible in
  every other clone and worktree, and check-then-act on it races. The status lines plus `main`
  itself are the shared state.
- **Re-read before every handoff.** Before writing a status line, dispatching any agent, or
  landing, the orchestrator runs `git log --oneline -1`, `git status --short`, and
  `git worktree list`, then re-reads the task's status line. If the head, the tree, or the line
  moved under it, it stops and reconciles before touching anything.
- **Land serially through rebase.** The rebase-then-fast-forward is the atomic step. Whoever lands
  second resolves the conflict. A dirty file your own status lines do not own is another
  orchestrator's work. Stage and commit only your own files, leave theirs dirty, and keep going.
  Note their dirt in your handoff log so the next session knows it was there and untouched.

### Planning files stay out of worktrees

`AGENTS.md`, `product.md` and `dev-diary/` are gitignored, so a worktree does not contain them. Every
agent reads them at their absolute paths under `/home/nryn/work/reprise/`.

- Reviewers, implementers and remediators write only the round files named in "Files a round
  produces". The file name carries the task and round, so two agents never share one.
- The orchestrator alone edits phase files, status lines, handoff logs and `libraries.md`.
- Probes live under `/home/nryn/work/reprise/dev-diary/probes/<agent>/`. A probe stays while any open
  task or review round cites it.

## Four roles, kept separate

| Role | Does | Never does |
|---|---|---|
| **Orchestrator** | Picks the task. Opens worktrees. Hands hashes to reviewers. Gates the loop and refuses to advance on a dirty verdict. Lands the commit. | Implement a task. Write a verdict. Decide a finding is not worth fixing. |
| **Implementation** | Builds the task inside its `owns` paths in its worktree, and commits. Raises a contract change in its handoff file instead of reaching outside. | Review its own work. Mark the task done. Touch the main checkout. |
| **Review** | Reviews the handed commit in its own worktree. Writes the verdict, the severity counts, and the four columns. | Fix anything it found. Review uncommitted work. Soften a finding because the fix looks expensive. |
| **Remediation** | Fixes every finding as new commits on the reviewed commit. Writes one row per finding. | Change the verdict. Rewrite the reviewed commit. Fix things nobody found. |

**Use a fresh agent per role per round.** Neither reviewer may have implemented the task.

### The orchestrator's one exemption

The orchestrator may fix an L finding directly, as its own commit on the task branch, with no
remediation round. All four conditions must hold. The finding is an L. The fix cannot change
behaviour. The review file records it by name and file. Nothing about it is arguable.

A comment that misdescribes behaviour is not an exempt doc defect. It carries the severity of the
behaviour it misdescribes.

## A pin is a measurement, never a log line

Software reports its own success. That report is the thing under review, not evidence for it.

- **Audio.** Run `ffprobe`, `ebur128` or `astats` on the file that was written. A render's own log
  proves nothing.
- **Alignment.** Cross-correlate the local stems against AssemblyAI's stereo session recording.
- **Spend.** Read connected seconds from AssemblyAI's Sessions API, not from the browser's timer.
- **Privacy.** Request a private episode's media with `curl` and no session cookie. Read the
  `Cache-Control` header.
- **Deletion.** Query the SQLite file, list the media directory, and call the provider's API after a
  delete.
- **Public URLs.** Verify from a workstation, never from the Hetzner box. Cloudflare challenges
  requests from the box's own address.

### A clean tree is the baseline

A warm cache hides what a fresh checkout shows. The gate is judged in a fresh worktree after
`npm ci`. A change to the gate itself is not verified until a fresh worktree passes it three times in
a row, with the three exit codes in the review file.

### Testing audio

1. **Logic lives outside the browser wrappers.** Resampling, chunking, offsets, alignment maths and
   cut boundaries are pure functions with exact assertions on synthetic samples.
2. **No development check listens to a device or a person.** A browser check feeds a signal the page
   generates, with markers it can find again. A live provider probe streams generated speech.
3. **No wall-clock tolerance.** A check compares against a fixed input, or against a ratio read from
   one clock in one run.
4. **The measuring tool runs wherever the check runs.** CI installs pinned `ffmpeg` and `ffprobe`. A
   missing tool fails the gate by name.

A case that breaks a rule is quarantined with `test.fixme` and a reason that states the defect. A
phase task owns restoring it. Quarantine is never how a task passes its own review.

## Task shape and dispatch

Tasks live in `dev-diary/PHASE-*.md`. Each one carries this block.

```yaml
requires:   T1.1, T1.5
fixture-ok: yes
size:       M · frontier
owns:       internal/broker/
status:     not-started
```

- **requires** binds. Reprise task ids only. A library is a pinned version, never a task: Keel
  `v0.3.0` and Chaaya `0.2.0` carry what this plan builds on, and how they came to carry it belongs
  to their repositories, not to this one.
- **A shared path is a dependency, not a scheduling hint.** Where two tasks write one path, the later
  one names the earlier in `requires`, even when their logic is unrelated.
- **Task ids follow the dependency waves.** A task never requires a higher id, and tasks that may
  start together hold a contiguous block of ids.
- **fixture-ok** says whether the task can start against fixtures before its real upstream exists.
- **size** estimates the work, from XS to XL. **class** says which agent to send.
- **owns** lists the paths this task writes. No two concurrent tasks may own the same path, even in
  separate worktrees, because they meet again at landing.
- **status** takes one of the values in "Task status". Only the orchestrator changes it.

**Phase-level requires is advisory. Task-level requires is binding.** Start a task when the tasks in
its `requires` line are done. Do not wait for a whole phase to close.

**Task ids** read `T<phase>.<n>`. A task that needs a person or production appends `b`, as in
`T6.4b`. A split appends a letter, as in `T3.3a`. A number is never reused.

**Where documents disagree**, `dev-diary/project.md` wins over a phase file. The orchestrator records
the discrepancy in that phase's handoff log.

### Handoff logs

Every phase file ends with a handoff log under three headings. The orchestrator updates it when a
task lands, from the round files.

- **What exists now.** The facts a newcomer needs, not the history.
- **What surprised us.** Anything the specification got wrong or left out.
- **Notes for the next developer.** What they should not work out again.

An undocumented landing is an unfinished task. A landing that surfaced something Keel or Chaaya
should own says so in the handoff, and the capability becomes a task in that library's own plan.

### Agent class

| Class | Send it for |
|---|---|
| **frontier** | A decision others build on. Audio, spend or privacy verified by measurement. A specification that needs interpreting. |
| **mid** | A reference to port, or a specification precise enough to follow literally. |
| **light** | Mechanical, low ambiguity work that the test shipping with it verifies. |

### Live work

A probe that needs a real key and real money, but no person, runs in development behind the `live`
build tag. It never runs in CI.

Work that needs a person, a real voice, or a physical device runs once, at the end, as T6.4b. No
development task may require it.

## No phase or task names in code

Code and comments never cite the plan. That means no task ids (`T2.1`), no phase ids (`P3`), no
`PLAN.md`, `project.md`, `product.md` or `handoff.md` references, no `§` marks, no numbered
invariants, and no review rounds. The same holds for test names, error strings, SQL migrations and
commit messages. A branch name may carry a task id, because a branch is not a tracked file and the
orchestrator deletes it at landing.

A comment states its reason itself.

```go
// Wrong:
// Batch runs on the rendered file (product.md §2).

// Right:
// Batch runs on the rendered file. Transcribing the raw take would shift every chapter
// timestamp by whatever the edit removed.
```

`tools/check.sh` fails on these patterns in tracked source files.

## Keel and Chaaya first

Reprise builds on two libraries. Reprise never grows its own copy of what they provide.

- **What they provide is recorded.** `dev-diary/libraries.md` lists what Keel and Chaaya carry, and
  what Reprise builds itself. Read it before writing anything general.
- **Keel is pinned.** `github.com/nrynss/keel` at `v0.3.0`, public, fetched from the module proxy.
- **Chaaya is `@nrynss/chaaya` from npm**, pinned to `0.2.0` exactly in `web/package.json`.
- **A missing capability is written in the library**, as a task in that library's own plan, through
  its own loop. Reprise files no asks and keeps no request list. Build it here only when it is
  specific to this product, this provider or this deployment, and say which in the task.
- **Never patch a library from here.** A library change runs through that library's own loop.
- **Never edit an existing project.** Other repositories on this machine are read-only.

### A bug or a gap in a library becomes a GitHub issue

A task that meets a bug in Keel or Chaaya, or a piece that is missing from either, raises it where
that library's maintainers read, not in a Reprise file nobody there opens. A bug is the more urgent
of the two: a gap blocks one task, while a defect in a released version is already in every consumer
that installed it. Report it even when Reprise can work around it, and even when the workaround is
one line.

- **The implementer writes it up in its handoff file first**, with the version it hit, the smallest
  reproduction it can state, and what it expected. A claim with no reproduction is not a report.
- **The orchestrator files the issue**, with `gh issue create --repo nrynss/keel` or
  `--repo nrynss/chaaya`, and puts the issue URL in the handoff file and the task's notes. Only the
  orchestrator works outside a worktree, and an issue is outside every worktree.
- **Write it in the library's own terms.** A reproduction that needs Reprise's code is a bug report
  nobody can run. Reduce it to the library's API, its own fixtures, and the published version.
- **The task does not stop.** If a workaround exists, take it, and record it in the handoff with the
  issue URL beside it, so whoever removes the workaround knows what has to ship first. If no
  workaround exists, the task goes `blocked:` with the issue URL as the reason.
- **Never fix it here.** Not a patch, not a vendored copy, not a local fork. The library's own loop
  reviews library code, and a fix that skips it is unreviewed work in a consumer.

## Architectural invariants

Breaking one is a plan change, not an implementation detail. Propose it in a handoff file first.

1. **`internal/assemblyai` is the only code that talks to AssemblyAI.** `internal/gemini` is the only
   code that talks to Gemini.
2. **Configuration loads once, in `main`, through `keel/config`.** Packages take a settings struct
   and never read the environment.
3. **Consumers declare interfaces. Producers return concrete types.**
4. **Nothing blocks longer than 100 seconds.** Long work runs as a `keel/job` and reports over
   `keel/stream`.
5. **The API key never reaches the browser.** The server mints a single-use AssemblyAI token per
   session.
6. **Every paid call reserves budget first.** A refused reservation stops the call before it starts.
7. **Every session ends explicitly.** The browser sends `session.end` on the end control, on
   `pagehide` and on destroy. The token's duration cap stops a session the browser abandons.
8. **Analysis runs on the rendered audio, never the raw take.**
9. **The model proposes. The user disposes.** No edit applies without a revertible record.
10. **Private by default.** Publishing is an explicit action per episode.
11. **Callbacks and counts come from data, never from invention.** The host's opening cites a stored
    mention with its episode and offset. A spoken count such as "three times in 34 days" is a query
    result. The prompt only phrases it.
12. **Provider output persists on receipt.** Artifact URLs expire.
13. **A paid job never reruns on its own.** Its `keel/job` kind is not idempotent, so a restart marks
    it `interrupted`.
14. **An episode always ships.** It renders with no cuts and a plain title when the editorial model
    fails.
15. **Seeded history is marked, removable and baked in.** Every seeded row carries a flag and a stable
    key, one query finds all of it, and the season ships inside the image.

## Stack, frozen

- **Backend:** Go 1.27.1, standard library first, on Keel `v0.3.0`. `google.golang.org/genai` for
  Gemini, imported only by `internal/gemini`. AssemblyAI over plain `net/http`.
- **Frontend:** Svelte 5 with runes, SvelteKit with the static adapter, served by the Go binary.
  TypeScript strict. Node 26. npm. Chaaya for behaviour. Bits UI for primitives.
- **Media:** ffmpeg as a runtime binary, through `keel/ffmpeg`.
- **Host:** one distroless container behind Traefik on the Hetzner box, at `reprise.nryn.dev`.

### Svelte 5 only, never mixed

Use runes: `$state`, `$derived`, `$props`, `$effect`. Never write `export let`. Never write `$:`.
Never use a store to drive reactivity. A Svelte 4 idiom compiles clean and then never reacts.

## Go style, enforced

- No `**T`. No pointer to a slice, map, channel, func, or interface. `any`, never `interface{}`.
  Wire shapes are named structs, never `map[string]any`.
- `ctx context.Context` comes first on anything that does I/O. Never store it in a struct.
- Never return a non-nil value alongside a non-nil error. No naked returns.
- Every exported identifier has a doc comment that starts with its name.
- No `panic` or `log.Fatal` outside `func main`.
- One sentinel per condition. Wrap with `%w`. Never match a provider's error text.
- Every goroutine has a defined exit path. Fan-out uses `errgroup` with `SetLimit`.
- `go test -race ./...` is mandatory. A race is a C.

## Documentation style

Four rules. They cover markdown, code comments, and commit messages alike.

1. No semicolons. Split the sentence.
2. No em dashes. Use a full stop, a comma, or parentheses.
3. Sentences run 30 words at most.
4. Active voice, unless passive genuinely reads clearer.

## Secrets

Secrets are references in a TOML file, resolved by `keel/config`. No secret value appears in any
tracked file. Locally they resolve from `/home/nryn/work/reprise/.env`, mode `0600`. On the box they
resolve from `/etc/reprise/env`, root-owned, mode `0600`. Never export one in an interactive shell,
because it lands in `~/.zsh_history`.

## Memory

Lambo is the graph memory every session here shares. Several agents work this repository at once, so
the graph is the only place one session learns what another decided. Every role uses it, the
orchestrator included.

**No lambo, no obligation.** If your environment exposes no lambo MCP server, skip this section and
work from this file. It holds everything you need. The rest of this section binds every agent that
has the tools.

**Recall before you read anything else.** Call `lambo_recall` before the read order below, before you
open a file, and before you search the filesystem. The graph records what other agents actually did
and why. The source files record only the result.

**Recall again before you touch shared ground.** Reconfiguring infrastructure, renaming or deleting a
resource, or opening a new workstream each start with a recall. A warning about blast radius means
other work depends on that thing. Name the dependents in your handoff instead of proceeding.

**Use one stable `agent_id` for the whole session.** Name the model you are running as, such as
`claude-opus-5`. Never invent an id per task or per topic. Attribution and soft locks key on that id,
and a one-off id fragments both.

**The orchestrator recalls before it dispatches and writes after it lands.** Recall tells it whether
another session already moved the ground a task stands on. After the landing it calls
`lambo_derive` for the decision and `lambo_record_action` for what landed, with the hash.

**Implementers and reviewers write what the plan does not already say.** Derive the decision and its
reason, a departure a reviewer accepted, or a trap the next agent would fall into. Do not derive the
activity. Git records that.

**`lambo_derive` de-duplicates on resend. `lambo_record_action` does not.** Never resend a reworded
action. Both return before the write lands, so do not read your own write back immediately.

**Memory being down never blocks work.** Note the outage in your handoff file and carry on. This
file still holds everything you need.

## Read order

0. `lambo_recall` on your task, before you open anything. Skip this step if you have no lambo.
1. This file, in full.
2. [`product.md`](product.md), then [`dev-diary/project.md`](dev-diary/project.md).
3. Your task's block in its `dev-diary/PHASE-*.md`, and `dev-diary/libraries.md`.
4. [`dev-diary/README.md`](dev-diary/README.md) for the current status and the phase graph.
5. Any prior review rounds for your task.
6. The commit you were handed, or the worktree you were given, and its tests.

Then assert four things. I am in my own worktree, not the main checkout. I own every path I will
edit. The shapes I need already exist. I can validate this task on its own. If any part reads false,
stop and tell the orchestrator.
