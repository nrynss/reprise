# Reprise

> **Reprise** — a theme that returns later.

A personal podcast. You talk, a host asks, and it becomes an episode. The host
remembers what you said last time, so by episode four it opens with the thing
you were dreading in episode one.

- **Event:** AssemblyAI Voice Agent Hackathon, run by lablab.ai
- **Deadline:** **30 September 2026, 8:30 PM IST**

---

## 1. The product

Every AI journaling tool records what you chose to say. Reprise asks the
follow-up, and then asks again next week.

The output is not a transcript or a summary. It is **an episode**: a listenable
artifact with a cold open, chapters and show notes, that you can hand to
somebody or put on YouTube. That distinction is the whole product. A summary is
a file. An episode is a thing people play.

The beat that sells it is memory across episodes. Episode one you mention you
are dreading a conversation with your sister. Episode six the host opens with
whether you ever had it. Nobody needs the feature explained after hearing that.

---

## 2. The flow

**1 — Record.** One WebSocket to the Voice Agent API. The host asks, you talk,
it interrupts and follows up. Interruption handling matters more than usual: a
host that waits politely for you to finish sounds like a form.

**2 — Edit, optionally.** Transcript-driven. Strike a sentence and the audio cut
follows it exactly, because word timestamps make the cut sample-accurate. The
model proposes cuts, you dispose of them.

**3 — Mark done.** An explicit gate. It renders the final audio from the cut
list, and it is the only thing that triggers stage 4, so the expensive pass runs
once per episode rather than once per edit.

**4 — Analyse.** Batch-transcribe **the rendered file**, then run Speech
Understanding over that: chapters, summarization for show notes, entity
detection, key phrases.

**5 — Gallery.** Episodes, transcript, analysis, downloads, share.

**Transcription is not stage 4.** Editing needs a transcript, so the editor cuts
against the realtime transcript from the session. Stage 4 is the batch pass on
the finished audio.

**The batch pass runs on rendered audio, never raw.** Transcribe the raw take
and then cut it, and every chapter timestamp and word offset is wrong by
whatever you removed. Transcript, chapters and timestamps must describe the
episode people actually hear.

---

## 3. The memory loop

Entity detection and key phrases across episodes build an index of who and what
keeps coming up. It feeds two things.

**The host's opening question.** This is where callbacks come from, and it is
the thesis.

**Keyterm prompting**, which takes about a hundred words: the names that recur
in your life. People, places, the project you keep mentioning.

So **the show gets better at hearing you the longer you use it.** Episode one
mangles your friend's name. Episode four gets it right, because episode one
taught it. That is a demonstrable platform mechanic and it is specific to a
personal podcast rather than generic voice AI.

---

## 4. Models

**Live: AssemblyAI owns the conversation end to end.** Transcription, interview,
turn-taking, interruption and voice all run inside the Voice Agent API. English
only, realtime, one speaker.

**Post: an external multimodal model runs the editing flow.** Gemini Flash or
DeepSeek class — cheap, fast, and nothing is waiting on it. Its jobs are the
judgement calls:

- **Which fifteen seconds open the episode.** The hardest and highest-value call
  in the pipeline, and the one that decides whether anyone keeps listening.
- What to cut: false starts, the tangent that went nowhere, the thirty seconds
  where you lost the thread.
- Structure, title, show notes, description.
- The callback to plant for next time.
- Cover art.

**Multimodality earns its keep here specifically.** A transcript tells you the
words; only the audio tells you that you laughed, or paused, or your voice went
thin. Picking a cold open from text alone picks on content when the real
criterion is delivery. Speech Understanding supplies the structured signals;
the multimodal model hears the takes.

---

## 5. Audio

**The app records both sides locally during the session, as separate stems.**
Your voice at 48 kHz, the host at its native rate. Stems are what editing needs,
and recording locally means never depending on how the API returns its copy.

- **The host is capped at 24 kHz** whatever you do, because that is what the
  Voice Agent pipeline sends. This is fine. Listeners judge the human voice, and
  nobody has complained about a synthetic one being 24k.
- **Never capture over the phone path.** Twilio SIP is 8 kHz μ-law, which is
  exactly the phone-call sound this product exists to avoid. Browser or app only.
- Encode to Opus or AAC at rest, keep WAV only for the live session.
- Fades, levels and mixing are ffmpeg defaults, not UI.

---

## 6. Accounts and guest mode

Supabase for auth, Postgres and storage.

**Guests use anonymous sign-in, not a separate no-auth path.** They get a real
user row, row-level security keeps working, and there is one code path.

**Guest mode lands on a seeded season, never an empty state.** This product is
invisible without history: a first-time visitor sees a microphone button and
nothing else, and the callback, which is the entire thesis, cannot happen.
Episodes one to four are already there, and the visitor records episode five. If
the host opens by referencing something the visitor never recorded, they
understand the product in ninety seconds without watching the video.

**A public URL that opens $4.50/hr sessions is a way to drain $150.** Hard cap
on session length, cap on sessions per guest, and a global switch.

---

## 7. Rules

- **The artifact is the product.** The zero-edit episode must be genuinely listenable, or the strongest demo beat is lost.
- **The model proposes, you dispose.** Cuts arrive as strikethrough transcript ranges. Never silently delete part of somebody's diary; an unreviewable edit on personal material is a trust breach, not a feature.
- **Default all proposed cuts to applied, and make every one revertible.** That keeps zero-edit good and gives the editor an obvious reason to exist.
- **Every episode is private by default.** Publishing is an explicit per-episode action. Accidental publication is the one failure mode that would actually hurt somebody.
- **The analysis panel is not a dashboard.** No sentiment gauges, no topic pie charts.
- **Terminate every session explicitly.** Streaming bills on connection duration, not audio sent.

---

## 8. The gallery and export

The analysis worth showing is **the thread across episodes**: the people who
keep coming up and where, the things you said you would do and whether you ever
mentioned doing them, the topic you have circled four times without resolving.
That is the memory made visible, it is the same thesis as the host's callbacks,
and it is the only analysis anyone opens twice.

Export packages exactly what YouTube's upload form asks for:

- The audio, plus a waveform-over-cover-art video
- Transcript as **SRT or VTT**, so captions upload rather than being auto-generated
- Description with **auto chapters written as timestamped lines**, which YouTube turns into chapter markers automatically
- Cover art at 1:1

The chapter trick is the detail worth keeping: a feature of AssemblyAI's
produces a visibly professional result on a platform the judges recognise, with
no transformation in between.

---

## 9. Budget and platform constraints

- **Voice Agent API: $4.50/hr**, all-in — STT, turn and interruption detection, LLM, TTS, hosting, plus recordings and transcripts. Billed per second of connected time.
- **$150 in credits ≈ 33 hours.** Seven twenty-minute episodes is a little over two hours, so the ceiling is testing, not content.
- An unterminated session auto-closes at three hours and bills all three. **$13.50 per leak.**
- Batch Universal-3.5 Pro is $0.21/hr, so stage 4 is rounding error.
- Realtime is English-only here by choice; the model supports 18 languages if that ever matters.

---

## 10. Positioning

**The business slide is a slide.** The proven market next door is life-story
capture — StoryWorth, Remento, HereAfter — where people pay real subscription
money to record an elderly parent's stories before they cannot. Same mechanism
exactly: an interviewer with memory that produces something listenable. Name it,
do not build it.

**Distinct from Orma.** Both are a daily conversation that asks you things and
remembers. The separation holds as long as Reprise's output is an artifact and
its register is affection, while Orma's output is accountability and its channel
is a call with teeth. If Reprise starts nagging about tasks, it is Orma twice.

**The claim to make** is not that it records you. It is that it asks the
question you did not think to answer, and then remembers the answer.

---

## 11. Submission

- **Public GitHub repository**, MIT-compatible, original work
- **A live, reachable application URL** — a submission field, not optional polish
- **Video presentation** and a **separate slide deck**
- Cover image, title, short and long description, technology and category tags
- **Judging, four criteria equally weighted:** Application of Technology · Presentation · Business Value · Originality
- **Five equal prizes** of $1,000 cash plus $1,000 in credits, no ranking. The target is clearing a bar, not winning a comparison.

**The demo is one thing: play the episode.** The beat is the host referencing
something from a previous episode. Everything else in the video is context for
that moment.
