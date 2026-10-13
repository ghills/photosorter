package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"

	_ "github.com/glebarez/go-sqlite"
)

type HashDB struct {
	db          *sql.DB
	existsStmt  *sql.Stmt
	insertStmt  *sql.Stmt
	insertCh    chan insertRecord
	closeDoneCh chan struct{}
	memCache    sync.Map
}

type insertRecord struct {
	hash     string
	filename string
}

func InitDB(outputDir string) (*HashDB, int, error) {
	err := os.MkdirAll(outputDir, 0755)
	if err != nil {
		return nil, 0, err
	}
	dbPath := filepath.Join(outputDir, ".photosorter_cache.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, 0, err
	}

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

	// Performance optimizations
	_, err = db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
	`)
	if err != nil {
		return nil, 0, err
	}

	existsStmt, err := db.Prepare("SELECT hash FROM processed_hashes WHERE hash = ?")
	if err != nil {
		return nil, 0, err
	}

	insertStmt, err := db.Prepare("INSERT OR IGNORE INTO processed_hashes (hash, filename) VALUES (?, ?)")
	if err != nil {
		return nil, 0, err
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM processed_hashes").Scan(&count)
	if err != nil {
		return nil, 0, err
	}

	h := &HashDB{
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

func (h *HashDB) Insert(hash, filename string) error {
	h.memCache.Store(hash, true)
	h.insertCh <- insertRecord{hash: hash, filename: filename}
	return nil
}

func (h *HashDB) writer() {
	for record := range h.insertCh {
		tx, err := h.db.Begin()
		if err != nil {
			// Fallback to non-transactional insert if tx fails
			h.insertStmt.Exec(record.hash, record.filename)
			continue
		}

		stmt := tx.Stmt(h.insertStmt)
		stmt.Exec(record.hash, record.filename)

		batchSize := 1
		done := false
		// Opportunistically grab up to 500 queued inserts
		for !done && batchSize < 500 {
			select {
			case nextRecord, ok := <-h.insertCh:
				if !ok {
					done = true
				} else {
					stmt.Exec(nextRecord.hash, nextRecord.filename)
					batchSize++
				}
			default:
				done = true
			}
		}

		tx.Commit()
	}
	close(h.closeDoneCh)
}

func (h *HashDB) Close() error {
	if h.insertCh != nil {
		close(h.insertCh)
		<-h.closeDoneCh
	}
	
	if h.existsStmt != nil {
		h.existsStmt.Close()
	}
	if h.insertStmt != nil {
		h.insertStmt.Close()
	}
	return h.db.Close()
}
