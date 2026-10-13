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

	// known holds every hash seen this run: recorded, claimed, or confirmed in SQLite.
	known sync.Map

	queue      chan record
	writerDone chan struct{}

	// mu guards closed so Record can never send on a closed channel.
	mu     sync.RWMutex
	closed bool

	// err holds the first background write failure.
	errMu sync.Mutex
	err   error
}

type record struct {
	hash     string
	filename string
}

// Open opens (creating if needed) the database in the library folder dir. dir itself
// is created if it does not exist.
func Open(dir string) (db *DB, err error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("sqlite", sqliteDSN(filepath.Join(dir, DBFileName)))
	if err != nil {
		return nil, err
	}
	db = &DB{sql: sqlDB}
	defer func() {
		if err != nil {
			db.closeSQL()
			db = nil
		}
	}()

	// The schema is shared with earlier versions of PhotoSorter; keep it compatible.
	if _, err = sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS processed_hashes (
			hash TEXT PRIMARY KEY,
			filename TEXT,
			processed_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return nil, err
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
	escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	return "file:" + escaped +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
}

// Count returns the number of hashes committed to disk.
func (db *DB) Count() (int, error) {
	var n int
	err := db.sql.QueryRow("SELECT COUNT(*) FROM processed_hashes").Scan(&n)
	return n, err
}

// Has reports whether hash is recorded (or claimed) in the library.
func (db *DB) Has(hash string) (bool, error) {
	if _, ok := db.known.Load(hash); ok {
		return true, nil
	}
	found, err := db.inSQL(hash)
	if found {
		db.known.Store(hash, true)
	}
	return found, err
}

// Claim atomically reserves hash for the caller. It returns true only for the first
// caller to claim a hash that is not already recorded, so concurrent workers holding
// identical content cannot both import it. A successful claim must be followed by
// [DB.Record] (on success) or [DB.Release] (on failure).
func (db *DB) Claim(hash string) (bool, error) {
	if _, loaded := db.known.LoadOrStore(hash, true); loaded {
		return false, nil
	}
	found, err := db.inSQL(hash)
	if err != nil {
		db.known.Delete(hash)
		return false, err
	}
	// If it was already persisted, leave it cached so later lookups short-circuit.
	return !found, nil
}

// Release drops a claim that was not followed by Record.
func (db *DB) Release(hash string) {
	db.known.Delete(hash)
}

// Record queues hash (with the file name it was stored under) for writing. It fails
// if the database is closed or an earlier background write failed.
func (db *DB) Record(hash, filename string) error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.closed {
		return errClosed
	}
	if err := db.Err(); err != nil {
		return err
	}
	db.known.Store(hash, true)
	db.queue <- record{hash: hash, filename: filename}
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
