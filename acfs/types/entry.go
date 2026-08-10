// Package types contains the public data contracts used by ACFS and its
// command-line protocol.
package types

import "time"

// ProtocolVersion is the current acfs wire protocol version.
const ProtocolVersion = 2

// Entry describes a filesystem entry relative to a workspace root.
type Entry struct {
	ModTime         time.Time `json:"modTime"`
	ModTimeUnixNano int64     `json:"modTimeUnixNano"`
	Name            string    `json:"name"`
	Path            string    `json:"path"`
	Mode            string    `json:"mode"`
	LinkTarget      string    `json:"linkTarget,omitempty"`
	Size            int64     `json:"size"`
	// UnixMode carries the raw os.FileMode bits of the entry, so callers can
	// compare or reapply them. Mode is a human-readable rendering meant for
	// display only.
	UnixMode    uint32 `json:"unixMode"`
	IsDirectory bool   `json:"isDirectory"`
	IsSymlink   bool   `json:"isSymlink"`
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
	Entry     *Entry `json:"entry,omitempty"`
	End       bool   `json:"end,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Count     int    `json:"count,omitempty"`
	Version   int    `json:"version"`
}

// WalkOptions bounds a recursive traversal. Zero values are unlimited.
type WalkOptions struct {
	MaxDepth   int
	MaxEntries int
}

// WalkResult summarizes a bounded recursive traversal.
type WalkResult struct {
	Count     int
	Truncated bool
}

// VersionResponse describes an acfs build and its supported protocol.
type VersionResponse struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	BuildTime string `json:"buildTime"`
	Protocol  int    `json:"protocol"`
}
