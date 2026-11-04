// Package blob is the smallest file store that is safe to put other people's media in.
//
// There is no object-storage client here, and that is a decision rather than an omission: the recordings this
// stores are ciphertext the server cannot read (the browser seals them, see internal/virtualclass/recording.go), so a
// bucket's server-side encryption, lifecycle rules and cross-region replication buy nothing that a mounted volume
// does not. What the store does have to get right is the part every file store gets wrong once:
//
//   - **Keys cannot escape the root.** A key is validated segment by segment and then joined; `..`, an absolute
//     path, a URL-encoded slash and a symlink are all refused before a file is opened. A store keyed by anything a
//     client influences is an arbitrary-write primitive otherwise.
//   - **A write is atomic.** Bytes go to a temporary file in the same directory and are renamed into place. A
//     process killed mid-upload leaves a `.tmp` file to be swept, never a half-written chunk that reads as complete.
//   - **A write is bounded.** The reader is wrapped in a limit one byte above the cap so the store can tell "exactly
//     at the limit" from "over it" without reading a gigabyte to find out.
//   - **A delete is idempotent.** Deleting something already gone is success: a retried cleanup must not fail.
package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Errors the callers act on.
var (
	// ErrTooLarge means the body exceeded the cap it was given. It is distinct from a generic write error because the
	// handler answers 413 rather than 500.
	ErrTooLarge = errors.New("blob: the body is larger than the limit")
	// ErrNotFound means there is no file at that key.
	ErrNotFound = errors.New("blob: no such object")
	// ErrBadKey means the key is not in the shape this store accepts.
	ErrBadKey = errors.New("blob: invalid key")
	// ErrClosed means the store has no root configured. Callers turn it into a clear 503 rather than a nil panic.
	ErrClosed = errors.New("blob: no store is configured")
)

// segment is the shape of one path segment: no dots-only names, no separators, nothing a shell or a URL would
// reinterpret. Keys are built by this package, but they are built from ids that arrive over HTTP, so the shape is
// enforced here rather than trusted from the caller.
var segment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Store is a directory of files addressed by slash-separated keys.
type Store struct {
	root string
	// now is injectable so a test can age a temporary file without sleeping through the age it is testing.
	now func() time.Time
}

// Open prepares a store rooted at dir. The directory is created if it does not exist, because a deployment that
// mounts an empty volume should not need a manual mkdir before its first recording.
func Open(dir string) (*Store, error) {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return nil, ErrClosed
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return nil, fmt.Errorf("blob: resolve %q: %w", dir, err)
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, fmt.Errorf("blob: create %q: %w", absolute, err)
	}
	return &Store{root: absolute, now: time.Now}, nil
}

// Root is the absolute path the store writes into. It is for the startup log line, so an operator can see where
// media is going without reading the configuration twice.
func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// Enabled reports whether the store is usable.
func (s *Store) Enabled() bool { return s != nil && s.root != "" }

// validate splits a key and refuses anything that could resolve outside the root.
func (s *Store) validate(key string) ([]string, error) {
	if !s.Enabled() {
		return nil, ErrClosed
	}
	/**
	 * An absolute key is refused rather than quietly made relative. A caller that builds `/etc/passwd` has a bug, and
	 * silently storing it as `etc/passwd` inside the root would hide that bug until it was somewhere worse. The
	 * trailing slash is trimmed because a prefix argument naturally carries one.
	 */
	trimmed := strings.TrimSpace(key)
	if strings.HasPrefix(trimmed, "/") {
		return nil, ErrBadKey
	}
	trimmed = strings.TrimRight(trimmed, "/")
	if trimmed == "" {
		return nil, ErrBadKey
	}
	parts := strings.Split(trimmed, "/")
	for _, part := range parts {
		if !segment.MatchString(part) {
			return nil, ErrBadKey
		}
	}
	return parts, nil
}

// path resolves a validated key under the root, and re-checks the join. The second check is not redundant: it is the
// one that still holds if someone later loosens the regexp.
func (s *Store) path(parts []string) (string, error) {
	joined := filepath.Join(append([]string{s.root}, parts...)...)
	relative, err := filepath.Rel(s.root, joined)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrBadKey
	}
	return joined, nil
}

// PutResult is what a successful write reports.
type PutResult struct {
	// Size is how many bytes were stored.
	Size int64
	// SHA256 is the hex digest of what was written, computed from the bytes that actually landed rather than from
	// what the client claimed. A resumed upload or a truncated body is caught here.
	SHA256 string
}

// Put writes r to key, refusing more than maxBytes. A returned ErrTooLarge means nothing was kept.
func (s *Store) Put(key string, r io.Reader, maxBytes int64) (PutResult, error) {
	parts, err := s.validate(key)
	if err != nil {
		return PutResult{}, err
	}
	target, err := s.path(parts)
	if err != nil {
		return PutResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return PutResult{}, fmt.Errorf("blob: create directory: %w", err)
	}

	// One byte of headroom, so "exactly maxBytes" is accepted and "maxBytes+1" is refused without having to read
	// the rest of the body to know.
	limited := io.LimitReader(r, maxBytes+1)
	temporary, err := os.CreateTemp(filepath.Dir(target), ".upload-*")
	if err != nil {
		return PutResult{}, fmt.Errorf("blob: temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	// A partial file must never survive this function, whatever the exit path.
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}

	digest := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, digest), limited)
	if copyErr != nil {
		cleanup()
		return PutResult{}, fmt.Errorf("blob: write %q: %w", key, copyErr)
	}
	if written > maxBytes {
		cleanup()
		return PutResult{}, ErrTooLarge
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return PutResult{}, fmt.Errorf("blob: sync %q: %w", key, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryName)
		return PutResult{}, fmt.Errorf("blob: close %q: %w", key, err)
	}
	if err := os.Chmod(temporaryName, 0o640); err != nil {
		_ = os.Remove(temporaryName)
		return PutResult{}, fmt.Errorf("blob: chmod %q: %w", key, err)
	}
	// The rename is what makes the object appear: a reader either sees the previous version or this one, never a
	// prefix of a body that is still arriving.
	if err := os.Rename(temporaryName, target); err != nil {
		_ = os.Remove(temporaryName)
		return PutResult{}, fmt.Errorf("blob: commit %q: %w", key, err)
	}
	return PutResult{Size: written, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

// Open returns the stored bytes. The caller closes it.
func (s *Store) Open(key string) (*os.File, error) {
	parts, err := s.validate(key)
	if err != nil {
		return nil, err
	}
	target, err := s.path(parts)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("blob: open %q: %w", key, err)
	}
	return file, nil
}

// Size reports the stored length, or ErrNotFound.
func (s *Store) Size(key string) (int64, error) {
	parts, err := s.validate(key)
	if err != nil {
		return 0, err
	}
	target, err := s.path(parts)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("blob: stat %q: %w", key, err)
	}
	return info.Size(), nil
}

// Delete removes one object. Missing is not an error: a retried cleanup has to succeed.
func (s *Store) Delete(key string) error {
	parts, err := s.validate(key)
	if err != nil {
		return err
	}
	target, err := s.path(parts)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("blob: delete %q: %w", key, err)
	}
	return nil
}

// DeletePrefix removes every object under a key prefix, and then the directories it emptied.
//
// It exists so that deleting a recording is one call rather than a loop the handler can get wrong halfway through.
// A missing prefix is success, for the same reason Delete is idempotent.
func (s *Store) DeletePrefix(prefix string) (int, error) {
	parts, err := s.validate(prefix)
	if err != nil {
		return 0, err
	}
	root, err := s.path(parts)
	if err != nil {
		return 0, err
	}
	removed := 0
	// Walk the tree by hand rather than with filepath.WalkDir: the entries are all one level deep and this keeps the
	// "did it actually go" count honest.
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("blob: read prefix %q: %w", prefix, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			// Nested directories (a prefix with children) are recursed so a caller cannot leak storage by nesting.
			nested, nestedErr := s.DeletePrefix(prefix + "/" + entry.Name())
			removed += nested
			if nestedErr != nil {
				return removed, nestedErr
			}
			continue
		}
		if err := os.Remove(filepath.Join(root, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, fmt.Errorf("blob: delete %q: %w", prefix+"/"+entry.Name(), err)
		}
		removed++
	}
	if err := os.Remove(root); err != nil && !errors.Is(err, os.ErrNotExist) && !isDirectoryNotEmpty(err) {
		return removed, fmt.Errorf("blob: remove directory %q: %w", prefix, err)
	}
	return removed, nil
}

// SweepTemporaries deletes `.upload-*` leftovers older than age. A process that died mid-upload leaves one behind, and
// nothing else will ever claim it, so without this sweep a crash-loop is also a slow disk leak.
func (s *Store) SweepTemporaries(age time.Duration) (int, error) {
	if !s.Enabled() {
		return 0, ErrClosed
	}
	nowFn := s.now
	if nowFn == nil {
		nowFn = time.Now
	}
	limit := age.Seconds()
	removed := 0
	err := filepath.WalkDir(s.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasPrefix(entry.Name(), ".upload-") {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		if nowFn().Sub(info.ModTime()).Seconds() < limit {
			return nil
		}
		if removeErr := os.Remove(path); removeErr == nil {
			removed++
		}
		return nil
	})
	return removed, err
}

func isDirectoryNotEmpty(err error) bool {
	return strings.Contains(err.Error(), "directory not empty")
}
