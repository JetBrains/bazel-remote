// Package blobnames records which build output each large CAS blob was
// written as, taken from ActionResults as they are stored, so that
// /status/largest can name the blobs filling the cache.
//
// Nothing else in bazel-remote connects a blob to the build that produced it:
// a CAS blob is addressed only by its digest, so attributing one after the
// fact means scanning every Action Cache entry and hoping the one referencing
// it has not been evicted.
//
// The registry is in-memory, bounded, and lost on restart. A nil *Registry is
// a working no-op, so callers do not need to check whether the feature is
// enabled.
package blobnames

import (
	"sort"
	"sync"

	pb "github.com/buchgr/bazel-remote/v2/genproto/build/bazel/remote/execution/v2"
)

// Blobs smaller than this are not worth remembering: recording every tiny
// output would spend the capacity on blobs nobody will ask about.
const minRecordedSize = 1024 * 1024

// Name is one origin of a CAS blob. A blob is identified by its content, so
// the same one can be an output of several targets; only the most recently
// recorded naming is kept.
type Name struct {
	// Path is the exec-root-relative output path, e.g.
	// "bazel-out/k8-fastbuild/bin/platform/util/libutil.jar". For an output
	// directory it is the directory's path; for stdout/stderr, a placeholder.
	Path string

	// TargetID is the Bazel label from the client's RequestMetadata, e.g.
	// "//platform/util:util". Empty when the client sent no request metadata,
	// as the HTTP cache protocol never does.
	TargetID string

	// Size is the logical size from the ActionResult's digest, used to decide
	// which names to keep when pruning.
	Size int64
}

// Registry maps CAS blob hashes to the build output they were produced as.
// It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	names    map[string]Name
	capacity int
}

// NewRegistry returns a registry holding names for at most capacity blobs, or
// nil if capacity is not positive. A nil registry is a no-op.
func NewRegistry(capacity int) *Registry {
	if capacity <= 0 {
		return nil
	}

	return &Registry{
		names:    make(map[string]Name, capacity),
		capacity: capacity,
	}
}

// Record notes the large outputs of one ActionResult. targetID comes from the
// client's RequestMetadata and may be empty.
func (r *Registry) Record(ar *pb.ActionResult, targetID string) {
	if r == nil || ar == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, file := range ar.OutputFiles {
		r.recordLocked(file.GetDigest(), file.GetPath(), targetID)
	}

	for _, dir := range ar.OutputDirectories {
		// Only the Tree proto itself can be named here. The blobs *inside* a
		// tree artifact are listed in that Tree, and reading it back would
		// put a CAS read on the critical path of every AC write.
		r.recordLocked(dir.GetTreeDigest(), dir.GetPath(), targetID)
	}

	r.recordLocked(ar.GetStdoutDigest(), "<stdout>", targetID)
	r.recordLocked(ar.GetStderrDigest(), "<stderr>", targetID)

	r.pruneLocked()
}

// recordLocked requires r.mu to be held.
func (r *Registry) recordLocked(digest *pb.Digest, path string, targetID string) {
	if digest == nil || digest.Hash == "" || digest.SizeBytes < minRecordedSize {
		return
	}

	// Don't let a rewrite by a client that sent no request metadata erase a
	// target label we already know.
	existing, ok := r.names[digest.Hash]
	if ok && existing.TargetID != "" && targetID == "" {
		return
	}

	r.names[digest.Hash] = Name{
		Path:     path,
		TargetID: targetID,
		Size:     digest.SizeBytes,
	}
}

// pruneLocked drops the smallest names once capacity is exceeded, keeping
// somewhat fewer than capacity so that it does not run on nearly every write.
// It requires r.mu to be held.
func (r *Registry) pruneLocked() {
	if len(r.names) <= r.capacity {
		return
	}

	type sized struct {
		hash string
		size int64
	}

	all := make([]sized, 0, len(r.names))
	for hash, name := range r.names {
		all = append(all, sized{hash: hash, size: name.Size})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].size > all[j].size })

	keep := r.capacity * 3 / 4
	if keep < 1 {
		// Rounding down would otherwise leave a tiny registry empty.
		keep = 1
	}

	for _, dropped := range all[keep:] {
		delete(r.names, dropped.hash)
	}
}

// Lookup returns what is known about the blob with the given hash.
func (r *Registry) Lookup(hash string) (Name, bool) {
	if r == nil {
		return Name{}, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	name, ok := r.names[hash]
	return name, ok
}

// Len returns the number of names held.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.names)
}
