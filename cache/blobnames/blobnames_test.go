package blobnames

import (
	"fmt"
	"testing"

	pb "github.com/buchgr/bazel-remote/v2/genproto/build/bazel/remote/execution/v2"
)

const big = minRecordedSize * 2

func resultWithOutput(path string, hash string, size int64) *pb.ActionResult {
	return &pb.ActionResult{
		OutputFiles: []*pb.OutputFile{{
			Path:   path,
			Digest: &pb.Digest{Hash: hash, SizeBytes: size},
		}},
	}
}

func TestNilRegistryIsANoOp(t *testing.T) {
	var r *Registry

	r.Record(resultWithOutput("foo.jar", "aaa", big), "//foo:bar")

	if _, ok := r.Lookup("aaa"); ok {
		t.Error("a nil registry must not return names")
	}
	if held := r.Len(); held != 0 {
		t.Errorf("nil registry holds %d names, want 0", held)
	}
}

func TestDisabledByNonPositiveCapacity(t *testing.T) {
	if r := NewRegistry(0); r != nil {
		t.Error("capacity 0 must disable the registry")
	}
	if r := NewRegistry(-1); r != nil {
		t.Error("negative capacity must disable the registry")
	}
}

func TestRecordsOutputs(t *testing.T) {
	r := NewRegistry(10)

	ar := resultWithOutput("bazel-out/k8-fastbuild/bin/foo/libbar.jar", "aaa", big)
	ar.StdoutDigest = &pb.Digest{Hash: "bbb", SizeBytes: big}
	ar.OutputDirectories = []*pb.OutputDirectory{{
		Path:       "foo/dir",
		TreeDigest: &pb.Digest{Hash: "ccc", SizeBytes: big},
	}}

	r.Record(ar, "//foo:bar")

	name, ok := r.Lookup("aaa")
	if !ok {
		t.Fatal("output file was not recorded")
	}
	if name.Path != "bazel-out/k8-fastbuild/bin/foo/libbar.jar" {
		t.Errorf("path: got %q", name.Path)
	}
	if name.TargetID != "//foo:bar" {
		t.Errorf("target: got %q", name.TargetID)
	}

	if name, ok := r.Lookup("bbb"); !ok || name.Path != "<stdout>" {
		t.Errorf("stdout digest: ok=%v path=%q", ok, name.Path)
	}
	if name, ok := r.Lookup("ccc"); !ok || name.Path != "foo/dir" {
		t.Errorf("tree digest: ok=%v path=%q", ok, name.Path)
	}
}

func TestIgnoresSmallBlobs(t *testing.T) {
	r := NewRegistry(10)

	r.Record(resultWithOutput("small", "aaa", minRecordedSize-1), "")

	if _, ok := r.Lookup("aaa"); ok {
		t.Error("a blob below the threshold must not be recorded")
	}
}

func TestLaterWriteWithoutMetadataKeepsKnownTarget(t *testing.T) {
	r := NewRegistry(10)

	// A gRPC client sends request metadata; an HTTP client cannot.
	r.Record(resultWithOutput("foo.jar", "aaa", big), "//foo:bar")
	r.Record(resultWithOutput("foo.jar", "aaa", big), "")

	name, ok := r.Lookup("aaa")
	if !ok {
		t.Fatal("name disappeared")
	}
	if name.TargetID != "//foo:bar" {
		t.Errorf("target label was erased by a later write: got %q", name.TargetID)
	}
}

func TestPruningKeepsTheLargest(t *testing.T) {
	const capacity = 100
	r := NewRegistry(capacity)

	// Record more than capacity, with size increasing along with the index.
	const total = capacity * 3
	for i := 0; i < total; i++ {
		hash := fmt.Sprintf("hash-%04d", i)
		r.Record(resultWithOutput("out", hash, big+int64(i)), "")
	}

	if held := r.Len(); held > capacity {
		t.Errorf("held %d names, above the capacity of %d", held, capacity)
	}

	// The largest must have survived, the smallest must not have.
	if _, ok := r.Lookup(fmt.Sprintf("hash-%04d", total-1)); !ok {
		t.Error("the largest blob's name was pruned")
	}
	if _, ok := r.Lookup("hash-0000"); ok {
		t.Error("the smallest blob's name survived pruning")
	}
}

// Retaining a fraction of a tiny capacity rounds down, which used to leave
// small registries permanently empty.
func TestTinyCapacityStillHoldsAName(t *testing.T) {
	for _, capacity := range []int{1, 2, 3} {
		r := NewRegistry(capacity)

		for i := 0; i < 10; i++ {
			hash := fmt.Sprintf("%064x", i)
			r.Record(resultWithOutput("out.jar", hash, big+int64(i)), "")
		}

		held := r.Len()
		if held < 1 {
			t.Errorf("capacity %d: holds %d names, want at least 1", capacity, held)
		}
		if held > capacity {
			t.Errorf("capacity %d: holds %d names, want at most %d", capacity, held, capacity)
		}

		// The largest blob is the one worth keeping.
		largest := fmt.Sprintf("%064x", 9)
		if _, ok := r.Lookup(largest); !ok {
			t.Errorf("capacity %d: dropped the largest blob", capacity)
		}
	}
}
