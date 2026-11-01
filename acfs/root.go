package acfs

// Root is a host directory that confines every operation called on it.
//
// Logical paths passed to its methods are absolute ("/a/b") and are
// interpreted relative to Path; nothing outside Path can be read or changed.
// Root is a plain value: build it with a literal, e.g. acfs.Root{Path: dir}.
// The zero Root has an empty Path and every operation on it fails.
type Root struct {
	// Path is the host directory that logical paths are resolved against.
	Path string
}
