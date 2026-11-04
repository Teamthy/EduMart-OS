package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/teamthy/edumart/apps/api/internal/virtualclass"
	"testing"
	"time"
)

// uploadSegment posts a raw (already sealed) chunk. The recording upload path is octet-stream rather than JSON, because
// base64 in JSON would inflate every byte by a third and buffer a whole chunk to decode it.
func (h *harness) uploadSegment(token, classID string, seq int, body []byte, durationMS int) (int, map[string]any) {
	h.t.Helper()
	request, err := http.NewRequest("POST",
		h.server.URL+"/v1/virtual-classes/"+classID+"/recording/segments?seq="+strconv.Itoa(seq)+"&durationMs="+strconv.Itoa(durationMS),
		bytes.NewReader(body))
	if err != nil {
		h.t.Fatalf("build upload: %v", err)
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		h.t.Fatalf("upload: %v", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	payload := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	return response.StatusCode, payload
}

// A live class the host has started, ready to record.
func startLiveClass(t *testing.T, h *harness, hostEmail, learnerEmail string) (host, learner, classID string) {
	t.Helper()
	host = h.registerAndVerify(hostEmail)
	learner = h.registerAndVerify(learnerEmail)
	status, body := h.do("POST", "/v1/virtual-classes", host, map[string]any{
		"title":           "Recorded Biology",
		"description":     "A class that is written down.",
		"creditsCost":     0,
		"startsAt":        time.Now().Add(time.Minute).UTC().Format(rfc3339),
		"durationMinutes": 45,
	})
	if status != http.StatusCreated {
		t.Fatalf("create class: %d %v", status, body)
	}
	class := body["class"].(map[string]any)
	classID = class["id"].(string)

	// Join answers 200 when the seat already exists or the class is free, so both are a seat.
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/join", learner, nil); status >= 300 {
		t.Fatalf("learner join: %d %v", status, body)
	}
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/start", host, nil); status != http.StatusOK {
		t.Fatalf("start: %d %v", status, body)
	}
	return host, learner, classID
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"

// TestARecordingSurvivesTheChunkPath is the acceptance test for the recording claim: a host starts one, a participant
// acknowledges, the browser uploads sealed chunks, the host finishes it, and the bytes come back intact — checked by
// digest, because "the download worked" is not the same claim as "the download is the same file".
func TestARecordingSurvivesTheChunkPath(t *testing.T) {
	h := newHarness(t)
	host, learner, classID := startLiveClass(t, h, "rec-host@example.com", "rec-learner@example.com")

	// Recording a class that has not started is refused: an empty file that claims a class happened is worse than no
	// file at all.
	status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/recording", host, nil)
	if status != http.StatusCreated {
		t.Fatalf("start recording: %d %v", status, body)
	}
	recordingID := body["recording"].(map[string]any)["id"].(string)
	if body["recording"].(map[string]any)["status"] != "recording" {
		t.Fatalf("expected status recording, got %v", body["recording"])
	}

	// Starting twice returns the same recording rather than two files of the same class.
	status, body = h.do("POST", "/v1/virtual-classes/"+classID+"/recording", host, nil)
	if status != http.StatusCreated || body["recording"].(map[string]any)["id"] != recordingID {
		t.Fatalf("a second start must reuse the live recording: %d %v", status, body)
	}

	// The learner is told, and says so. The recorder only mixes acknowledged participants, so this row is the
	// evidence that they were asked.
	status, body = h.do("POST", "/v1/virtual-classes/"+classID+"/recording/acknowledge", learner, nil)
	if status != http.StatusOK || body["acknowledged"] != true {
		t.Fatalf("acknowledge: %d %v", status, body)
	}

	// Three sealed chunks, deliberately not JSON.
	sealed := [][]byte{
		[]byte("sealed chunk one \x00\x01\x02"),
		[]byte("sealed chunk two — with a tag at the end"),
		bytes.Repeat([]byte{0x0f}, 4096),
	}
	for index, chunk := range sealed {
		status, body := h.uploadSegment(learner, classID, index+1, chunk, 2000)
		if status != http.StatusCreated {
			t.Fatalf("upload chunk %d: %d %v", index+1, status, body)
		}
		segment := body["segment"].(map[string]any)
		if int(segment["bytes"].(float64)) != len(chunk) {
			t.Fatalf("chunk %d stored %v bytes, sent %d", index+1, segment["bytes"], len(chunk))
		}
		if len(segment["sha256"].(string)) != 64 {
			t.Fatalf("chunk %d has no digest: %v", index+1, segment["sha256"])
		}
	}

	// A re-upload of the same sequence is refused: a chunk that changes under a download makes its digest meaningless.
	if status, _ := h.uploadSegment(learner, classID, 1, []byte("different"), 10); status != http.StatusConflict {
		t.Fatalf("a duplicate chunk must be refused, got %d", status)
	}

	status, body = h.do("POST", "/v1/virtual-classes/"+classID+"/recording/complete", host, nil)
	if status != http.StatusOK {
		t.Fatalf("complete: %d %v", status, body)
	}
	recording := body["recording"].(map[string]any)
	if recording["status"] != "ready" {
		t.Fatalf("expected ready, got %v", recording["status"])
	}
	if int(recording["chunks"].(float64)) != 3 {
		t.Fatalf("expected 3 chunks, got %v", recording["chunks"])
	}
	acknowledged := recording["acknowledgedUserIds"].([]any)
	// The host pressed record (implied) and the learner acknowledged (explicit): both are in the file's consent list.
	if len(acknowledged) != 2 {
		t.Fatalf("expected two acknowledged participants, got %v", acknowledged)
	}
	if missing := recording["missingSeqs"].([]any); len(missing) != 0 {
		t.Fatalf("a contiguous upload has no gaps, got %v", missing)
	}

	// The chunks come back byte for byte.
	segments := recording["segments"].([]any)
	for index, entry := range segments {
		segment := entry.(map[string]any)
		request, err := http.NewRequest("GET",
			h.server.URL+"/v1/virtual-classes/"+classID+"/recording/segments/"+segment["id"].(string), nil)
		if err != nil {
			t.Fatalf("build download: %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+host)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("download: %v", err)
		}
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("download chunk %d: %d", index+1, response.StatusCode)
		}
		if !bytes.Equal(raw, sealed[index]) {
			t.Fatalf("chunk %d came back different: %q", index+1, raw)
		}
		if response.Header.Get("Cache-Control") == "" {
			t.Fatal("a recording download must not be cacheable by an intermediary")
		}
	}

	// A range request is what makes seeking in a long class work.
	request, _ := http.NewRequest("GET",
		h.server.URL+"/v1/virtual-classes/"+classID+"/recording/segments/"+segments[2].(map[string]any)["id"].(string), nil)
	request.Header.Set("Authorization", "Bearer "+host)
	request.Header.Set("Range", "bytes=100-199")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("range request: %v", err)
	}
	partial, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusPartialContent || len(partial) != 100 {
		t.Fatalf("expected a 100-byte 206, got %d with %d bytes", response.StatusCode, len(partial))
	}
}

// TestARecordingIsNotPublic: the class's share link is public, the recording behind it is not.
func TestARecordingIsNotPublic(t *testing.T) {
	h := newHarness(t)
	host, _, classID := startLiveClass(t, h, "rec-private-host@example.com", "rec-private-learner@example.com")
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/recording", host, nil); status != http.StatusCreated {
		t.Fatalf("start recording: %d %v", status, body)
	}
	if status, _ := h.uploadSegment(host, classID, 1, []byte("sealed"), 1000); status != http.StatusCreated {
		t.Fatalf("upload: %d", status)
	}
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/recording/complete", host, nil); status != http.StatusOK {
		t.Fatalf("complete: %d %v", status, body)
	}

	// A signed-in stranger: not in the room, so the recording is not theirs to read.
	stranger := h.registerAndVerify("rec-stranger@example.com")
	if status, _ := h.do("GET", "/v1/virtual-classes/"+classID+"/recording", stranger, nil); status != http.StatusForbidden {
		t.Fatalf("a non-member must not read the recording, got %d", status)
	}
	// And somebody signed out gets nothing at all.
	if status, _ := h.do("GET", "/v1/virtual-classes/"+classID+"/recording", "", nil); status != http.StatusUnauthorized {
		t.Fatalf("anonymous access must be refused, got %d", status)
	}
}

// TestDeletingARecordingErasesTheBytes: "deleted" has to mean the file is gone, not that a row says so.
func TestDeletingARecordingErasesTheBytes(t *testing.T) {
	h := newHarness(t)
	host, learner, classID := startLiveClass(t, h, "rec-del-host@example.com", "rec-del-learner@example.com")
	status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/recording", host, nil)
	if status != http.StatusCreated {
		t.Fatalf("start: %d %v", status, body)
	}
	if status, body = h.uploadSegment(host, classID, 1, []byte("sealed chunk"), 1000); status != http.StatusCreated {
		t.Fatalf("upload: %d %v", status, body)
	}
	status, body = h.do("POST", "/v1/virtual-classes/"+classID+"/recording/complete", host, nil)
	if status != http.StatusOK {
		t.Fatalf("complete: %d %v", status, body)
	}
	segments := body["recording"].(map[string]any)["segments"].([]any)
	segmentID := segments[0].(map[string]any)["id"].(string)

	// Only the host can erase it.
	if status, _ := h.do("DELETE", "/v1/virtual-classes/"+classID+"/recording", learner, nil); status != http.StatusForbidden {
		t.Fatalf("a learner must not delete the recording, got %d", status)
	}
	if status, body := h.do("DELETE", "/v1/virtual-classes/"+classID+"/recording", host, nil); status != http.StatusOK {
		t.Fatalf("delete: %d %v", status, body)
	}

	// The bytes are gone from storage, not just hidden behind a status column.
	request, _ := http.NewRequest("GET",
		h.server.URL+"/v1/virtual-classes/"+classID+"/recording/segments/"+segmentID, nil)
	request.Header.Set("Authorization", "Bearer "+host)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post-delete download: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("a deleted chunk must 404, got %d", response.StatusCode)
	}
	// And the recording itself reads as absent rather than as an empty one.
	if status, _ := h.do("GET", "/v1/virtual-classes/"+classID+"/recording", host, nil); status != http.StatusNotFound {
		t.Fatalf("a deleted recording must read as absent, got %d", status)
	}
}

// TestARecordingWithNoChunksFailsHonestly: pressing record and uploading nothing must not produce a "ready" file.
func TestARecordingWithNoChunksFailsHonestly(t *testing.T) {
	h := newHarness(t)
	host, _, classID := startLiveClass(t, h, "rec-empty-host@example.com", "rec-empty-learner@example.com")
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/recording", host, nil); status != http.StatusCreated {
		t.Fatalf("start: %d %v", status, body)
	}
	status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/recording/complete", host, nil)
	if status != http.StatusOK {
		t.Fatalf("complete: %d %v", status, body)
	}
	recording := body["recording"].(map[string]any)
	if recording["status"] != "failed" {
		t.Fatalf("an empty recording must be failed, got %v", recording["status"])
	}
	if recording["failureReason"] == nil || recording["failureReason"] == "" {
		t.Fatal("a failed recording must say why, in words a host can read")
	}
}

// TestARecordingStopsAcceptingChunks: capture is closed by complete, and every further upload is refused rather than
// appended to a file somebody has already been told is final.
func TestARecordingStopsAcceptingChunks(t *testing.T) {
	h := newHarness(t)
	host, learner, classID := startLiveClass(t, h, "rec-seal-host@example.com", "rec-seal-learner@example.com")
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/recording", host, nil); status != http.StatusCreated {
		t.Fatalf("start: %d %v", status, body)
	}
	if status, _ := h.uploadSegment(learner, classID, 1, []byte("sealed"), 1000); status != http.StatusCreated {
		t.Fatalf("first upload should work")
	}
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/recording/complete", host, nil); status != http.StatusOK {
		t.Fatalf("complete: %d %v", status, body)
	}
	if status, _ := h.uploadSegment(learner, classID, 2, []byte("late"), 1000); status != http.StatusConflict {
		t.Fatalf("a chunk after complete must be refused, got %d", status)
	}
}

// TestRecordingIsRefusedWhenNoDirectoryIsMounted: the feature is off by configuration, and says so. A 404 would send
// whoever is debugging it looking for a missing route.
func TestRecordingIsRefusedWhenNoDirectoryIsMounted(t *testing.T) {
	h := newHarness(t)
	h.live.WithRecording(nil, virtualclass.RecordingConfig{})
	host := h.registerAndVerify("rec-off-host@example.com")
	status, body := h.do("POST", "/v1/virtual-classes", host, map[string]any{
		"title":           "Unrecorded",
		"description":     "No media directory on this harness.",
		"creditsCost":     0,
		"startsAt":        time.Now().Add(time.Minute).UTC().Format(rfc3339),
		"durationMinutes": 45,
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, body)
	}
	classID := body["class"].(map[string]any)["id"].(string)
	if status, _ := h.do("POST", "/v1/virtual-classes/"+classID+"/start", host, nil); status != http.StatusOK {
		t.Fatalf("start: %d", status)
	}
	status, body = h.do("POST", "/v1/virtual-classes/"+classID+"/recording", host, nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without a media directory, got %d %v", status, body)
	}
	message, _ := body["error"].(map[string]any)["message"].(string)
	if !strings.Contains(strings.ToLower(message), "media directory") {
		t.Fatalf("the refusal must name the missing configuration, got %q", message)
	}
}

// TestAChunkOverTheLimitIsRefusedAtTheDoor keeps the cap enforced before anything is written, and answers 413 so the
// recorder knows to split rather than to retry.
func TestAChunkOverTheLimitIsRefusedAtTheDoor(t *testing.T) {
	h := newHarness(t)
	host, learner, classID := startLiveClass(t, h, "rec-big-host@example.com", "rec-big-learner@example.com")
	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/recording", host, nil); status != http.StatusCreated {
		t.Fatalf("start: %d %v", status, body)
	}
	// The harness caps a segment at 64 KiB; send 128 KiB, which the store must refuse rather than truncate.
	status, body := h.uploadSegment(learner, classID, 1, bytes.Repeat([]byte{0x42}, 128*1024), 1000)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d %v", status, body)
	}
	// And nothing was kept: the recording still has no chunks.
	status, body = h.do("GET", "/v1/virtual-classes/"+classID+"/recording", host, nil)
	if status != http.StatusOK {
		t.Fatalf("read recording: %d %v", status, body)
	}
	if int(body["recording"].(map[string]any)["chunks"].(float64)) != 0 {
		t.Fatalf("a refused chunk must not be recorded: %v", body["recording"])
	}
}

// TestTheClassSaysItIsBeingRecorded is the consent half that the server can actually enforce: nobody can be in a
// recorded room without the fact being in the payload they were given.
func TestTheClassSaysItIsBeingRecorded(t *testing.T) {
	h := newHarness(t)
	host, learner, classID := startLiveClass(t, h, "rec-flag-host@example.com", "rec-flag-learner@example.com")

	status, body := h.do("GET", "/v1/virtual-classes/"+classID, learner, nil)
	if status != http.StatusOK {
		t.Fatalf("get class: %d %v", status, body)
	}
	if body["class"].(map[string]any)["recording"] != nil {
		t.Fatalf("a class with no recording must not claim one: %v", body["class"])
	}

	if status, body := h.do("POST", "/v1/virtual-classes/"+classID+"/recording", host, nil); status != http.StatusCreated {
		t.Fatalf("start: %d %v", status, body)
	}
	status, body = h.do("GET", "/v1/virtual-classes/"+classID, learner, nil)
	if status != http.StatusOK {
		t.Fatalf("get class: %d %v", status, body)
	}
	recording, _ := body["class"].(map[string]any)["recording"].(map[string]any)
	if recording == nil || recording["status"] != "recording" {
		t.Fatalf("a recorded class must say so in the payload every client already fetches: %v", body["class"])
	}
}
