// Package migrations_test pins the login schema guards through the real
// migration seam. It applies the embedded identity migrations on an empty
// database and on a diary copy, then proves the provider subject pairing
// stays unique and the recent send lookup uses its index.
package migrations_test

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/store"
)

// openDatabase creates a fresh database file for one test.
func openDatabase(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   filepath.Join(t.TempDir(), "login.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// openDiaryCopy migrates the diary schema first, so the login tables land
// beside the production tables with their parent rows present.
func openDiaryCopy(t *testing.T) *sqlite.DB {
	t.Helper()
	db := openDatabase(t)
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open diary store: %v", err)
	}
	return db
}

// applyLogin runs the identity migrations through the service entry point.
// Later code boots through the same call, so the test guards what ships.
func applyLogin(t *testing.T, db *sqlite.DB) {
	t.Helper()
	if _, err := identity.New(t.Context(), identity.Config{
		DB:         db,
		SigningKey: "test-signing-key-with-enough-length",
		Now:        func() time.Time { return time.Unix(1758000000, 0) },
	}); err != nil {
		t.Fatalf("apply identity migrations: %v", err)
	}
}

// tableExists reports whether sqlite_master holds a table of that name.
func tableExists(t *testing.T, db *sqlite.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n); err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	return n == 1
}

// indexExists reports whether sqlite_master holds an index of that name.
func indexExists(t *testing.T, db *sqlite.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", name).Scan(&n); err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	return n == 1
}

// exec runs one write and fails the test on error.
func exec(t *testing.T, db *sqlite.DB, stmt string, args ...any) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(), stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

// tryExec runs one write and returns its error for refusal checks.
func tryExec(t *testing.T, db *sqlite.DB, stmt string, args ...any) error {
	t.Helper()
	_, err := db.Writer().ExecContext(t.Context(), stmt, args...)
	return err
}

// seedUser writes one guest user row that identities can attach to.
func seedUser(t *testing.T, db *sqlite.DB, userID string) {
	t.Helper()
	exec(t, db, `INSERT INTO users (id, kind, created_at, last_seen_at)
		VALUES (?, 'guest', 1758000000, 1758000000)`, userID)
}

// insertIdentity writes one identity row with a fixed stamp.
func insertIdentity(t *testing.T, db *sqlite.DB, id, userID, provider, subject string) {
	t.Helper()
	exec(t, db, `INSERT INTO identities (id, user_id, provider, subject, created_at)
		VALUES (?, ?, ?, ?, 1758000000)`, id, userID, provider, subject)
}

// TestLoginMigrationsApplyOnEmptyDatabase checks the login tables migrate
// on a database with no diary tables, and a second boot changes nothing.
func TestLoginMigrationsApplyOnEmptyDatabase(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	applyLogin(t, db)
	for _, name := range []string{"identities", "login_codes"} {
		if !tableExists(t, db, name) {
			t.Fatalf("table %s is missing after migrate", name)
		}
	}
	if !indexExists(t, db, "login_codes_address_created_idx") {
		t.Fatal("send lookup index is missing after migrate")
	}
	applyLogin(t, db)
	for _, name := range []string{"identities", "login_codes"} {
		if !tableExists(t, db, name) {
			t.Fatalf("table %s went missing after a second boot", name)
		}
	}
}

// TestLoginMigrationsApplyOnDiaryCopy checks the login tables migrate
// beside the diary tables, with the send lookup index present.
func TestLoginMigrationsApplyOnDiaryCopy(t *testing.T) {
	t.Parallel()
	db := openDiaryCopy(t)
	applyLogin(t, db)
	for _, name := range []string{"identities", "login_codes"} {
		if !tableExists(t, db, name) {
			t.Fatalf("table %s is missing on the diary copy", name)
		}
	}
	if !indexExists(t, db, "login_codes_address_created_idx") {
		t.Fatal("send lookup index is missing on the diary copy")
	}
}

// TestLoginDisplayNameBackfillsDiaryRows checks the display column lands on
// a diary copy that already holds user rows, and every old row reads empty.
func TestLoginDisplayNameBackfillsDiaryRows(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open diary store: %v", err)
	}
	seedUser(t, db, "user-old")
	applyLogin(t, db)
	applyLogin(t, db)
	var col string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT name FROM pragma_table_info('users') WHERE name = 'display_name'").Scan(&col); err != nil {
		t.Fatalf("read users columns: %v", err)
	}
	if col != "display_name" {
		t.Fatalf("users column = %q, want display_name", col)
	}
	var name string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT display_name FROM users WHERE id = 'user-old'").Scan(&name); err != nil {
		t.Fatalf("read backfilled display name: %v", err)
	}
	if name != "" {
		t.Fatalf("backfilled display name = %q, want empty", name)
	}
}

// TestIdentityProviderSubjectStaysUnique checks a duplicate provider
// subject pair fails, the same subject under the other provider succeeds,
// and one user may hold both an email and a second provider identity.
func TestIdentityProviderSubjectStaysUnique(t *testing.T) {
	t.Parallel()
	db := openDiaryCopy(t)
	applyLogin(t, db)
	seedUser(t, db, "user-1")
	seedUser(t, db, "user-2")

	insertIdentity(t, db, "id-1", "user-1", "email", "a@example.com")
	dupEmail := tryExec(t, db, `INSERT INTO identities (id, user_id, provider, subject, created_at)
		VALUES ('id-2', 'user-2', 'email', 'a@example.com', 1758000000)`)
	if dupEmail == nil {
		t.Fatal("duplicate email pair landed, want refusal")
	}

	insertIdentity(t, db, "id-3", "user-2", "google", "a@example.com")
	insertIdentity(t, db, "id-4", "user-1", "google", "sub-9")

	dupGoogle := tryExec(t, db, `INSERT INTO identities (id, user_id, provider, subject, created_at)
		VALUES ('id-5', 'user-1', 'google', 'sub-9', 1758000000)`)
	if dupGoogle == nil {
		t.Fatal("duplicate second provider pair landed, want refusal")
	}
}

// TestLoginCodeSendLookupUsesIndex checks the recent send lookup for one
// address uses the address index instead of scanning the table.
func TestLoginCodeSendLookupUsesIndex(t *testing.T) {
	t.Parallel()
	db := openDiaryCopy(t)
	applyLogin(t, db)
	seedUser(t, db, "user-1")
	exec(t, db, `INSERT INTO guest_sessions (id, user_id, created_at, revoked)
		VALUES ('sess-1', 'user-1', 1758000000, 0)`)
	exec(t, db, `INSERT INTO login_codes
		(id, address_hash, address, code_hash, requesting_session, expires_at, attempts, used_at, created_at)
		VALUES ('code-1', 'hash-a', 'a@example.com', 'codehash', 'sess-1', 1758000600, 0, 0, 1758000000)`)

	rows, err := db.Reader().QueryContext(t.Context(), `EXPLAIN QUERY PLAN
		SELECT id FROM login_codes WHERE address_hash = ? AND created_at > ?`, "hash-a", 1757913600)
	if err != nil {
		t.Fatalf("explain send lookup: %v", err)
	}
	defer func() { _ = rows.Close() }()
	plan := ""
	for rows.Next() {
		var first, second, third int
		var detail string
		if err := rows.Scan(&first, &second, &third, &detail); err != nil {
			t.Fatalf("read query plan: %v", err)
		}
		plan += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("drain query plan: %v", err)
	}
	if !strings.Contains(plan, "login_codes_address_created_idx") {
		t.Fatalf("send lookup plan = %q, want the address index", plan)
	}
}
