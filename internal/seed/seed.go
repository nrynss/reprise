// Package seed carries the shared catalog and its per-user copies.
//
// The shipped catalog starts empty. The operator adds one or two finished
// episodes as files under the catalog directory after first deploy. Sync
// reads those files into catalog rows owned by the reserved seed user.
// EnsureCopy hands each new visitor their own copy on the first season
// list. Drop removes one visitor copy without touching the catalog or
// any other visitor.
//
// Catalog audio persists once as public blobs under the seed user. The
// bytes are identical demo material for every visitor, so sharing them
// keeps one copy on disk. Copies hold rows only: the episode, its words,
// its mentions, and its planted callback. The callback selector still
// finds a stored row to open on. Copy rows stay private to their owner.
// Only the shared audio bytes are public, and their ids are unguessable.
//
// Copies never delete blobs. Dropping a copy deletes its rows only, and
// the catalog blobs stay. The episode erasure must therefore branch on
// the seeded flag before it runs. It sends recorded episodes through the
// erasure fan-out and seeded episodes through Drop. The guest sweep needs
// the same branch. Erasing a seeded copy through the fan-out would delete
// the shared catalog audio for every visitor. That wiring lives outside
// this package, and the handoff names it as a contract change.
package seed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nrynss/keel/mediastore"
	"github.com/nrynss/keel/sqlite"
)

// SeedUserID owns every catalog row. The user is reserved at Sync time
// and never signs in. Its kind is KindSeed, so the guest sweep skips it.
const SeedUserID = "seed"

// KindSeed marks the reserved catalog owner. Guests carry "guest" and
// logins carry "owner". Any other kind counts as a visitor account and
// receives a copy.
const KindSeed = "seed"

// KindOwner marks a login. Owners never receive a copy.
const KindOwner = "owner"

// Sentinel errors. Every failure path this package produces wraps one of
// these, so callers branch with errors.Is.
var (
	// ErrInvalid reports a Config this package cannot honour, or a
	// catalog file it cannot use. The wrapped error names the cause.
	ErrInvalid = errors.New("seed: invalid argument")
	// ErrNotFound reports an unknown user, an unknown episode, or an
	// episode owned by someone else. Handlers answer 404 for all three.
	ErrNotFound = errors.New("seed: not found")
	// ErrNotSeeded reports Drop for a recorded episode. Recorded rows
	// belong to the erasure fan-out, so the caller sends them there.
	ErrNotSeeded = errors.New("seed: episode is not a seeded copy")
)

// BlobStore persists and deletes catalog audio. The media store
// implements it. Copies never write blobs, so EnsureCopy and Drop take
// no store. Only Sync touches audio.
type BlobStore interface {
	// Persist stores src and returns its blob id.
	Persist(ctx context.Context, src io.Reader, p mediastore.Put) (string, error)
	// Delete removes the blob stored under id.
	Delete(ctx context.Context, id string) error
}

// Config configures a Service. DB is the shared diary handle. Media
// persists catalog audio. CatalogDir holds one JSON file per stable key
// with optional audio siblings. Now stamps receipts, and nil means
// time.Now.
type Config struct {
	DB         *sqlite.DB
	Media      BlobStore
	CatalogDir string
	Now        func() time.Time
}

// Service imports the catalog and copies it per visitor. Create it with
// New, because the zero value holds no database. A Service is safe for
// concurrent use.
type Service struct {
	db    *sqlite.DB
	media BlobStore
	dir   string
	now   func() time.Time
}

// New migrates the catalog tables and returns the Service. It creates no
// rows. Sync creates the reserved user and the catalog on demand.
func New(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.DB == nil {
		return nil, wrapInvalid("database must not be nil")
	}
	if cfg.Media == nil {
		return nil, wrapInvalid("media store must not be nil")
	}
	if cfg.CatalogDir == "" {
		return nil, wrapInvalid("catalog directory must not be empty")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	if err := migrate(ctx, cfg.DB); err != nil {
		return nil, err
	}
	return &Service{db: cfg.DB, media: cfg.Media, dir: cfg.CatalogDir, now: now}, nil
}

// wrapInvalid builds an ErrInvalid with a reason.
func wrapInvalid(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, reason)
}
