// The operator catalog on disk and its import into rows.
//
// One JSON file per stable key lives in the catalog directory. The file
// name without its extension is the key, so renaming a file retires one
// entry and adds another. Sync reads every entry, validates the whole set
// first, then applies it file by file. A validation failure applies
// nothing. An apply failure keeps earlier files and reports the key, so a
// retry finishes what is left. Removing a file retires its catalog rows
// and its audio. Visitor copies stay, because they are separate rows.

package seed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"embed"
	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/mediastore"
	"github.com/nrynss/keel/sqlite"
)

// schemaNamespace is the migration ledger namespace this package owns. It
// shares the database file with the diary schema without colliding,
// because each namespace keeps its own ledger.
const schemaNamespace = "reprise_seed"

//go:embed migrations/*.sql
var migrations embed.FS

// migrate applies the catalog tables to db.
func migrate(ctx context.Context, db *sqlite.DB) error {
	schema, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("seed: open migrations: %w", err)
	}
	if err := sqlite.Migrate(ctx, db, schemaNamespace, schema); err != nil {
		return fmt.Errorf("seed: migrate: %w", err)
	}
	return nil
}

// wordEntry is one transcript word in a catalog file. Offsets point at
// the rendered words, so copied mentions resolve against them.
type wordEntry struct {
	// Text is the spoken word.
	Text string `json:"text"`
	// StartMs is the word start on the episode clock.
	StartMs int `json:"start_ms"`
	// EndMs is the word end on the episode clock.
	EndMs int `json:"end_ms"`
}

// mentionEntry is one stored thread hit in a catalog file. Kinds match
// the memory index: people, commitments, and key phrases.
type mentionEntry struct {
	// Kind names the thread kind behind the hit.
	Kind string `json:"kind"`
	// Offset is the rendered word index the hit points at.
	Offset int `json:"word_offset"`
	// Quote is the stored wording, as heard.
	Quote string `json:"quote"`
}

// catalogFile is one stable catalog entry on disk. Number orders the
// episode in every season that copies it. Audio names an optional sibling
// audio file with the finished mix. Planted names an optional mention
// quote that the next episode opens on.
type catalogFile struct {
	// Title names the episode.
	Title string `json:"title"`
	// Number orders the episode in its season.
	Number int `json:"number"`
	// Audio names the sibling audio file, or empty for rows only.
	Audio string `json:"audio"`
	// Words holds the transcript words behind the mentions.
	Words []wordEntry `json:"words"`
	// Mentions holds the thread hits the next episode opens on.
	Mentions []mentionEntry `json:"mentions"`
	// Planted names the mention quote the next episode opens on. It
	// must match one mention quote exactly, or stay empty.
	Planted string `json:"planted_quote"`
}

// loadedFile is one validated catalog file with its key and hash. The
// hash covers the JSON and the audio bytes together, so editing either
// reimports the entry.
type loadedFile struct {
	key   string
	entry catalogFile
	hash  string
	audio []byte
}

// audioTypes maps an audio sibling extension to its stored content type.
// The operator keeps finished mixes in one of these forms.
var audioTypes = map[string]string{
	".opus": "audio/ogg",
	".ogg":  "audio/ogg",
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".wav":  "audio/wav",
}

// seedLoudness is the placeholder loudness on a catalog render row.
// Playback reads only the blob id, and the analysis pass remeasures real
// renders, so the catalog never needs a measured value.
const seedLoudness = -23.0

// Sync imports the catalog directory into rows. It creates the reserved
// user on demand. New and changed files replace their catalog episode.
// Files removed since the last run retire their catalog episode. An empty
// directory clears the catalog and stops new copies. Visitor copies stay
// in every case, because they are separate rows under other owners.
// Sync returns the catalog episode count.
func (s *Service) Sync(ctx context.Context) (int, error) {
	loaded, err := s.loadDir()
	if err != nil {
		return 0, err
	}
	for _, file := range loaded {
		if err := s.apply(ctx, file); err != nil {
			return 0, err
		}
	}
	if err := s.prune(ctx, loaded); err != nil {
		return 0, err
	}
	return len(loaded), nil
}

// loadDir reads and validates every catalog file. It returns entries
// sorted by key, so applies run in a stable order.
func (s *Service) loadDir() ([]loadedFile, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("seed: read catalog: %w", err)
	}
	var out []loadedFile
	seen := map[int]string{}
	for _, item := range entries {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".json") {
			continue
		}
		key := strings.TrimSuffix(item.Name(), ".json")
		if key == "" {
			return nil, wrapInvalid("catalog file " + item.Name() + " has no key")
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, item.Name()))
		if err != nil {
			return nil, fmt.Errorf("seed: read catalog file %s: %w", key, err)
		}
		var entry catalogFile
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&entry); err != nil {
			return nil, fmt.Errorf("seed: parse catalog file %s: %w: fix the JSON", key, err)
		}
		if err := checkEntry(key, entry); err != nil {
			return nil, err
		}
		if other, found := seen[entry.Number]; found {
			return nil, wrapInvalid("catalog files " + other + " and " + key +
				" share episode number")
		}
		seen[entry.Number] = key
		var audio []byte
		if entry.Audio != "" {
			audio, err = os.ReadFile(filepath.Join(s.dir, entry.Audio))
			if err != nil {
				return nil, fmt.Errorf("seed: read catalog audio %s: %w", entry.Audio, err)
			}
			if _, found := audioTypes[strings.ToLower(filepath.Ext(entry.Audio))]; !found {
				return nil, wrapInvalid("catalog audio " + entry.Audio +
					" needs an opus, ogg, mp3, m4a, or wav sibling")
			}
		}
		sum := sha256.Sum256(raw)
		if audio != nil {
			sum = sha256.Sum256(append(sum[:], audio...))
		}
		out = append(out, loadedFile{
			key:   key,
			entry: entry,
			hash:  hex.EncodeToString(sum[:]),
			audio: audio,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out, nil
}

// checkEntry validates one catalog file. It returns ErrInvalid naming
// the key and the defect.
func checkEntry(key string, entry catalogFile) error {
	if entry.Title == "" {
		return wrapInvalid("catalog file " + key + " needs a title")
	}
	if entry.Number < 1 {
		return wrapInvalid("catalog file " + key + " needs a positive number")
	}
	for i, word := range entry.Words {
		if word.Text == "" || word.StartMs < 0 || word.EndMs < word.StartMs {
			return wrapInvalid(fmt.Sprintf("catalog file %s word %d needs text and ordered offsets", key, i))
		}
	}
	for i, mention := range entry.Mentions {
		if mention.Kind == "" || mention.Quote == "" || mention.Offset < 0 {
			return wrapInvalid(fmt.Sprintf("catalog file %s mention %d needs a kind, a quote, and an offset", key, i))
		}
	}
	if entry.Planted != "" {
		matched := false
		for _, mention := range entry.Mentions {
			if mention.Quote == entry.Planted {
				matched = true
				break
			}
		}
		if !matched {
			return wrapInvalid("catalog file " + key +
				" plants a quote with no matching mention")
		}
	}
	return nil
}

// apply upserts one catalog episode. An unchanged hash skips the write,
// so a steady directory costs one scan. A changed file swaps the episode
// rows in one transaction and replaces the audio around it.
func (s *Service) apply(ctx context.Context, file loadedFile) error {
	var known string
	var blobID string
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT source_hash, audio_blob_id FROM seed_catalog WHERE key = ?", file.key).Scan(&known, &blobID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("seed: read catalog entry %s: %w", file.key, err)
	}
	if err == nil && known == file.hash {
		return nil
	}
	var fresh string
	if file.audio != nil {
		ext := strings.ToLower(filepath.Ext(file.entry.Audio))
		fresh, err = s.media.Persist(ctx, bytes.NewReader(file.audio), mediastore.Put{
			ContentType: audioTypes[ext],
			Owner:       SeedUserID,
			Group:       file.key,
			Visibility:  mediastore.Public,
		})
		if err != nil {
			return fmt.Errorf("seed: persist catalog audio %s: %w", file.key, err)
		}
	}
	if err := s.swapEpisode(ctx, file, fresh); err != nil {
		if fresh != "" {
			_ = s.media.Delete(ctx, fresh)
		}
		return err
	}
	if blobID != "" && blobID != fresh {
		_ = s.media.Delete(ctx, blobID)
	}
	return nil
}

// swapEpisode replaces the catalog rows for one key in one transaction.
// Either the whole entry lands or nothing does. Fresh names the newly
// persisted audio blob, or empty when the file carries no audio.
func (s *Service) swapEpisode(ctx context.Context, file loadedFile, fresh string) error {
	episodeID, err := id.New()
	if err != nil {
		return fmt.Errorf("seed: mint catalog episode: %w", err)
	}
	shareToken, err := id.New()
	if err != nil {
		return fmt.Errorf("seed: mint catalog share token: %w", err)
	}
	tx, err := s.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("seed: import catalog file %s: %w", file.key, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	stamp := s.now().Unix()
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, kind, created_at, last_seen_at)
		VALUES (?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`,
		SeedUserID, KindSeed, stamp, stamp); err != nil {
		return fmt.Errorf("seed: ensure catalog owner: %w", err)
	}
	var oldEpisode sql.NullString
	err = tx.QueryRowContext(ctx,
		"SELECT episode_id FROM seed_catalog WHERE key = ?", file.key).Scan(&oldEpisode)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("seed: read catalog entry %s: %w", file.key, err)
	}
	if oldEpisode.Valid {
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM episodes WHERE id = ?", oldEpisode.String); err != nil {
			return fmt.Errorf("seed: retire catalog episode %s: %w", file.key, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, ?, ?, ?, 'ready', 'private', ?, 0)`,
		episodeID, SeedUserID, file.entry.Number, file.entry.Title, shareToken); err != nil {
		return fmt.Errorf("seed: insert catalog episode %s: %w", file.key, err)
	}
	mentionIDs := make([]string, len(file.entry.Mentions))
	for i, word := range file.entry.Words {
		wordID, err := id.New()
		if err != nil {
			return fmt.Errorf("seed: mint catalog word: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO words
			(id, owner_id, episode_id, text, start_ms, end_ms, source)
			VALUES (?, ?, ?, ?, ?, ?, 'rendered')`,
			wordID, SeedUserID, episodeID, word.Text, word.StartMs, word.EndMs); err != nil {
			return fmt.Errorf("seed: insert catalog word %d in %s: %w", i, file.key, err)
		}
	}
	for i, mention := range file.entry.Mentions {
		mentionID, err := id.New()
		if err != nil {
			return fmt.Errorf("seed: mint catalog mention: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mentions
			(id, owner_id, episode_id, kind, word_offset, quote)
			VALUES (?, ?, ?, ?, ?, ?)`,
			mentionID, SeedUserID, episodeID, mention.Kind, mention.Offset, mention.Quote); err != nil {
			return fmt.Errorf("seed: insert catalog mention %d in %s: %w", i, file.key, err)
		}
		mentionIDs[i] = mentionID
	}
	if file.entry.Planted != "" {
		for i, mention := range file.entry.Mentions {
			if mention.Quote != file.entry.Planted {
				continue
			}
			callbackID, err := id.New()
			if err != nil {
				return fmt.Errorf("seed: mint catalog callback: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO callbacks
				(id, owner_id, episode_id, mention_id, used)
				VALUES (?, ?, ?, ?, 0)`, callbackID, SeedUserID, episodeID, mentionIDs[i]); err != nil {
				return fmt.Errorf("seed: plant catalog callback in %s: %w", file.key, err)
			}
		}
	}
	if fresh != "" {
		renderID, err := id.New()
		if err != nil {
			return fmt.Errorf("seed: mint catalog render: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO renders
			(id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
			VALUES (?, ?, ?, ?, ?, '', ?)`,
			renderID, SeedUserID, episodeID, file.hash, fresh, seedLoudness); err != nil {
			return fmt.Errorf("seed: insert catalog render in %s: %w", file.key, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO rendered_sources (episode_id, render_id) VALUES (?, ?)`,
			episodeID, renderID); err != nil {
			return fmt.Errorf("seed: link catalog render in %s: %w", file.key, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO seed_catalog (key, episode_id, source_hash, audio_blob_id)
		VALUES (?, ?, ?, ?) ON CONFLICT (key) DO UPDATE
		SET episode_id = excluded.episode_id, source_hash = excluded.source_hash,
			audio_blob_id = excluded.audio_blob_id`,
		file.key, episodeID, file.hash, fresh); err != nil {
		return fmt.Errorf("seed: record catalog entry %s: %w", file.key, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("seed: import catalog file %s: %w", file.key, err)
	}
	committed = true
	return nil
}

// prune retires catalog entries with no file. Visitor copies stay,
// because they are separate rows under other owners. Retired audio goes
// with its entry, because no copy references it.
func (s *Service) prune(ctx context.Context, loaded []loadedFile) error {
	keep := make(map[string]bool, len(loaded))
	for _, file := range loaded {
		keep[file.key] = true
	}
	rows, err := s.db.Reader().QueryContext(ctx, "SELECT key, episode_id, audio_blob_id FROM seed_catalog")
	if err != nil {
		return fmt.Errorf("seed: list catalog entries: %w", err)
	}
	type retired struct {
		episode string
		blob    string
	}
	var gone []retired
	var keys []string
	for rows.Next() {
		var key, episode, blob string
		if err := rows.Scan(&key, &episode, &blob); err != nil {
			_ = rows.Close()
			return fmt.Errorf("seed: list catalog entries: %w", err)
		}
		if !keep[key] {
			gone = append(gone, retired{episode: episode, blob: blob})
			keys = append(keys, key)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("seed: list catalog entries: %w", err)
	}
	for i, item := range gone {
		tx, err := s.db.Writer().BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("seed: retire catalog entry: %w", err)
		}
		committed := false
		rollback := func() {
			if !committed {
				_ = tx.Rollback()
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM episodes WHERE id = ?", item.episode); err != nil {
			rollback()
			return fmt.Errorf("seed: retire catalog entry: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM seed_catalog WHERE key = ?", keys[i]); err != nil {
			rollback()
			return fmt.Errorf("seed: retire catalog entry: %w", err)
		}
		if err := tx.Commit(); err != nil {
			rollback()
			return fmt.Errorf("seed: retire catalog entry: %w", err)
		}
		committed = true
		if item.blob != "" {
			_ = s.media.Delete(ctx, item.blob)
		}
	}
	return nil
}
