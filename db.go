package main

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "github.com/glebarez/go-sqlite"
)

var errDBClosed = errors.New("database is closed")

type HashDB struct {
	db          *sql.DB
	existsStmt  *sql.Stmt
	insertStmt  *sql.Stmt
	insertCh    chan insertRecord
	closeDoneCh chan struct{}
	memCache    sync.Map

	// mu guards closed so Insert can never send on a closed channel.
	mu     sync.RWMutex
	closed bool

	// err holds the first background write failure; once set, Insert refuses new work.
	errMu sync.Mutex
	err   error
}

type insertRecord struct {
	hash     string
	filename string
}

// sqliteDSN builds a URI whose pragmas apply to every pooled connection (PRAGMA
// statements run via Exec only affect the one connection that happened to run them).
func sqliteDSN(path string) string {
	escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	return "file:" + escaped +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
}

func InitDB(outputDir string) (h *HashDB, count int, err error) {
	if err = os.MkdirAll(outputDir, 0755); err != nil {
		return nil, 0, err
	}
	dbPath := filepath.Join(outputDir, ".photosorter_cache.db")
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, 0, err
	}
	var existsStmt, insertStmt *sql.Stmt
	defer func() {
		if err != nil {
			if existsStmt != nil {
				existsStmt.Close()
			}
			if insertStmt != nil {
				insertStmt.Close()
			}
			db.Close()
		}
	}()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS processed_hashes (
			hash TEXT PRIMARY KEY,
			filename TEXT,
			processed_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return nil, 0, err
	}

	existsStmt, err = db.Prepare("SELECT hash FROM processed_hashes WHERE hash = ?")
	if err != nil {
		return nil, 0, err
	}

	insertStmt, err = db.Prepare("INSERT OR IGNORE INTO processed_hashes (hash, filename) VALUES (?, ?)")
	if err != nil {
		return nil, 0, err
	}

	err = db.QueryRow("SELECT COUNT(*) FROM processed_hashes").Scan(&count)
	if err != nil {
		return nil, 0, err
	}

	h = &HashDB{
		db:          db,
		existsStmt:  existsStmt,
		insertStmt:  insertStmt,
		insertCh:    make(chan insertRecord, 1000), // Buffer adds breathing room
		closeDoneCh: make(chan struct{}),
	}

	go h.writer()

	return h, count, nil
}

func (h *HashDB) Exists(hash string) (bool, error) {
	if _, ok := h.memCache.Load(hash); ok {
		return true, nil
	}

	var dummy string
	err := h.existsStmt.QueryRow(hash).Scan(&dummy)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	h.memCache.Store(hash, true)
	return true, nil
}

// Claim atomically reserves a hash for the caller. It returns true only for the
// first caller to claim a hash that is not already recorded, so concurrent workers
// holding identical content cannot both import it. A successful claim must be
// followed by Insert (on success) or Release (on failure).
func (h *HashDB) Claim(hash string) (bool, error) {
	if _, loaded := h.memCache.LoadOrStore(hash, true); loaded {
		return false, nil
	}

	var dummy string
	err := h.existsStmt.QueryRow(hash).Scan(&dummy)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		h.memCache.Delete(hash)
		return false, err
	}
	// Already persisted; leave it cached so later lookups short-circuit.
	return false, nil
}

// Release drops a claim that was not followed by an Insert.
func (h *HashDB) Release(hash string) {
	h.memCache.Delete(hash)
}

// Insert queues a record for the background writer. It returns an error if the
// database is closed or an earlier background write has already failed.
func (h *HashDB) Insert(hash, filename string) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return errDBClosed
	}
	if err := h.Err(); err != nil {
		return err
	}
	h.memCache.Store(hash, true)
	h.insertCh <- insertRecord{hash: hash, filename: filename}
	return nil
}

// Err returns the first background write error, if any.
func (h *HashDB) Err() error {
	h.errMu.Lock()
	defer h.errMu.Unlock()
	return h.err
}

func (h *HashDB) setErr(err error) {
	h.errMu.Lock()
	defer h.errMu.Unlock()
	if h.err == nil {
		h.err = err
	}
}

func (h *HashDB) writer() {
	defer close(h.closeDoneCh)
	for record := range h.insertCh {
		batch := []insertRecord{record}
		// Opportunistically grab up to 500 queued inserts
	drain:
		for len(batch) < 500 {
			select {
			case next, ok := <-h.insertCh:
				if !ok {
					break drain
				}
				batch = append(batch, next)
			default:
				break drain
			}
		}
		h.flush(batch)
	}
}

// flush writes one batch in a single transaction. Failures are recorded rather than
// dropped; the writer keeps draining so producers never block on a dead writer.
func (h *HashDB) flush(batch []insertRecord) {
	if h.Err() != nil {
		return
	}
	tx, err := h.db.Begin()
	if err != nil {
		h.setErr(err)
		return
	}
	stmt := tx.Stmt(h.insertStmt)
	defer stmt.Close()
	for _, r := range batch {
		if _, err := stmt.Exec(r.hash, r.filename); err != nil {
			tx.Rollback()
			h.setErr(err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		h.setErr(err)
	}
}

// Close flushes all queued inserts and closes the database. It returns the first
// write error seen, so callers learn if any record failed to persist. It is safe to
// call more than once.
func (h *HashDB) Close() error {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		close(h.insertCh)
	}
	h.mu.Unlock()
	<-h.closeDoneCh

	h.existsStmt.Close()
	h.insertStmt.Close()
	closeErr := h.db.Close()
	if err := h.Err(); err != nil {
		return err
	}
	return closeErr
}
