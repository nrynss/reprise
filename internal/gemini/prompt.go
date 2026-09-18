package gemini

// editorialSystem tells the model how to judge. Delivery decides the cold
// open, never the transcript alone. A laugh, a pause, or a voice going
// thin outranks a clever sentence.
func editorialSystem() string {
	return "You are the editor of a personal podcast. You hear the " +
		"recording and read its word timeline. Judge delivery from the " +
		"audio, never from the transcript alone. A laugh, a pause, or a " +
		"voice going thin outranks a clever sentence. Propose cuts the " +
		"speaker would thank you for. Quote the speaker exactly. Answer " +
		"with JSON only, matching the response schema."
}

// editorialPrompt carries the timeline and the judging rules for one
// episode. Word offsets in brackets are the only way to point at audio.
// The cold open runs 10 to 20 seconds and is chosen for how it sounds.
func editorialPrompt(timeline string) string {
	return "Hear both stems and read the word timeline. The first stem is " +
		"the speaker. The second stem is the host. Bracketed numbers are " +
		"word offsets. Point at words with offsets only.\n\n" +
		"Pick one cold open of 10 to 20 seconds. Choose it for delivery: " +
		"a laugh, a pause, a voice going thin. Do not pick from the " +
		"transcript alone. Name the span with start_word and end_word.\n\n" +
		"Cut false starts, dead tangents, and lost threads. Each cut " +
		"carries a one line reason. Leave a span alone when in doubt.\n\n" +
		"Title the episode short and specific to what was said. Write " +
		"show notes in a few sentences in the speaker's own words. " +
		"Name one unresolved thing to open next time, with its exact " +
		"quote and the span the quote came from.\n\n" +
		"Timeline:\n" + timeline
}
