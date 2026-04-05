package main

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "github.com/glebarez/go-sqlite"
)

type HashDB struct {
	db         *sql.DB
	existsStmt *sql.Stmt
	insertStmt *sql.Stmt
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

	return &HashDB{
		db:         db,
		existsStmt: existsStmt,
		insertStmt: insertStmt,
	}, count, nil
}

func (h *HashDB) Exists(hash string) (bool, error) {
	var dummy string
	err := h.existsStmt.QueryRow(hash).Scan(&dummy)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (h *HashDB) Insert(hash, filename string) error {
	_, err := h.insertStmt.Exec(hash, filename)
	return err
}

func (h *HashDB) Close() error {
	if h.existsStmt != nil {
		h.existsStmt.Close()
	}
	if h.insertStmt != nil {
		h.insertStmt.Close()
	}
	return h.db.Close()
}
