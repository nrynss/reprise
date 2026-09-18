// Package editorial turns two stems and a word timeline into proposals.
//
// One model call hears both stems and reads the timeline. Its answer
// points at word offsets, and every offset is checked against the loaded
// timeline before anything is stored. A span pointing at a word that does
// not exist is dropped and logged. Cuts default to accepted with a
// decisions row, so the untouched episode still plays well. The callback
// persists as a mention row with a callbacks row, so the next opening
// cites stored data, never invention. A failed call still leaves a
// renderable draft with no cuts and a plain title.
package editorial
