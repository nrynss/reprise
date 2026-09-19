// Per-visitor copies of the catalog.
//
// EnsureCopy runs on the first request that lists a season, never inside
// session mint. Minting must stay cheap, and a visitor who never opens
// the season needs no rows. Each new guest and each new empty account
// gets one copy when the catalog holds rows. Owners never get one. A
// returning visitor never gets a second one: the receipt from the first
// copy blocks every later run, and dropping copies never clears it.
// Signing up keeps the copies, because the user id does not change.

package seed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nrynss/keel/id"
)

// EnsureCopy hands userID its copy of the catalog and returns the copied
// episode count. It copies nothing when the caller is an owner or the
// catalog user itself, when a receipt already exists, when the user
// already holds a season, or when the catalog is empty. Those paths write
// no receipt, so a later catalog still reaches a visitor with no season.
// The copy and its receipt land in one transaction. Either both land or
// neither does.
func (s *Service) EnsureCopy(ctx context.Context, userID string) (int, error) {
	if userID == "" {
		return 0, wrapInvalid("user id must not be empty")
	}
	var kind string
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT kind FROM users WHERE id = ?", userID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("seed: copy season for %s: %w", userID, ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("seed: copy season for %s: %w", userID, err)
	}
	if kind == KindOwner || kind == KindSeed {
		return 0, nil
	}
	var receipt string
	err = s.db.Reader().QueryRowContext(ctx,
		"SELECT user_id FROM seed_receipts WHERE user_id = ?", userID).Scan(&receipt)
	if err == nil {
		return 0, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("seed: read copy receipt for %s: %w", userID, err)
	}
	var season int
	if err := s.db.Reader().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM episodes WHERE owner_id = ?", userID).Scan(&season); err != nil {
		return 0, fmt.Errorf("seed: read season for %s: %w", userID, err)
	}
	if season > 0 {
		return 0, nil
	}
	catalog, err := s.catalogEpisodes(ctx)
	if err != nil {
		return 0, err
	}
	if len(catalog) == 0 {
		return 0, nil
	}
	if err := s.copySeason(ctx, userID, catalog); err != nil {
		return 0, err
	}
	return len(catalog), nil
}

// catalogEpisode is one catalog row with its content rows. Copies keep
// every value except the ids, the owner, the share token, and the seeded
// flag.
type catalogEpisode struct {
	id       string
	number   int
	title    string
	words    []wordRow
	mentions []mentionRow
	hooks    []hookRow
}

// wordRow is one stored transcript word.
type wordRow struct {
	text  string
	start int
	end   int
}

// mentionRow is one stored thread hit.
type mentionRow struct {
	id     string
	kind   string
	offset int
	quote  string
}

// hookRow is one stored callback pointing at a mention.
type hookRow struct {
	mention string
	used    int
}

// catalogEpisodes reads every catalog episode with its words, mentions,
// and callbacks, oldest number first.
func (s *Service) catalogEpisodes(ctx context.Context) ([]catalogEpisode, error) {
	rows, err := s.db.Reader().QueryContext(ctx,
		"SELECT id, number, title FROM episodes WHERE owner_id = ? ORDER BY number ASC", SeedUserID)
	if err != nil {
		return nil, fmt.Errorf("seed: list catalog episodes: %w", err)
	}
	var out []catalogEpisode
	for rows.Next() {
		var item catalogEpisode
		if err := rows.Scan(&item.id, &item.number, &item.title); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("seed: list catalog episodes: %w", err)
		}
		out = append(out, item)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("seed: list catalog episodes: %w", err)
	}
	for i := range out {
		if err := s.loadWords(ctx, &out[i]); err != nil {
			return nil, err
		}
		if err := s.loadMentions(ctx, &out[i]); err != nil {
			return nil, err
		}
		if err := s.loadHooks(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// loadWords reads the transcript words of one catalog episode.
func (s *Service) loadWords(ctx context.Context, item *catalogEpisode) error {
	rows, err := s.db.Reader().QueryContext(ctx,
		"SELECT text, start_ms, end_ms FROM words WHERE episode_id = ? AND owner_id = ? ORDER BY rowid ASC",
		item.id, SeedUserID)
	if err != nil {
		return fmt.Errorf("seed: read catalog words: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var word wordRow
		if err := rows.Scan(&word.text, &word.start, &word.end); err != nil {
			return fmt.Errorf("seed: read catalog words: %w", err)
		}
		item.words = append(item.words, word)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("seed: read catalog words: %w", err)
	}
	return nil
}

// loadMentions reads the thread hits of one catalog episode.
func (s *Service) loadMentions(ctx context.Context, item *catalogEpisode) error {
	rows, err := s.db.Reader().QueryContext(ctx,
		"SELECT id, kind, word_offset, quote FROM mentions WHERE episode_id = ? AND owner_id = ? ORDER BY rowid ASC",
		item.id, SeedUserID)
	if err != nil {
		return fmt.Errorf("seed: read catalog mentions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var mention mentionRow
		if err := rows.Scan(&mention.id, &mention.kind, &mention.offset, &mention.quote); err != nil {
			return fmt.Errorf("seed: read catalog mentions: %w", err)
		}
		item.mentions = append(item.mentions, mention)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("seed: read catalog mentions: %w", err)
	}
	return nil
}

// loadHooks reads the stored callbacks of one catalog episode.
func (s *Service) loadHooks(ctx context.Context, item *catalogEpisode) error {
	rows, err := s.db.Reader().QueryContext(ctx,
		"SELECT mention_id, used FROM callbacks WHERE episode_id = ? AND owner_id = ? ORDER BY rowid ASC",
		item.id, SeedUserID)
	if err != nil {
		return fmt.Errorf("seed: read catalog callbacks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hook hookRow
		if err := rows.Scan(&hook.mention, &hook.used); err != nil {
			return fmt.Errorf("seed: read catalog callbacks: %w", err)
		}
		item.hooks = append(item.hooks, hook)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("seed: read catalog callbacks: %w", err)
	}
	return nil
}

// copySeason writes every catalog episode under userID with fresh ids
// and stores the receipt beside them. Copies keep the catalog numbers,
// so callbacks cite the right episode. Copies set the seeded flag, so
// one query finds them and deletes branch on it.
func (s *Service) copySeason(ctx context.Context, userID string, catalog []catalogEpisode) error {
	tx, err := s.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("seed: copy season for %s: %w", userID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for _, item := range catalog {
		if err := copyEpisode(ctx, tx, userID, item); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO seed_receipts (user_id, copied_at, episodes) VALUES (?, ?, ?)",
		userID, s.now().Unix(), len(catalog)); err != nil {
		return fmt.Errorf("seed: write copy receipt for %s: %w", userID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("seed: copy season for %s: %w", userID, err)
	}
	committed = true
	return nil
}

// copyEpisode writes one catalog episode under its new owner. Mention
// ids are remapped, and callbacks follow the remap, so each copy cites
// its own rows. The used flags stay as the catalog holds them: a spoken
// opening stays spoken, and a planted callback stays plantable.
func copyEpisode(ctx context.Context, tx *sql.Tx, userID string, item catalogEpisode) error {
	episodeID, err := id.New()
	if err != nil {
		return fmt.Errorf("seed: mint copy episode: %w", err)
	}
	shareToken, err := id.New()
	if err != nil {
		return fmt.Errorf("seed: mint copy share token: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, ?, ?, ?, 'ready', 'private', ?, 1)`,
		episodeID, userID, item.number, item.title, shareToken); err != nil {
		return fmt.Errorf("seed: copy episode number %d: %w", item.number, err)
	}
	for _, word := range item.words {
		wordID, err := id.New()
		if err != nil {
			return fmt.Errorf("seed: mint copy word: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO words
			(id, owner_id, episode_id, text, start_ms, end_ms, source)
			VALUES (?, ?, ?, ?, ?, ?, 'rendered')`,
			wordID, userID, episodeID, word.text, word.start, word.end); err != nil {
			return fmt.Errorf("seed: copy word in episode number %d: %w", item.number, err)
		}
	}
	remapped := make(map[string]string, len(item.mentions))
	for _, mention := range item.mentions {
		mentionID, err := id.New()
		if err != nil {
			return fmt.Errorf("seed: mint copy mention: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mentions
			(id, owner_id, episode_id, kind, word_offset, quote)
			VALUES (?, ?, ?, ?, ?, ?)`,
			mentionID, userID, episodeID, mention.kind, mention.offset, mention.quote); err != nil {
			return fmt.Errorf("seed: copy mention in episode number %d: %w", item.number, err)
		}
		remapped[mention.id] = mentionID
	}
	for _, hook := range item.hooks {
		target, found := remapped[hook.mention]
		if !found {
			return fmt.Errorf("seed: copy callback in episode number %d: %w: dangling mention",
				item.number, ErrInvalid)
		}
		callbackID, err := id.New()
		if err != nil {
			return fmt.Errorf("seed: mint copy callback: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO callbacks
			(id, owner_id, episode_id, mention_id, used)
			VALUES (?, ?, ?, ?, ?)`, callbackID, userID, episodeID, target, hook.used); err != nil {
			return fmt.Errorf("seed: copy callback in episode number %d: %w", item.number, err)
		}
	}
	return nil
}
