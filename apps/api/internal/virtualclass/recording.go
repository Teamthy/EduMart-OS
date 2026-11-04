package virtualclass

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/blob"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Recordings, and the honest shape of them.
//
// Audio and video in a room are end-to-end encrypted (see apps/web/lib/live-media-e2ee.ts): the forwarder copies
// ciphertext it cannot open. That makes a *server-side* recorder impossible rather than merely unwise — the process
// that carries the packets never holds the plaintext. A recording therefore has exactly one place it can be made:
// a participant's browser, using the same key everybody in the room already holds.
//
// This file is the server's half of that, and the half it can actually enforce:
//
//	recorder's browser                     API                        store
//	  composite the stage  ──► seal ──► POST .../segments ──► blob.Put(ciphertext)
//	  (only acknowledged peers)                │
//	                                           └──► row: who uploaded what, when, how many bytes
//
// Three things follow, and each is a promise this code keeps rather than a wish:
//
//  1. **The notice is unavoidable.** While a recording is live the class payload carries `recording`, every join
//     response repeats it, and the room renders a banner that cannot be dismissed. A person who stays is a person
//     who was told.
//  2. **Acknowledgement is recorded per person, and given to the mixer.** `GET .../recording` returns the ids of
//     everybody who acknowledged. The recorder is required to include only those people, so a participant who did not
//     acknowledge is not in the file. The server cannot verify a composited frame — so this is stated as a limit
//     rather than claimed as a guarantee, and the recorder's tests check the filter.
//  3. **The bytes expire.** Each recording carries `retain_until` fixed at creation from the deployment's window.
//     A sweep deletes what is past it, host deletion is immediate, and neither leaves a row claiming a file exists.

// Recording statuses.
const (
	RecordingLive     = "recording"
	RecordingSealing  = "sealing"
	RecordingReady    = "ready"
	RecordingFailed   = "failed"
	RecordingDeleted  = "deleted"
	recordingPageSize = 4 << 20 // 4 MiB per chunk: a keyframe plus its dependents, comfortably under a phone's memory
)

// Recording is the host's and the room's view of one capture.
type Recording struct {
	ID         string `json:"id"`
	ClassID    string `json:"classId"`
	Status     string `json:"status"`
	Chunks     int    `json:"chunks"`
	Bytes      int64  `json:"bytes"`
	DurationMS int64  `json:"durationMs"`
	// RetainUntil is the day the bytes go, fixed when the recording started. A host who needs it longer has to keep a
	// copy: extending it silently would be changing the deal after the room agreed to it.
	RetainUntil string  `json:"retainUntil"`
	CreatedAt   string  `json:"createdAt"`
	CompletedAt *string `json:"completedAt"`
	Failure     *string `json:"failureReason"`
	// Segments is the ordered chunk list for playback. Empty until the host asks for the recording itself.
	Segments []RecordingSegment `json:"segments"`
	// Acknowledged is who was told and said so. The recorder is required to include only these people.
	Acknowledged []string `json:"acknowledgedUserIds"`
	// MissingSeqs lists gaps in the recorder's sequence. A recording with a gap plays with a hole, and the UI says so
	// rather than presenting a seamless file that is not one.
	MissingSeqs []int `json:"missingSeqs"`
}

// RecordingSegment is one retrievable chunk.
type RecordingSegment struct {
	ID         string `json:"id"`
	Seq        int    `json:"seq"`
	UserID     string `json:"uploadedBy"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	DurationMS int64  `json:"durationMs"`
	CreatedAt  string `json:"createdAt"`
}

// RecordingConfig is the deployment's side of it.
type RecordingConfig struct {
	// Enabled is false unless a directory is configured. A recording feature that silently writes nowhere is worse
	// than one that is off.
	Enabled bool
	// MaxSegmentBytes caps one uploaded chunk.
	MaxSegmentBytes int64
	// MaxRecordingBytes caps the whole recording, so one class cannot fill a volume.
	MaxRecordingBytes int64
	// Retention is how long a recording is kept. It is copied onto each row at creation.
	Retention time.Duration
}

// WithRecording attaches the store and the limits. Without it every recording endpoint answers 503 with a reason
// rather than pretending the feature is missing.
func (s *Service) WithRecording(store *blob.Store, config RecordingConfig) *Service {
	s.blobs = store
	s.recording = config
	if s.recording.MaxSegmentBytes <= 0 {
		s.recording.MaxSegmentBytes = recordingPageSize
	}
	if s.recording.MaxRecordingBytes <= 0 {
		s.recording.MaxRecordingBytes = 2 << 30
	}
	// No fallback for a zero retention: 0 means "erase at the next sweep", which is a legitimate setting for a test
	// that wants to watch the sweep work, and quietly turning it into thirty days made that impossible to express.
	// A deployment that forgets the variable gets the default from `cmd/api`, which is where the decision belongs.
	return s
}

// recordingReady is the guard every recording path starts with.
func (s *Service) recordingReady() error {
	if s.blobs == nil || !s.blobs.Enabled() || !s.recording.Enabled {
		return httpx.NewError(httpx.CodeUnavailable,
			"Recording is not configured on this deployment: no media directory is mounted for it.")
	}
	return nil
}

// activeRecording returns the recording that is currently capturing, if any.
func (s *Service) activeRecording(ctx context.Context, classID string) (*Recording, error) {
	var recording Recording
	var retainUntil string
	err := s.db.DB.QueryRowContext(ctx, `SELECT id, class_id, status, chunks, bytes, duration_ms, retain_until,
		created_at FROM class_recordings WHERE class_id = ? AND status IN (?, ?) ORDER BY created_at DESC LIMIT 1`,
		classID, RecordingLive, RecordingSealing).
		Scan(&recording.ID, &recording.ClassID, &recording.Status, &recording.Chunks, &recording.Bytes,
			&recording.DurationMS, &retainUntil, &recording.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	recording.RetainUntil = retainUntil
	return &recording, nil
}

// StartRecording turns capture on for a class. Host only, and only while the room is live: recording a class that
// nobody is in produces an empty file and a false claim that it exists.
func (s *Service) StartRecording(ctx context.Context, classID, hostID string) (*Recording, error) {
	if err := s.recordingReady(); err != nil {
		return nil, err
	}
	if err := s.requireHost(ctx, classID, hostID); err != nil {
		return nil, err
	}
	var status string
	if err := s.db.DB.QueryRowContext(ctx, `SELECT status FROM virtual_classes WHERE id = ?`, classID).Scan(&status); err != nil {
		return nil, httpx.NewError(httpx.CodeNotFound, "That class does not exist.")
	}
	if status != "live" {
		return nil, httpx.NewError(httpx.CodeConflict,
			"Start the class before recording it. A recording of an empty room is a file that says nothing happened.")
	}
	active, err := s.activeRecording(ctx, classID)
	if err != nil {
		return nil, err
	}
	if active != nil {
		return active, nil
	}

	id := store.NewID()
	now := store.Now()
	retainUntil := store.TS(time.Now().Add(s.recording.Retention))
	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO class_recordings
		(id, class_id, started_by, status, retain_until, created_at) VALUES (?,?,?,?,?,?)`,
		id, classID, hostID, RecordingLive, retainUntil, now); err != nil {
		return nil, err
	}
	// The recorder's own browser is a participant, so its acknowledgement is implied by pressing record. Everybody
	// else's is not.
	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO class_recording_consents (recording_id, user_id, acknowledged_at)
		VALUES (?,?,?)`, id, hostID, now); err != nil {
		return nil, err
	}
	s.log().Info("class recording started", "classId", classID, "recordingId", id, "hostId", hostID)
	return &Recording{ID: id, ClassID: classID, Status: RecordingLive, RetainUntil: retainUntil, CreatedAt: now}, nil
}

// Acknowledge stores that a participant was told the class is being recorded.
//
// Required for anybody who wants to be *in* the file: the recorder mixes only acknowledged participants, and this row
// is the evidence of who was told. It is idempotent — a page reload must not produce a second row or an error.
func (s *Service) Acknowledge(ctx context.Context, classID, userID string) (int, error) {
	if err := s.recordingReady(); err != nil {
		return 0, err
	}
	if err := s.requireMember(ctx, classID, userID); err != nil {
		return 0, err
	}
	recording, err := s.activeRecording(ctx, classID)
	if err != nil {
		return 0, err
	}
	if recording == nil {
		// Nothing is being captured, so there is nothing to acknowledge. A refusal here would make the client branch
		// on a race rather than on a fact.
		return 0, nil
	}
	if _, err := s.db.DB.ExecContext(ctx, `INSERT INTO class_recording_consents
		(recording_id, user_id, acknowledged_at) VALUES (?,?,?)
		ON CONFLICT (recording_id, user_id) DO NOTHING`, recording.ID, userID, store.Now()); err != nil {
		return 0, err
	}
	return s.acknowledgedCount(ctx, recording.ID)
}

func (s *Service) acknowledgedCount(ctx context.Context, recordingID string) (int, error) {
	var count int
	err := s.db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM class_recording_consents WHERE recording_id = ?`,
		recordingID).Scan(&count)
	return count, err
}

func (s *Service) acknowledgedIDs(ctx context.Context, recordingID string) ([]string, error) {
	rows, err := s.db.DB.QueryContext(ctx, `SELECT user_id FROM class_recording_consents
		WHERE recording_id = ? ORDER BY acknowledged_at`, recordingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AddSegment stores one sealed chunk.
//
// The body is opaque: this code never looks inside it, and could not read it if it wanted to. What it does check is
// that the upload belongs to a live recording, that the recorder is in the room, that the sequence number is fresh, and
// that the running total stays under the cap — the checks that stop a client filling a volume or writing over another
// participant's chunk.
func (s *Service) AddSegment(ctx context.Context, classID, userID string, seq int, durationMS int64, body io.Reader, contentLength int64) (*RecordingSegment, error) {
	if err := s.recordingReady(); err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, classID, userID); err != nil {
		return nil, err
	}
	if seq <= 0 {
		return nil, httpx.Validation(httpx.FieldError{Field: "seq", Message: "Chunk numbers start at 1."}.AsSlice())
	}
	if contentLength > s.recording.MaxSegmentBytes {
		return nil, httpx.NewError(httpx.CodePayloadTooLarge,
			"That chunk is larger than this deployment accepts. The recorder splits the file into smaller chunks.")
	}

	recording, err := s.activeRecording(ctx, classID)
	if err != nil {
		return nil, err
	}
	if recording == nil {
		return nil, httpx.NewError(httpx.CodeConflict, "Nothing is being recorded in this class.")
	}
	if recording.Status != RecordingLive {
		return nil, httpx.NewError(httpx.CodeConflict, "The recording has stopped accepting chunks.")
	}
	if recording.Bytes >= s.recording.MaxRecordingBytes {
		return nil, httpx.NewError(httpx.CodePayloadTooLarge,
			"This recording has reached its size limit. Stop it and start a new one.")
	}
	// A re-upload of the same sequence is refused rather than overwritten: a chunk that changes underneath a download
	// makes the digest meaningless, and a retry of a request that already succeeded is indistinguishable from a
	// rewrite.
	var existing string
	err = s.db.DB.QueryRowContext(ctx, `SELECT id FROM class_recording_segments
		WHERE recording_id = ? AND user_id = ? AND seq = ?`, recording.ID, userID, seq).Scan(&existing)
	if err == nil {
		return nil, httpx.NewError(httpx.CodeConflict, "That chunk has already been uploaded.")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// The key is derived from server-side ids only. A client-supplied name never reaches the filesystem.
	segmentID := store.NewID()
	key := "classes/" + classID + "/recordings/" + recording.ID + "/" + segmentID
	result, err := s.blobs.Put(key, body, s.recording.MaxSegmentBytes)
	if errors.Is(err, blob.ErrTooLarge) {
		return nil, httpx.NewError(httpx.CodePayloadTooLarge, "That chunk is larger than this deployment accepts.")
	}
	if err != nil {
		return nil, err
	}

	now := store.Now()
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		_ = s.blobs.Delete(key)
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO class_recording_segments
		(id, recording_id, user_id, seq, storage_key, bytes, sha256, duration_ms, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		segmentID, recording.ID, userID, seq, key, result.Size, result.SHA256, durationMS, now); err != nil {
		_ = s.blobs.Delete(key)
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE class_recordings
		SET chunks = chunks + 1, bytes = bytes + ?, duration_ms = duration_ms + ? WHERE id = ?`,
		result.Size, durationMS, recording.ID); err != nil {
		_ = s.blobs.Delete(key)
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		_ = s.blobs.Delete(key)
		return nil, err
	}
	return &RecordingSegment{ID: segmentID, Seq: seq, UserID: userID, Bytes: result.Size, SHA256: result.SHA256,
		DurationMS: durationMS, CreatedAt: now}, nil
}

// CompleteRecording stops accepting chunks and marks the file ready, or failed when nothing arrived.
//
// A recording with no chunks is `failed`, not `ready`: a host who pressed record and got nothing must be told that,
// because the alternative is a class that believes it has a recording and does not.
func (s *Service) CompleteRecording(ctx context.Context, classID, userID string) (*Recording, error) {
	if err := s.recordingReady(); err != nil {
		return nil, err
	}
	if err := s.requireHost(ctx, classID, userID); err != nil {
		return nil, err
	}
	recording, err := s.activeRecording(ctx, classID)
	if err != nil {
		return nil, err
	}
	if recording == nil {
		return nil, httpx.NewError(httpx.CodeNotFound, "There is no recording to finish in this class.")
	}
	now := store.Now()
	status, failure := RecordingReady, any(nil)
	if recording.Chunks == 0 {
		status = RecordingFailed
		failure = "No audio or video reached the server. If the recorder's browser was closed before it uploaded anything, there is nothing to keep."
	}
	if _, err := s.db.DB.ExecContext(ctx, `UPDATE class_recordings
		SET status = ?, completed_at = ?, failure_reason = ? WHERE id = ?`,
		status, now, failure, recording.ID); err != nil {
		return nil, err
	}
	s.log().Info("class recording finished", "classId", classID, "recordingId", recording.ID,
		"status", status, "chunks", recording.Chunks, "bytes", recording.Bytes)
	return s.Recording(ctx, classID, userID)
}

// Recording returns the newest recording of a class, with its segments and the list of who acknowledged.
//
// Readable by the host and by anybody in the room: a recording of a class is part of that class, and hiding it from the
// people who were in it would be strange. It is *not* readable by a stranger with the class's public slug — the
// requireMember check is what keeps a recording from being a public download.
func (s *Service) Recording(ctx context.Context, classID, userID string) (*Recording, error) {
	if err := s.recordingReady(); err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, classID, userID); err != nil {
		return nil, err
	}
	var recording Recording
	var retainUntil, createdAt string
	var completedAt, failure sql.NullString
	err := s.db.DB.QueryRowContext(ctx, `SELECT id, class_id, status, chunks, bytes, duration_ms, retain_until,
		created_at, completed_at, failure_reason FROM class_recordings
		WHERE class_id = ? AND status <> ? ORDER BY created_at DESC LIMIT 1`,
		classID, RecordingDeleted).Scan(&recording.ID, &recording.ClassID, &recording.Status, &recording.Chunks,
		&recording.Bytes, &recording.DurationMS, &retainUntil, &createdAt, &completedAt, &failure)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound,
			"This class has not been recorded. A host can start one while the class is live.")
	}
	if err != nil {
		return nil, err
	}
	recording.RetainUntil = retainUntil
	recording.CreatedAt = createdAt
	if completedAt.Valid {
		recording.CompletedAt = &completedAt.String
	}
	if failure.Valid {
		recording.Failure = &failure.String
	}

	rows, err := s.db.DB.QueryContext(ctx, `SELECT id, seq, user_id, bytes, sha256, duration_ms, created_at
		FROM class_recording_segments WHERE recording_id = ? ORDER BY seq`, recording.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recording.Segments = []RecordingSegment{}
	seen := map[int]bool{}
	highest := 0
	for rows.Next() {
		var segment RecordingSegment
		if err := rows.Scan(&segment.ID, &segment.Seq, &segment.UserID, &segment.Bytes, &segment.SHA256,
			&segment.DurationMS, &segment.CreatedAt); err != nil {
			return nil, err
		}
		recording.Segments = append(recording.Segments, segment)
		seen[segment.Seq] = true
		if segment.Seq > highest {
			highest = segment.Seq
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	recording.MissingSeqs = []int{}
	for seq := 1; seq <= highest; seq++ {
		if !seen[seq] {
			recording.MissingSeqs = append(recording.MissingSeqs, seq)
		}
	}
	if recording.Acknowledged, err = s.acknowledgedIDs(ctx, recording.ID); err != nil {
		return nil, err
	}
	if recording.Acknowledged == nil {
		recording.Acknowledged = []string{}
	}
	return &recording, nil
}

// Segment opens one chunk for download.
func (s *Service) Segment(ctx context.Context, classID, userID, segmentID string) (*os.File, int64, error) {
	if err := s.recordingReady(); err != nil {
		return nil, 0, err
	}
	if err := s.requireMember(ctx, classID, userID); err != nil {
		return nil, 0, err
	}
	var key string
	var size int64
	err := s.db.DB.QueryRowContext(ctx, `SELECT s.storage_key, s.bytes FROM class_recording_segments s
		JOIN class_recordings r ON r.id = s.recording_id
		WHERE s.id = ? AND r.class_id = ? AND r.status <> ?`, segmentID, classID, RecordingDeleted).
		Scan(&key, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, httpx.NewError(httpx.CodeNotFound, "That part of the recording does not exist.")
	}
	if err != nil {
		return nil, 0, err
	}
	file, err := s.blobs.Open(key)
	if errors.Is(err, blob.ErrNotFound) {
		// A row without a file is a real fault, and saying so beats handing back an empty download.
		return nil, 0, httpx.NewError(httpx.CodeInternal, "This part of the recording is missing from storage.")
	}
	if err != nil {
		return nil, 0, err
	}
	return file, size, nil
}

// DeleteRecording erases the bytes and marks the row deleted. Host only, and immediate: "delete" that leaves the file
// on disk until a sweep is not a deletion anyone asked for.
func (s *Service) DeleteRecording(ctx context.Context, classID, userID string) (int, error) {
	if err := s.recordingReady(); err != nil {
		return 0, err
	}
	if err := s.requireHost(ctx, classID, userID); err != nil {
		return 0, err
	}
	var recordingID string
	err := s.db.DB.QueryRowContext(ctx, `SELECT id FROM class_recordings WHERE class_id = ? AND status <> ?
		ORDER BY created_at DESC LIMIT 1`, classID, RecordingDeleted).Scan(&recordingID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, httpx.NewError(httpx.CodeNotFound, "There is no recording to delete.")
	}
	if err != nil {
		return 0, err
	}
	removed, err := s.blobs.DeletePrefix("classes/" + classID + "/recordings/" + recordingID)
	if err != nil {
		return removed, err
	}
	if _, err := s.db.DB.ExecContext(ctx, `UPDATE class_recordings SET status = ?, deleted_at = ?, bytes = 0
		WHERE id = ?`, RecordingDeleted, store.Now(), recordingID); err != nil {
		return removed, err
	}
	if _, err := s.db.DB.ExecContext(ctx, `DELETE FROM class_recording_segments WHERE recording_id = ?`,
		recordingID); err != nil {
		return removed, err
	}
	s.log().Info("class recording deleted", "classId", classID, "recordingId", recordingID, "objects", removed)
	return removed, nil
}

// ExpireRecordings deletes what is past its retention window. It runs on a sweep, and it is deliberately blunt:
// the promise made at creation is that the bytes go on a date, and a sweep that skips a row is a broken promise.
func (s *Service) ExpireRecordings(ctx context.Context, limit int) (int, error) {
	if s.blobs == nil || !s.blobs.Enabled() {
		return 0, nil
	}
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.db.DB.QueryContext(ctx, `SELECT id, class_id FROM class_recordings
		WHERE status <> ? AND retain_until <= ? LIMIT ?`, RecordingDeleted, store.TS(time.Now()), limit)
	if err != nil {
		return 0, err
	}
	type expired struct{ id, classID string }
	pending := []expired{}
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.id, &item.classID); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	expiredCount := 0
	for _, item := range pending {
		if _, err := s.blobs.DeletePrefix("classes/" + item.classID + "/recordings/" + item.id); err != nil {
			s.log().Warn("expired recording could not be erased", "recordingId", item.id, "error", err.Error())
			continue
		}
		if _, err := s.db.DB.ExecContext(ctx, `UPDATE class_recordings SET status = ?, deleted_at = ?, bytes = 0
			WHERE id = ?`, RecordingDeleted, store.Now(), item.id); err != nil {
			return expiredCount, err
		}
		if _, err := s.db.DB.ExecContext(ctx, `DELETE FROM class_recording_segments WHERE recording_id = ?`,
			item.id); err != nil {
			return expiredCount, err
		}
		expiredCount++
	}
	if expiredCount > 0 {
		s.log().Info("recordings expired past their retention window", "count", expiredCount)
	}
	return expiredCount, nil
}

// ── handlers ───────────────────────────────────────────────────────────────────────────────────────

// StartRecording handles POST /v1/virtual-classes/{id}/recording.
func (h *Handlers) StartRecording(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	recording, err := h.svc.StartRecording(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"recording": recording})
	return nil
}

// AcknowledgeRecording handles POST /v1/virtual-classes/{id}/recording/acknowledge.
func (h *Handlers) AcknowledgeRecording(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	count, err := h.svc.Acknowledge(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"acknowledged": true, "acknowledgedCount": count})
	return nil
}

// AddRecordingSegment handles POST /v1/virtual-classes/{id}/recording/segments.
//
// The body is the sealed chunk. It is deliberately not JSON: base64 in JSON would inflate every byte by a third and
// buffer the whole chunk in memory to decode it, and this path exists to move megabytes at a time.
func (h *Handlers) AddRecordingSegment(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	seq, convErr := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("seq")))
	if convErr != nil {
		return httpx.Validation(httpx.FieldError{
			Field:   "seq",
			Message: "Chunk numbers are whole numbers starting at 1.",
		}.AsSlice())
	}
	durationMS, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("durationMs")), 10, 64)
	segment, err := h.svc.AddSegment(r.Context(), classID(r), user.ID, seq, durationMS, r.Body, r.ContentLength)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"segment": segment})
	return nil
}

// CompleteRecording handles POST /v1/virtual-classes/{id}/recording/complete.
func (h *Handlers) CompleteRecording(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	recording, err := h.svc.CompleteRecording(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"recording": recording})
	return nil
}

// Recording handles GET /v1/virtual-classes/{id}/recording.
func (h *Handlers) Recording(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	recording, err := h.svc.Recording(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"recording": recording})
	return nil
}

// RecordingSegment handles GET /v1/virtual-classes/{id}/recording/segments/{segmentId}.
//
// `http.ServeContent` does the range handling, conditional requests and content-length, which is what makes seeking in
// a long class work: a player asks for bytes near the end and gets them without downloading the room's first hour.
func (h *Handlers) RecordingSegment(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	file, size, err := h.svc.Segment(r.Context(), classID(r), user.ID, chi.URLParam(r, "segmentId"))
	if err != nil {
		return err
	}
	defer file.Close()
	// No store, no shared cache: the body is ciphertext that only the room can open, but the fact of who attended a
	// class is not something to leak to an intermediary either.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Content-Disposition", `attachment; filename="segment.bin"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The file itself, not a section of it: `os.File` is already a `ReadSeeker`, and wrapping it in a
	// `io.SectionReader` made ServeContent answer a range request with 416 — a wrong size seen by the range parser.
	// `size` is still checked against what the row says, so a file that shrank under us is reported rather than served
	// short.
	if info, statErr := file.Stat(); statErr != nil || info.Size() != size {
		return httpx.NewError(httpx.CodeInternal, "This part of the recording does not match its record.")
	}
	http.ServeContent(w, r, "segment.bin", time.Time{}, file)
	return nil
}

// DeleteRecording handles DELETE /v1/virtual-classes/{id}/recording.
func (h *Handlers) DeleteRecording(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	removed, err := h.svc.DeleteRecording(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deleted": true, "objectsRemoved": removed})
	return nil
}
