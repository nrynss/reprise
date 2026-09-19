# Submission drafts index

Status: draft. The owner records and submits. This folder holds every item the submission form lists, in draft form.

## Files

- `video-script.md` holds the video script with one marked slot for the callback beat.
- `slide-deck.md` holds the slide deck outline against the four judging criteria.
- `cover-notes.md` holds the cover image direction.
- `descriptions.md` holds the title, the short description, and the long description.
- `tags.md` holds the technology and category tags.

## Measured facts reused here

- The live app answers at `https://reprise.nryn.dev` with a Cloudflare certificate.
- Scripted guests minted sessions with HTTP 201, streamed generated speech, heard host replies, and closed explicitly with `session.end`.
- Provider close reason read `client_end`. No session stayed open.
- Connected durations read 16.6, 16.8, and 66.8 seconds. The last wait came from script overhead, not provider behavior.
- Voice cost runs 4.50 dollars per connected hour. The three live calls cost about 2.1, 2.1, and 8.4 cents.
- The gallery lists each recorded episode privately under its owner.
- Anonymous media reads answer 404 with `Cache-Control: private, no-store`. Unknown ids answer identically.
- Owner stem reads round trip byte exact.
- The edge rate gate answers 429 with a retry hint under load. Patient callers recover.

## Slots that wait on T6.4b

[WAITS ON T6.4b] The callback beat audio. No real voice session has run, so no episode carries a true callback yet.
[WAITS ON T6.4b] The listening verdict. Nobody has heard one untouched episode end to end.
[WAITS ON T6.4b] The browser and device matrix. Chrome and Firefox, headphones and speakers, remain unrun.
[WAITS ON T6.4b] The final cover title and the exact quoted host line.

## Task close statement

This task cannot close until T6.4b lands. The drafts above stand ready. The marked slots need real voices first.
