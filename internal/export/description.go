package export

import (
	"fmt"
	"strings"
)

// Stamp renders milliseconds as the upload form reads them. Minutes lead
// without padding, so zero reads 0:00 and ninety seconds reads 1:30. An
// hour adds its own field, so 3661 seconds reads 1:01:01.
func Stamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	secs := ms / 1000
	hours := secs / 3600
	minutes := (secs % 3600) / 60
	rest := secs % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, rest)
	}
	return fmt.Sprintf("%d:%02d", minutes, rest)
}

// ChapterLine renders one chapter as the upload form reads it, a stamp
// followed by one space and the title.
func ChapterLine(chapter Chapter) string {
	return Stamp(chapter.StartMs) + " " + chapter.Title
}

// ValidateChapters checks chapters against the upload form rules. The
// first chapter opens at zero, at least three chapters exist, titles are
// non-blank, starts rise strictly, and every chapter lasts the minimum.
// The closing chapter measures against durationMs, so the caller passes
// the render length the chapters describe.
func ValidateChapters(chapters []Chapter, durationMs int64) error {
	if durationMs <= 0 {
		return fmt.Errorf("export: validate %d chapters: %w: missing duration", len(chapters), ErrInvalid)
	}
	if len(chapters) < MinChapters {
		return fmt.Errorf("export: validate %d chapters: %w: want at least %d", len(chapters), ErrChapters, MinChapters)
	}
	if chapters[0].StartMs != 0 {
		return fmt.Errorf("export: validate chapters: %w: first starts at %d", ErrChapters, chapters[0].StartMs)
	}
	for i, c := range chapters {
		if strings.TrimSpace(c.Title) == "" {
			return fmt.Errorf("export: validate chapter %d: %w: empty title", i, ErrChapters)
		}
		if c.StartMs < 0 || c.StartMs >= durationMs {
			return fmt.Errorf("export: validate chapter %d: %w: start %d past duration %d", i, ErrChapters, c.StartMs, durationMs)
		}
		if i > 0 && c.StartMs <= chapters[i-1].StartMs {
			return fmt.Errorf("export: validate chapter %d: %w: start not past %d", i, ErrChapters, chapters[i-1].StartMs)
		}
		end := durationMs
		if i+1 < len(chapters) {
			end = chapters[i+1].StartMs
		}
		if end-c.StartMs < MinChapterMs {
			return fmt.Errorf("export: validate chapter %d %q: %w: span %d under %d", i, c.Title, ErrChapters, end-c.StartMs, MinChapterMs)
		}
	}
	return nil
}

// Description builds the upload description from the show notes followed
// by the chapter lines. The notes come first so a reader meets the
// episode before its markers, and the chapters follow as timestamped
// lines the form turns into markers on its own.
func Description(showNotes string, chapters []Chapter, durationMs int64) (string, error) {
	if err := ValidateChapters(chapters, durationMs); err != nil {
		return "", err
	}
	var body strings.Builder
	notes := strings.TrimSpace(showNotes)
	if notes != "" {
		body.WriteString(notes)
		body.WriteString("\n\n")
	}
	for i, c := range chapters {
		body.WriteString(ChapterLine(Chapter{Title: strings.TrimSpace(c.Title), StartMs: c.StartMs}))
		if i+1 < len(chapters) {
			body.WriteString("\n")
		}
	}
	return body.String(), nil
}
