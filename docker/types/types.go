// Package types holds contracts shared between producers and consumers of the
// docker package.
package types

// ProgressWriterKey is the context key for an io.Writer that receives
// operation progress output, such as a writer wrapping an HTTP response.
type ProgressWriterKey struct{}
