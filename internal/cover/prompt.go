// Prompt shapes the image brief for one episode. The brief carries the
// title and the show notes for subject matter, then constrains the
// render. Faces and lettering are both refused, because the app sets
// the title itself and the art must stay abstract.

package cover

import "strings"

// Prompt builds the image brief from the episode title and show notes.
// The brief names the subject, then refuses faces and all lettering.
func Prompt(title, notes string) string {
	var out strings.Builder
	out.WriteString("Square podcast cover art. Abstract flat shapes in a limited palette. ")
	out.WriteString("No faces. No people. No animals. ")
	out.WriteString("No text. No letters. No words. No numbers. No logos. ")
	out.WriteString("The app sets the title itself, so render no lettering of any kind. ")
	if title != "" {
		out.WriteString("Episode: " + title + ". ")
	}
	if notes != "" {
		out.WriteString("Mood and subject: " + notes)
	}
	return strings.TrimSpace(out.String())
}
