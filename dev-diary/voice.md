# Voice: how Reprise speaks on screen

Every word a person reads in the app follows this page. The owner agreed the examples below on
2026-09-29. A reviewer checks new copy against it, and `web/src/lib/copy.test.ts` enforces the
banned words.

## Rules

1. **Talk to the person, not about the system.** Say what they can see or do. Never name the
   machinery: no job, stream, endpoint, detail, backend, fixture, scripted, ledger, reservation,
   session row, or provider.
2. **Say only what the moment needs.** One short sentence, or none. Most screens need no notice
   at all, because the page already shows the state.
3. **Status is a word or two.** "Transcribing…", "Rendering… 75%", "Ready", "Couldn't save".
4. **An error says what happened and what to do, in one line.** "Couldn't load your episodes. Try
   again." Never paste a raw error. The server log keeps it.
5. **Plain and warm, never mannered.** No slogans, no lists of three negatives ("No scores, no
   gauges"), no promises about privacy mechanics, and no explaining why a design is good.
6. **Contractions are fine.** "Couldn't", "we're", "didn't".
7. **Name things the way the person does.** Episode, take, draft, host, cover, link. Not stem,
   render, proposal, mint, or artifact.
8. **Demo mode shows a small "Demo" badge.** It never shows a sentence about being scripted.

## Examples

| Before | After |
|---|---|
| Episodes are private until published. A running job shows its progress right on the card, through the same job stream the processing screen reads. | Your episodes. Only you can see them until you publish. |
| People, promises, and circling topics. Each one links to the moment it was said. No scores, no gauges. Quotes and links, nothing else. | People and topics that keep coming up, and the moments you mentioned them. |
| Working · rendering · 75% · progress survives a reload | Rendering… 75% |
| The take stays here until the draft is ready. | We're getting your draft ready. |
| Check the microphone, then start the take. | Find a quiet spot. When you're ready, press Start. |
| Microphone: granted when you press start. Connection: opens with the session request. Memory: the host greets you once the session opens. | *(removed)* |
| That did not work. Try again. | That code didn't work. Check it and try again. |
| Public at /share/…. Full address …. Only the finished audio opens behind it. | *(the link row, with Copy link, and no sentence)* |
| The detail carries no audio stream address yet, so playback waits here. | No audio yet. |
| The erase did not go through. Retry. | Couldn't erase this episode. Try again. |
| Scripted season. No backend needed. | *(a small "Demo" badge)* |
