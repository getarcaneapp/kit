// Package tripwire watches a directory tree for changes and delivers
// debounced, deduplicated batches of caller-defined keys.
//
// A [Watcher] subscribes to every directory under its root (up to
// [Config.MaxDepth]), attaches new directories as they appear, and maps each
// filesystem event to a key with [Config.Key]. Keys collect in a set until the
// tree goes quiet for [Config.Debounce], or until [Config.MaxDelay] passes
// under constant churn. Then they are handed to [Config.OnBatch] in a single
// [Batch].
//
// The key type picks the batching behaviour:
//
//   - Watcher[string] with [PathOf] reports which paths changed.
//   - Watcher[struct{}] with [Signal] collapses any burst into one call.
//
// When events may have been lost (inotify queue overflow, a lost subscription,
// or the root being removed and recreated), the next batch has
// [Batch.Resync] set and the consumer should do a full reconcile.
//
// tripwire uses os.* rather than acfs on purpose: it watches arbitrary
// user-configured paths and, with [Config.FollowSymlinks], follows symlinked
// directories whose targets may live outside any confinement root.
package tripwire
