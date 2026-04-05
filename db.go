package main

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "github.com/glebarez/go-sqlite"
)

type HashDB struct {
	db *sql.DB
}

func InitDB(outputDir string) (*HashDB, error) {
	err := os.MkdirAll(outputDir, 0755)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(outputDir, ".photosorter_cache.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS processed_hashes (
			hash TEXT PRIMARY KEY,
			filename TEXT,
			processed_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return nil, err
	}

	return &HashDB{db: db}, nil
}

func (h *HashDB) Exists(hash string) (bool, error) {
	var dummy string
	err := h.db.QueryRow("SELECT hash FROM processed_hashes WHERE hash = ?", hash).Scan(&dummy)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (h *HashDB) Insert(hash, filename string) error {
	_, err := h.db.Exec("INSERT OR IGNORE INTO processed_hashes (hash, filename) VALUES (?, ?)", hash, filename)
	return err
}

func (h *HashDB) Close() error {
	return h.db.Close()
}
