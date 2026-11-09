package acfs

// Root confines every operation to Path. Its methods take absolute logical paths
// such as "/a/b"; the zero Root fails every operation.
type Root struct {
	// Path is the host directory that logical paths are resolved against.
	Path string
}
