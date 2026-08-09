// Package types contains the public data contracts used by ACFS and its
// command-line protocol.
package types

import "time"

// ProtocolVersion is the current acfs wire protocol version.
const ProtocolVersion = 1

// Entry describes a filesystem entry relative to a workspace root.
type Entry struct {
	ModTime     time.Time `json:"modTime"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Mode        string    `json:"mode"`
	LinkTarget  string    `json:"linkTarget,omitempty"`
	Size        int64     `json:"size"`
	IsDirectory bool      `json:"isDirectory"`
	IsSymlink   bool      `json:"isSymlink"`
}

// ListResponse is the versioned response emitted by the list command.
type ListResponse struct {
	Entries []Entry `json:"entries"`
	Version int     `json:"version"`
}

// StatResponse is the versioned response emitted by the stat command.
type StatResponse struct {
	Entry   Entry `json:"entry"`
	Version int   `json:"version"`
}

// WalkRecord is one versioned record emitted by the walk command.
type WalkRecord struct {
	Entry   Entry `json:"entry"`
	Version int   `json:"version"`
}

// VersionResponse describes an acfs build and its supported protocol.
type VersionResponse struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	BuildTime string `json:"buildTime"`
	Protocol  int    `json:"protocol"`
}
