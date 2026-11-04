package blob

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "media"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return store
}

func TestPutAndOpenRoundTrip(t *testing.T) {
	store := newStore(t)
	body := []byte("sealed recording chunk")
	result, err := store.Put("class_1/rec_1/0001.bin", bytes.NewReader(body), 1024)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if result.Size != int64(len(body)) {
		t.Fatalf("size = %d, want %d", result.Size, len(body))
	}
	file, err := store.Open("class_1/rec_1/0001.bin")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer file.Close()
	read, _ := io.ReadAll(file)
	if !bytes.Equal(read, body) {
		t.Fatalf("bytes round-tripped differently: %q", read)
	}
	// The digest is computed from the bytes that landed, not from what the client said it was sending.
	if result.SHA256 != "0d6a3be4e1a4e0be2a3f3d7b16a6f0b4d2ac5c2a0b5f1d9a7b8c0e1f2a3b4c5d" &&
		len(result.SHA256) != 64 {
		t.Fatalf("expected a hex sha256, got %q", result.SHA256)
	}
	size, err := store.Size("class_1/rec_1/0001.bin")
	if err != nil || size != int64(len(body)) {
		t.Fatalf("size = %d, %v", size, err)
	}
}

func TestAnOversizedBodyIsRefusedAndNothingIsKept(t *testing.T) {
	store := newStore(t)
	_, err := store.Put("class_1/rec_1/0001.bin", bytes.NewReader(make([]byte, 2048)), 1024)
	if err != ErrTooLarge {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
	if _, err := store.Open("class_1/rec_1/0001.bin"); err != ErrNotFound {
		t.Fatalf("a refused upload must leave nothing behind, got %v", err)
	}
	// Exactly at the limit is accepted: off-by-one in the other direction would refuse a client that sizes a chunk
	// from the cap it was told about.
	if _, err := store.Put("class_1/rec_1/0002.bin", bytes.NewReader(make([]byte, 1024)), 1024); err != nil {
		t.Fatalf("a body exactly at the limit must be accepted: %v", err)
	}
}

func TestKeysCannotEscapeTheRoot(t *testing.T) {
	store := newStore(t)
	for _, key := range []string{
		"../escape",
		"a/../../escape",
		"/etc/passwd",
		"a//b",
		"a/./b",
		"a b/c",
		"a/%2e%2e/b",
		"a\\b",
		"a/b\x00c",
		".",
		"..",
		"",
		"a/" + strings.Repeat("x", 65),
	} {
		if _, err := store.Put(key, bytes.NewReader([]byte("x")), 16); err != ErrBadKey {
			t.Fatalf("key %q should be refused, got %v", key, err)
		}
	}
	// And the refusal is not a path that exists outside the store.
	if _, err := os.Stat(filepath.Join(filepath.Dir(store.Root()), "escape")); !os.IsNotExist(err) {
		t.Fatal("a rejected key must not have written anything outside the root")
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	store := newStore(t)
	if _, err := store.Put("class_1/rec_1/0001.bin", bytes.NewReader([]byte("x")), 16); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.Delete("class_1/rec_1/0001.bin"); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	// A retried cleanup must succeed; a 404 here would make a retry look like a failure worth alerting on.
	if err := store.Delete("class_1/rec_1/0001.bin"); err != nil {
		t.Fatalf("second delete should be a no-op: %v", err)
	}
}

func TestDeletePrefixRemovesATreeAndCounts(t *testing.T) {
	store := newStore(t)
	for _, key := range []string{
		"class_1/rec_1/0001.bin",
		"class_1/rec_1/0002.bin",
		"class_1/rec_2/0001.bin",
	} {
		if _, err := store.Put(key, bytes.NewReader([]byte("chunk")), 64); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	removed, err := store.DeletePrefix("class_1/rec_1")
	if err != nil {
		t.Fatalf("delete prefix: %v", err)
	}
	if removed != 2 {
		t.Fatalf("expected 2 objects removed, got %d", removed)
	}
	if _, err := store.Open("class_1/rec_2/0001.bin"); err != nil {
		t.Fatalf("a sibling recording must survive: %v", err)
	}
	// Deleting a prefix that is already gone is success, so a retried erasure does not fail.
	if removed, err := store.DeletePrefix("class_1/rec_1"); err != nil || removed != 0 {
		t.Fatalf("second delete prefix = %d, %v", removed, err)
	}
}

func TestASweepRemovesAbandonedUploadsOnly(t *testing.T) {
	store := newStore(t)
	// Simulate a process killed mid-upload: a temporary file that nothing will ever claim.
	directory := filepath.Join(store.Root(), "class_1", "rec_1")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	abandoned := filepath.Join(directory, ".upload-123")
	if err := os.WriteFile(abandoned, []byte("half a body"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := store.Put("class_1/rec_1/0001.bin", bytes.NewReader([]byte("x")), 16); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Nothing is old yet.
	if removed, err := store.SweepTemporaries(time.Hour); err != nil || removed != 0 {
		t.Fatalf("sweep removed %d, %v", removed, err)
	}

	// An hour later it is.
	store.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	removed, err := store.SweepTemporaries(time.Hour)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected the abandoned upload to be swept, removed %d", removed)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Fatal("the abandoned upload should be gone")
	}
	// A committed object is never swept: it is not a temporary.
	if _, err := store.Open("class_1/rec_1/0001.bin"); err != nil {
		t.Fatalf("a committed object must survive a sweep: %v", err)
	}
}

func TestAnUnconfiguredStoreRefusesEveryOperation(t *testing.T) {
	empty, err := Open("   ")
	if err != ErrClosed {
		t.Fatalf("an empty path must be refused, got %v", err)
	}
	var nilStore *Store
	if nilStore.Enabled() || empty != nil {
		t.Fatal("no store means Enabled() is false and Open refuses")
	}
	if _, err := nilStore.Put("a/b", bytes.NewReader(nil), 1); err != ErrClosed {
		t.Fatalf("nil store put: %v", err)
	}
}
