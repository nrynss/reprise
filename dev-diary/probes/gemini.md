# Gemini probe

Live run on 2026-09-18 against Vertex AI with the service account key file.
The Go probe is `internal/gemini/live_test.go` behind the `live` build tag.
It rebuilds all audio on each run with Piper, a local neural voice, so no
committed clip and no cloud speech service are involved. No real voice
appears anywhere. The probe made three Vertex calls: one text ping, one
audio description, one listening choice. No other Vertex spend occurred.

## How to run

Run the single test from the checkout root. The loader fallback misses the
checkout file from the package directory, so the test walks up to find it.
An explicit config path also works. The run takes about one minute. Most
of that is local speech synthesis and the long audio request.

```bash
go test -tags live -run TestGeminiProbe -v ./internal/gemini/
```

## Question one: credentials on the box

The route is decided: Vertex AI with a service account key file through a
`file` source. The project and location come from settings as inline
values. The probe proves the route end to end.

The plan names the credential `secrets.gemini_credential` with source
`file` and the key path, status resolved. It names `vertex_project` and
`vertex_location` as inline values, both resolved. The probe reveals the
key JSON (2360 bytes) and checks its type reads `service_account`. It
builds credentials with the cloud platform scope and opens a Vertex client
with the project and location from settings. No key bytes appear in any log.
The plan and the logs carry lengths and names only.

A text ping then proves the client reaches Vertex. The model is
`gemini-2.5-flash` from local settings. Prompt: reply with the single
word PONG. Answer verbatim: PONG. Finish reason: STOP. Usage: 8 prompt
tokens (TEXT 8), 2 candidate tokens (TEXT 2), 29 total, 0 cached, 19
thoughts. Model version reported: `gemini-2.5-flash`.

A wrong or unreadable file fails at boot by name. A missing path fails
at load with this shape (temp path in the probe, real path on the box):

`settings: secret has no value: config: cannot resolve secret:
secrets.gemini_credential (source file locator path=<key path>):
source: not found: file: path=<key path>`

A group readable file also fails. The probe writes a temp key at mode
0640 and the load refuses it with this shape:

`settings: secret has no value: config: cannot resolve secret:
secrets.gemini_credential (source file locator path=<key path>):
source: file is group or other readable: path <key path> (mode 0640):
file: path=<key path>`

Both errors name the key, the source, and the path. The real key file
has mode 600, verified with stat. Its bytes never entered a log, a file,
or a quoted payload.

Decision: keep the Vertex service account file route. The proof is the
ping above, made with the project and location from settings.

## Question two: audio size

Vertex has no Files API, so the probe sends the stem inline. Build: four
sentences synthesized with Piper `en_US-lessac-medium`, joined, then
looped 80 times into one WAV at 22050 Hz, encoded to mono Opus at 24k
with ffmpeg. No bucket exists and none was created.

Final run stem, from ffprobe:

`Input #0, ogg, from 'stem.opus': Duration: 00:19:25.65, start:
0.000000, bitrate: 23 kb/s. Stream #0:0: Audio: opus, 48000 Hz, mono,
fltp. encoder: Lavc63.1.101 libopus.`

Size: 3450676 bytes. Prompt: describe in one sentence what this audio
sounds like. Answer verbatim: A calm female voice narrates a seaside
scene, accompanied by the sounds of ocean waves, seagulls, and a distant
foghorn. Finish reason: STOP. Usage: 10 prompt text tokens plus 29150
audio tokens, 25 candidate text tokens, 29298 total, 0 cached, 113
thoughts. Model version reported: `gemini-2.5-flash`.

Inline holds. A twenty minute mono Opus stem is accepted with no Cloud
Storage step. No bucket became a deployment dependency. The cost of the
probe call is the usage row above: about 29 thousand prompt tokens for
twenty minutes of audio, near 1500 audio tokens per minute at 24k mono.

Response shape that matters: `GenerateContentResponse` carries
`Candidates` (each with `Content.Parts`, `FinishReason`, `AvgLogprobs`),
`ModelVersion`, `ResponseID`, `UsageMetadata`, and `PromptFeedback`. The
usage block carries `PromptTokenCount`, `CandidatesTokenCount`,
`TotalTokenCount`, `CachedContentTokenCount`, `ThoughtsTokenCount`, plus
`PromptTokensDetails` and `CandidatesTokensDetails` broken down by
modality (TEXT, AUDIO). Audio arrives as `Part.InlineData` with MIME
`audio/ogg`. Every probe call returned exactly one candidate.

## Question three: listening, not reading

Two takes of one line, judged without any transcript. The flat take
reads: And that is why the lighthouse keeper never trusts the fog. The
laughing take reads the same line plus: Ha ha, he learned that the wet
way. Both use the same local voice. Sizes: 8835 bytes flat, 15151 bytes
laughing, both mono Opus. Prompt: two short voice takes follow, do not
transcribe them, listen to delivery only, say which take should open the
episode with one reason grounded in how it sounds.

Answer verbatim: The second take should open the episode. The reason is
the added laugh and subsequent conversational tone at the end of the
second take. This makes the delivery more engaging and inviting, which
is perfect for an opener to draw listeners in.

Finish reason: STOP. Usage: 40 prompt text tokens plus 225 audio tokens,
47 candidate text tokens, 962 total, 0 cached, 650 thoughts. Model
version reported: `gemini-2.5-flash`.

The test judges delivery, not content. The model picked the laughing
take and grounded its reason in tone, not words.

## Spend

Three Vertex `GenerateContent` calls on `gemini-2.5-flash`, all counted:
ping (29 total tokens), audio description (29298 total), listening
choice (962 total). About 30289 tokens in all. No Cloud Storage use, no
bucket cost, no other Vertex call.
