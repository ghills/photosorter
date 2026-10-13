package main

import (
	"os"
	"path/filepath"
)

// pathContains reports whether child is the same directory as parent or lies inside it.
// It compares real filesystem identity (os.SameFile) rather than strings, so symlinks and
// case-insensitive filesystems are handled. child need not exist yet; its nearest existing
// ancestor is used.
func pathContains(parent, child string) bool {
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return false
	}
	p, err := filepath.Abs(child)
	if err != nil {
		return false
	}

	// Climb to the nearest ancestor that exists, then resolve symlinks from there.
	for {
		if _, err := os.Stat(p); err == nil {
			break
		}
		next := filepath.Dir(p)
		if next == p {
			return false
		}
		p = next
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}

	for {
		if info, err := os.Stat(p); err == nil && os.SameFile(info, parentInfo) {
			return true
		}
		next := filepath.Dir(p)
		if next == p {
			return false
		}
		p = next
	}
}

// pathsOverlap reports whether either directory contains (or is) the other.
func pathsOverlap(a, b string) bool {
	return pathContains(a, b) || pathContains(b, a)
}
