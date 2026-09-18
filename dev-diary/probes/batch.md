# Batch probe

Live run on 2026-09-18 against the AssemblyAI batch API with the flagship
model. The Go probe is `internal/assemblyai/batch_live_test.go` behind the
`live` build tag. It rebuilds its own 48 kHz stem on each run with Piper, a
local neural voice, so no committed audio and no cloud speech service are
involved. No real voice appears anywhere.

## How to run

Set the config path, then run the single test. The run takes about two
minutes. Most of that is local speech synthesis. The transcript itself
completes in seconds.

```bash
REPRISE_CONFIG=config/reprise.local.toml \
  go test -tags live -run TestBatchProbe -v ./internal/assemblyai/
```

## Clip

Four sentences, one voice, each sentence synthesized alone and joined with
700 ms of silence. Piper `en_US-lessac-medium` at its native rate, joined
and resampled to 48 kHz mono PCM. Word bounds come from forced alignment of
each sentence against its own audio inside the test. The final run used a
19 second clip with 54 known words.

Script:

1. The harbour lantern burned late while Mara studied the tide charts on
   the wall.
2. Quilby the cartographer mapped the marshes past midnight for the
   Meridian Survey.
3. Rain drummed the skylight as the attic clock counted eleven bells over
   Lisbon.
4. She folded the ferry schedules into a paper crane and set it sailing at
   dawn.

The script plants five entities: Mara and Quilby as people, cartographer as
an occupation, midnight as a time, Lisbon as a location.

## Request

One creation call carries every feature at once:

```json
{
  "audio_url": "<upload_url>",
  "speech_models": ["universal-3-5-pro"],
  "entity_detection": true,
  "auto_highlights": true,
  "speech_understanding": {
    "request": {
      "summarization": {"summary_type": "bullets"}
    }
  }
}
```

The old `speech_model` singular field is deprecated. It returns 400 and
points at `speech_models`. The old `summarization`, `summary_model` and
`summary_type` top level fields are gone for this model. They return 400
with `speech_model 'universal-3-pro' is not compatible`. Summarization now
lives under `speech_understanding.request`. Entity detection stays a top
level boolean. Key phrases are `auto_highlights`, also top level.

Upload went to `POST /v2/upload` as `application/octet-stream`, 1804702
bytes for the 19 second clip. Creation went to `POST /v2/transcript`.
Polling went to `GET /v2/transcript/{id}` every 5 seconds.

## Transcript response

Full text from the final run:

> The harbor lantern burned late while Mara studied the tide charts on the
> wall. Quilby the cartographer mapped the marshes past midnight for the
> meridian survey. Rain drummed the skylight as the attic clock counted 11
> bells over Lisbon. She folded the ferry schedules into a paper crane and
> set it sailing at dawn.

Confidence 0.99 or so on most words. Audio duration 19 seconds. The model
heard `harbour` as `harbor` and `eleven` as `11`. Both are spelling drifts,
not timing faults. Everything else matched word for word.

Each word carries `text`, `start`, `end`, `confidence` and `speaker`. Times
are integer milliseconds. `speaker` is null with no diarization asked.

```json
{"text": "The", "start": 32, "end": 112, "confidence": 0.92, "speaker": null}
{"text": "Mara", "start": 1703, "end": 1895, "confidence": 0.997, "speaker": null}
{"text": "Lisbon.", "start": 12725, "end": 13094, "confidence": 0.992, "speaker": null}
```

## Timestamp error per word

Known bounds come from alignment inside the test, so each run logs them
beside the returned words. The final run matched 52 of 54 known words. Two
are spelling drifts with no error to report: `harbour` heard as `harbor`
and `eleven` heard as `11`.

Start error runs from minus 267 ms to plus 518 ms. The mean sits near minus
80 ms. End error runs from minus 78 ms to plus 342 ms. The mean sits near
plus 150 ms. Most words land within 150 ms on both edges. Outliers cluster
on short function words after a gap: `studied` starts 518 ms early because
the aligner absorbed the pause before it, and `schedules` starts 327 ms
early for the same reason. Full per-word rows are in the test log. Rerun
the probe to reproduce them.

## Entities

Five entities came back, one per planted name:

```json
{"entity_type": "person_name", "text": "Mara", "start": 1703, "end": 1895}
{"entity_type": "person_name", "text": "Quilby", "start": 4514, "end": 4820}
{"entity_type": "occupation", "text": "cartographer", "start": 4996, "end": 5559}
{"entity_type": "time", "text": "midnight", "start": 6844, "end": 7310}
{"entity_type": "location", "text": "Lisbon", "start": 12725, "end": 13094}
```

Each entity carries `entity_type`, `text`, `start` and `end` in
milliseconds. No confidence field. Times match the word stamps exactly.

## Key phrases

`auto_highlights_result` carries `status` plus `results`. Each result has
`count`, `rank`, `text` and `timestamps`. The final run returned 15
phrases. Top ranks went to the names: Mara 0.102, midnight 0.096, Quilby
0.077, Lisbon 0.076. Multiword phrases carry one stamp span: `paper crane`
at 16324 to 16629, `tide charts` at 2426 to 3149.

```json
{"count": 1, "rank": 0.102, "text": "Mara",
 "timestamps": [{"start": 1703, "end": 1895}]}
```

## Summarization

The summary lives under `speech_understanding.response.summarization`.
Request echoes under `speech_understanding.request`. Shape from the final
run:

```json
{"summarization": {
  "summary": [{
    "start": 32, "end": 17898,
    "bullets": [
      "Mara studied tide charts late into the night as Quilby mapped marshes.",
      "Rain fell while the clock struck eleven in Lisbon.",
      "She folded schedules into a paper crane to set sail at dawn."],
    "headline": "Late Night Preparations For A Ferry Trip"}],
  "block_summary": "Mara studied tide charts ...",
  "summary_type": "bullets", "effort": "low", "status": "success"}}
```

Effort is provider chosen. The plan assumed summarization, entity detection
and key phrases remain beside the deprecated chapters route. That holds.

## Chapters through LLM Gateway

Chapters ride the LLM Gateway, not the transcript endpoint. The deprecated
`auto_chapters` parameter is gone from the transcript API. The docs route
is: transcribe, fetch paragraphs, group them, then call the gateway once
per group.

The probe calls `GET /v2/transcript/{id}/paragraphs` first. The 19 second
clip returned one paragraph from 32 to 17898 ms with per-word confidence
inside. Then it calls `POST
https://llm-gateway.assemblyai.com/v1/chat/completions` with the transcript
text and this body:

```json
{"model": "qwen3.5-4b-32k-fast",
 "messages": [{"role": "user", "content": "Split this transcript ..."}],
 "max_tokens": 800}
```

The gateway answered with four chapters, one per sentence, each with a
title and a start time. Usage for the call: 90 prompt tokens, 222
completion tokens, 312 total. Full content and shapes are in the test log.

Model choice needs care. The account reaches exactly one of the 37 listed
gateway models: `qwen3.5-4b-32k-fast`. Every other model, including the
docs example `claude-sonnet-4-6` and the cheap Gemini flashes, returns 400
with `Your account does not have access to this LLM Gateway model`. The
models list endpoint stays public: `GET
https://llm-gateway.assemblyai.com/v1/models` returns all 37 with per
million token prices. The working model costs 0.10 per million prompt
tokens and 0.50 per million completion tokens. The chapter call cost about
0.00012 dollars.

Chapter route decision: transcribe with Universal-3.5 Pro, fetch
paragraphs, then call the gateway with `qwen3.5-4b-32k-fast`. If access
widens later, prefer a cheaper-per-quality pick and remeasure.

## DELETE on the transcript

`DELETE /v2/transcript/{id}` returns 200. A fetch afterwards still returns
200, but the content is gone: text reads `Deleted by user.`, audio URL
reads `http://deleted_by_user`, words are null, confidence is null. The
probe asserts exactly that. Erasure must treat the provider copy as soft
deleted and say so on the privacy page. The plan already records that the
provider deletion is soft.

## Result route

Polling carries results. Webhooks do not reach this machine.

The probe created a second transcript with `webhook_url` pointed at a
loopback listener. The transcript completed, `webhook_status_code` stayed
null, and nothing arrived locally. A repeat against the public IP with no
listener also left `webhook_status_code` null. No signed callback, no
retry stamp, no failure code to branch on. From a workstation behind NAT
with no inbound port, a webhook subscription is a hope, not a channel.

Result route decision: poll `GET /v2/transcript/{id}` on a timer. No
provider webhook receiver is needed. If a public ingress ever exists,
remeasure delivery before building on it.

## Spend

Batch bills 0.21 dollars per connected hour. Counts from the transcript
list endpoint after the runs:

- 11 creation calls, 10 polled to completion, 1 still processing at count
  time and deleted after. Audio seconds: 19 plus 19 plus 44 times 4 plus 4
  plus 1 plus 1 plus 2 plus 1. Total about 223 seconds.
- Batch cost about 0.013 dollars.
- 2 gateway chapter calls at about 0.00012 dollars each.
- Total probe spend under 0.02 dollars. The task budget holds with wide
  margin.
- All scratch transcripts were deleted after reading. The account list
  holds only soft deleted rows.

## Surprises

- The flagship model rejects the legacy summarization fields with a 400
  that names the wrong model string (`universal-3-pro`). The fix is the
  `speech_understanding.request` wrapper, not a different model name.
- `entity_detection` and `auto_highlights` stay top level booleans while
  summarization moved under `speech_understanding`. Mixed generations of
  parameters share one request.
- Only 1 of 37 gateway models answers on this key. The docs example model
  does not. The probe pins `qwen3.5-4b-32k-fast` until access changes.
- DELETE returns full 200 shapes with scrubbed fields instead of 404. A
  fetch that succeeds with `Deleted by user.` is the deletion proof, not a
  failing fetch.
- A package with only live tagged test files breaks the non-live gate
  with `build constraints exclude all Go files`. The probe adds
  `internal/assemblyai/doc.go` with the package clause so plain `go test`
  passes. Whoever owns the provider adapter keeps that file.
