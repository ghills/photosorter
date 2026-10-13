package library

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "github.com/glebarez/go-sqlite" // registers the pure-Go "sqlite" driver
)

// DBFileName is the cache file PhotoSorter keeps at the root of the library.
const DBFileName = ".photosorter_cache.db"

const (
	// writeQueueSize is how many records may wait for the background writer before
	// Record blocks.
	writeQueueSize = 1000

	// maxBatch is the most records written in one transaction.
	maxBatch = 500
)

var errClosed = errors.New("library database is closed")

// DB records the content hash of every file in the library, so content that is
// already present is never imported again.
//
// It also remembers how many media files the library held after the last sync or
// import ([DB.LibraryFiles]), so files added by other means can be noticed.
//
// Lookups are served from an in-memory set backed by SQLite. Writes are queued and
// committed in batches by a background goroutine; [DB.Close] flushes the queue. If a
// background write fails, the error is kept, [DB.Err] reports it, and further
// [DB.Record] calls fail, so callers can stop instead of importing files that would
// not be remembered.
//
// All methods are safe for concurrent use.
type DB struct {
	sql        *sql.DB
	existsStmt *sql.Stmt
	insertStmt *sql.Stmt

	// known holds every hash seen this run. The value is true once the hash is recorded,
	// kept or confirmed in SQLite, or a *claim while a worker is still importing it.
	known sync.Map

	// readOnly keeps records in memory only (see [OpenReadOnly]).
	readOnly bool

	queue      chan record
	writerDone chan struct{}

	// mu guards closed so Record can never send on a closed channel.
	mu     sync.RWMutex
	closed bool

	// err holds the first background write failure.
	errMu sync.Mutex
	err   error
}

// claim is an in-flight import of a hash. done is closed once the claim is recorded,
// kept or released.
type claim struct{ done chan struct{} }

type record struct {
	hash     string
	filename string
}

// Open opens (creating if needed) the database in the library folder dir. dir itself
// is created if it does not exist.
func Open(dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return open(sqliteDSN(filepath.Join(dir, DBFileName)), false, true)
}

// OpenReadOnly opens the database in the library folder dir without writing anything
// to disk, for dry runs. Records are kept in memory for the rest of the run only. A
// missing database (or library folder) behaves as an empty one.
func OpenReadOnly(dir string) (*DB, error) {
	path := filepath.Join(dir, DBFileName)
	if _, err := os.Stat(path); err != nil {
		return open("file::memory:", true, true)
	}
	// immutable=1 reads the file without locks, journals or a shared-memory file.
	return open("file:"+escapePath(path)+"?immutable=1", true, false)
}

func open(dsn string, readOnly, createSchema bool) (db *DB, err error) {
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if readOnly {
		// Every pooled connection to :memory: would get its own empty database.
		sqlDB.SetMaxOpenConns(1)
	}
	db = &DB{sql: sqlDB, readOnly: readOnly}
	defer func() {
		if err != nil {
			db.closeSQL()
			db = nil
		}
	}()

	// The schema is shared with earlier versions of PhotoSorter; keep it compatible.
	// library_state is newer, so it may be missing from a database opened read-only.
	if createSchema {
		if _, err = sqlDB.Exec(`
			CREATE TABLE IF NOT EXISTS processed_hashes (
				hash TEXT PRIMARY KEY,
				filename TEXT,
				processed_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			CREATE TABLE IF NOT EXISTS library_state (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				files INTEGER NOT NULL
			)`); err != nil {
			return nil, err
		}
	}
	if db.existsStmt, err = sqlDB.Prepare("SELECT 1 FROM processed_hashes WHERE hash = ?"); err != nil {
		return nil, err
	}
	if db.insertStmt, err = sqlDB.Prepare("INSERT OR IGNORE INTO processed_hashes (hash, filename) VALUES (?, ?)"); err != nil {
		return nil, err
	}

	db.queue = make(chan record, writeQueueSize)
	db.writerDone = make(chan struct{})
	go db.writer()
	return db, nil
}

// sqliteDSN builds a URI whose pragmas apply to every pooled connection (PRAGMA
// statements run via Exec only affect the one connection that happened to run them).
func sqliteDSN(path string) string {
	return "file:" + escapePath(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
}

// escapePath escapes the characters that are special in an SQLite URI.
func escapePath(path string) string {
	return strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
}

// Count returns the number of hashes committed to disk.
func (db *DB) Count() (int, error) {
	var n int
	err := db.sql.QueryRow("SELECT COUNT(*) FROM processed_hashes").Scan(&n)
	return n, err
}

// LibraryFiles returns how many media files the library held after the last sync or
// import. ok is false if that is unknown, as with databases from earlier versions.
func (db *DB) LibraryFiles() (n int, ok bool, err error) {
	err = db.sql.QueryRow("SELECT files FROM library_state WHERE id = 1").Scan(&n)
	if err != nil {
		// No row yet, or (read-only) no table yet: either way, unknown.
		return 0, false, nil
	}
	return n, true, nil
}

// SetLibraryFiles stores n as [DB.LibraryFiles]. It does nothing in a read-only database.
func (db *DB) SetLibraryFiles(n int) error {
	if db.readOnly {
		return nil
	}
	_, err := db.sql.Exec("INSERT OR REPLACE INTO library_state (id, files) VALUES (1, ?)", n)
	return err
}

// Claim atomically reserves hash for the caller. It returns true only for the first
// caller to claim a hash that is not already recorded, so concurrent workers holding
// identical content cannot both import it. If another caller holds the claim, Claim
// waits for its outcome: false if it was recorded or kept, otherwise the claim is
// tried again. A successful claim must be followed by [DB.Record] or [DB.Keep] (on
// success) or [DB.Release] (on failure).
func (db *DB) Claim(hash string) (bool, error) {
	for {
		c := &claim{done: make(chan struct{})}
		v, loaded := db.known.LoadOrStore(hash, c)
		if !loaded {
			found, err := db.inSQL(hash)
			switch {
			case err != nil:
				db.known.Delete(hash)
			case found:
				// Already persisted: cache it so later lookups short-circuit.
				db.known.Store(hash, true)
			}
			if err != nil || found {
				close(c.done)
				return false, err
			}
			return true, nil
		}
		other, pending := v.(*claim)
		if !pending {
			return false, nil
		}
		<-other.done
	}
}

// Keep marks a claimed hash as present for the rest of this run without recording it.
func (db *DB) Keep(hash string) {
	db.settle(hash, true)
}

// Release drops a claim that was not followed by Record or Keep, so a caller waiting
// in Claim can import the same content instead.
func (db *DB) Release(hash string) {
	db.settle(hash, false)
}

// settle ends any in-flight claim on hash, leaving the hash known or forgotten.
func (db *DB) settle(hash string, known bool) {
	var prev any
	if known {
		prev, _ = db.known.Swap(hash, true)
	} else {
		prev, _ = db.known.LoadAndDelete(hash)
	}
	if c, ok := prev.(*claim); ok {
		close(c.done)
	}
}

// Record queues hash (with the file name it was stored under) for writing, ending any
// claim on it. It fails if the database is closed or an earlier background write
// failed. In a read-only database the record is kept in memory only.
func (db *DB) Record(hash, filename string) error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return errClosed
	}
	if err := db.Err(); err != nil {
		return err
	}
	db.settle(hash, true)
	if !db.readOnly {
		db.queue <- record{hash: hash, filename: filename}
	}
	return nil
}

// Err returns the first background write error, if any.
func (db *DB) Err() error {
	db.errMu.Lock()
	defer db.errMu.Unlock()
	return db.err
}

func (db *DB) setErr(err error) {
	db.errMu.Lock()
	defer db.errMu.Unlock()
	if db.err == nil {
		db.err = err
	}
}

// Close flushes queued records and closes the database. It returns the first write
// error seen, so callers learn if any record failed to persist. Calling it more than
// once is safe.
func (db *DB) Close() error {
	db.mu.Lock()
	if !db.closed {
		db.closed = true
		close(db.queue)
	}
	db.mu.Unlock()
	<-db.writerDone

	closeErr := db.closeSQL()
	if err := db.Err(); err != nil {
		return err
	}
	return closeErr
}

func (db *DB) closeSQL() error {
	if db.existsStmt != nil {
		db.existsStmt.Close()
	}
	if db.insertStmt != nil {
		db.insertStmt.Close()
	}
	return db.sql.Close()
}

func (db *DB) inSQL(hash string) (bool, error) {
	var one int
	err := db.existsStmt.QueryRow(hash).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// writer drains the queue, committing whatever has accumulated (up to maxBatch) in
// one transaction.
func (db *DB) writer() {
	defer close(db.writerDone)
	for first := range db.queue {
		batch := []record{first}
	fill:
		for len(batch) < maxBatch {
			select {
			case r, ok := <-db.queue:
				if !ok {
					break fill
				}
				batch = append(batch, r)
			default:
				break fill
			}
		}
		db.flush(batch)
	}
}

// flush writes one batch in a single transaction. Failures are recorded rather than
// dropped; the writer keeps draining so producers never block on a dead writer.
func (db *DB) flush(batch []record) {
	if db.Err() != nil {
		return
	}
	tx, err := db.sql.Begin()
	if err != nil {
		db.setErr(err)
		return
	}
	stmt := tx.Stmt(db.insertStmt)
	defer stmt.Close()
	for _, r := range batch {
		if _, err := stmt.Exec(r.hash, r.filename); err != nil {
			tx.Rollback()
			db.setErr(err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		db.setErr(err)
	}
}
