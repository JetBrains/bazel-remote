package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/buchgr/bazel-remote/v2/cache/disk"
	testutils "github.com/buchgr/bazel-remote/v2/utils"
)

// countingCache reports a fixed ranking and counts how many times the index
// was walked. StubCache supplies the methods LargestBlobsHandler does not
// use.
type countingCache struct {
	*StubCache

	mu    sync.Mutex
	walks int
	blobs []disk.BlobInfo
}

func (c *countingCache) LargestBlobs(n int) []disk.BlobInfo {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.walks++

	if n < len(c.blobs) {
		return c.blobs[:n]
	}
	return c.blobs
}

func (c *countingCache) Stats() (totalSize int64, reservedSize int64, numItems int, uncompressedSize int64) {
	return 0, 0, len(c.blobs), 0
}

func (c *countingCache) walkCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.walks
}

func newCountingCache(numBlobs int) *countingCache {
	blobs := make([]disk.BlobInfo, 0, numBlobs)
	for i := 0; i < numBlobs; i++ {
		blobs = append(blobs, disk.BlobInfo{
			Kind:       "cas",
			Hash:       fmt.Sprintf("%064x", i),
			Size:       int64(numBlobs - i),
			SizeOnDisk: int64(numBlobs - i),
		})
	}

	return &countingCache{StubCache: &StubCache{}, blobs: blobs}
}

func getLargest(t *testing.T, h *httpCache, query string) largestBlobsData {
	t.Helper()

	w := httptest.NewRecorder()
	h.LargestBlobsHandler(w, httptest.NewRequest(http.MethodGet, "/status/largest"+query, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /status/largest%s: got %d, want %d", query, w.Code, http.StatusOK)
	}

	var data largestBlobsData
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	return data
}

// Walking the index holds the cache lock, so repeated requests must not each
// trigger a walk: that is what lets an unauthenticated request loop stall all
// cache traffic indefinitely.
func TestLargestBlobsSnapshotIsReused(t *testing.T) {
	c := newCountingCache(5)
	h := &httpCache{cache: c, accessLogger: testutils.NewSilentLogger()}

	for i := 0; i < 10; i++ {
		if got := len(getLargest(t, h, "?n=3").Blobs); got != 3 {
			t.Fatalf("request %d: got %d blobs, want 3", i, got)
		}
	}

	if got := c.walkCount(); got != 1 {
		t.Errorf("walked the index %d times, want 1", got)
	}
}

// A later request for a longer list must be served from the same snapshot,
// not trigger a fresh walk.
func TestLargestBlobsSnapshotHoldsMaxEntries(t *testing.T) {
	c := newCountingCache(5)
	h := &httpCache{cache: c, accessLogger: testutils.NewSilentLogger()}

	if got := len(getLargest(t, h, "?n=1").Blobs); got != 1 {
		t.Fatalf("got %d blobs, want 1", got)
	}

	data := getLargest(t, h, "?n=5")
	if got := len(data.Blobs); got != 5 {
		t.Errorf("got %d blobs, want 5", got)
	}
	if got := c.walkCount(); got != 1 {
		t.Errorf("walked the index %d times, want 1", got)
	}
}

// n exceeding the number of cached entries is not an error, and the snapshot
// is capped at maxLargestBlobs however large n is.
func TestLargestBlobsRequestBeyondCacheSize(t *testing.T) {
	c := newCountingCache(3)
	h := &httpCache{cache: c, accessLogger: testutils.NewSilentLogger()}

	if got := len(getLargest(t, h, fmt.Sprintf("?n=%d", maxLargestBlobs*10)).Blobs); got != 3 {
		t.Errorf("got %d blobs, want 3", got)
	}
}
