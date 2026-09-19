# Season catalog

This directory ships empty. The operator adds one or two finished
episodes here after first deploy, as dogfood for new visitors. Each new
guest with no season receives a copy on the next season list. The server
imports this directory on boot and on every season list that finds no
receipt, so editing files here reaches visitors without a restart.

## File shape

One JSON file per episode. The file name without its extension is the
stable key. Keep it lowercase with dashes, such as `dread.json`. The
server ignores every other file, including this readme.

```json
{
  "title": "The dreaded conversation",
  "number": 1,
  "audio": "dread.opus",
  "words": [
    {"text": "Maya", "start_ms": 0, "end_ms": 120}
  ],
  "mentions": [
    {"kind": "person_name", "word_offset": 0, "quote": "Maya"}
  ],
  "planted_quote": "Maya"
}
```

Title names the episode. Number orders it in the season. Keep numbers
stable across edits, because callbacks cite them. Audio names an
optional sibling file with the finished mix, in opus, ogg, mp3, m4a, or
wav form. The server stores those bytes once and shares them across all
copies. Words carry the transcript behind the mentions. Mentions carry
the thread hits the host opens on. The planted quote must match one
mention quote exactly, and it becomes the opening for the next episode.

## Retiring an episode

Delete its JSON file and its audio sibling. The next import retires the
catalog rows and stops new copies. Existing visitor copies stay until
their owners drop them or the guest sweep takes them.
