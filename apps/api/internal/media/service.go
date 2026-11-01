// Package media is the live-class media server: a small selective-forwarding unit that lets a room hold far
// more people than a peer-to-peer mesh can.
//
// # Why this exists
//
// A mesh — every participant connected to every other — is the cheapest way to get encrypted video between
// two people, and it collapses at about six. Each participant's uplink must carry one stream per peer, so a
// 50-person room asks every phone to upload 49 streams. No amount of tuning fixes that; the fan-out has to move
// to something with a server's uplink.
//
// This package is that server. Each participant holds **one** peer connection to it. Publishers upload one
// stream; the forwarder copies the packets to every subscriber that wants them.
//
// # The honest change to the encryption story
//
// Media is DTLS-SRTP encrypted on both hops — participant → server, server → participant — but the server
// terminates it, so this is **hop-by-hop, not end-to-end**. That is the real cost of a 50-seat room, and it is
// stated rather than hidden: with a forwarding unit in the path, the machine can see the media, so "the server
// cannot record" stops being a mathematical fact and becomes a policy this code keeps (there is no recording
// path, no disk write and no egress). Whiteboard strokes and chat remain end-to-end encrypted and the media
// server never sees them.
//
// # Why the layout is fixed
//
// Negotiation happens once, when a participant joins, over a single request/response. Everything that could
// otherwise force a renegotiation mid-call — a speaker being promoted, somebody leaving — is expressed as a
// *slot*: the stage has a fixed number of slots, each with a permanently bound audio and video track. A slot is
// silent until somebody is publishing into it. Promotions and departures therefore change which publisher feeds
// a slot, never the shape of any connection, so no participant is ever renegotiated while speaking.
package media

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// StageSlots is how many people can publish into a room at once: the host plus five speakers.
//
// It is a product decision as much as a technical one. Six video streams is what a viewer's downlink can carry
// on a Nigerian mobile connection (roughly 2.5 Mbit/s at the 400 kbit/s per-stream ceiling this server asks for),
// and it is more faces than a tutoring session ever needs on stage. Everybody else is in the room, seen in the
// roster, able to raise a hand, and promoted into a slot when the host says so.
const StageSlots = 6

// MaxParticipants is the room size the server is built for.
const MaxParticipants = 50

// Frame requests (PLI) are sent this often so a viewer who joins mid-sentence gets a keyframe instead of a
// smear of partially-delta frames.
const pliInterval = 2 * time.Second

// ErrNotPublishing is returned when someone without a stage slot tries to publish.
var ErrNotPublishing = errors.New("only the host and speakers can publish")

// Session is one participant's connection to the forwarder.
type Session struct {
	ID     string
	UserID string
	RoomID string
	// Slot is the stage position this session publishes into, or -1 for a viewer.
	Slot int
	// Close tears the connection down. It is idempotent.
	Close func()
}

// room is the forwarder for one live class.
type room struct {
	id string

	mu       sync.RWMutex
	sessions map[string]*Session
	// slots[i] holds the tracks a publisher feeds. They exist for the life of the room.
	slots []*slot
	// occupants[i] is the session id currently publishing into slot i, or "".
	occupants []string
}

// slot is one stage position: a permanently bound audio and video track.
type slot struct {
	video *webrtc.TrackLocalStaticRTP
	audio *webrtc.TrackLocalStaticRTP
}

func newSlot(roomID string, index int) (*slot, error) {
	video, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000},
		fmt.Sprintf("video-%s-%d", roomID, index), "edumart-stage")
	if err != nil {
		return nil, err
	}
	audio, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		fmt.Sprintf("audio-%s-%d", roomID, index), "edumart-stage")
	if err != nil {
		return nil, err
	}
	return &slot{video: video, audio: audio}, nil
}

// Service owns the rooms and the shared WebRTC API.
type Service struct {
	api     *webrtc.API
	mu      sync.Mutex
	rooms   map[string]*room
	closing bool
}

// Config shapes the forwarder.
type Config struct {
	// UDPMin/UDPMax pin the port range the server offers as its ICE candidates. A deployment behind a firewall
	// that only forwards a handful of ports needs this; without it the OS picks ephemeral ports and half of
	// them are closed at the edge, which shows up as "video works for some people".
	UDPMin uint16
	UDPMax uint16
}

// New builds the forwarder.
func New(config Config) (*Service, error) {
	mediaEngine := &webrtc.MediaEngine{}
	// VP8 and Opus only, deliberately. The answer then negotiates exactly one codec per stream, which is what
	// makes a stable slot track possible without transcoding: the forwarder copies RTP packets, so it cannot
	// convert a stream it did not expect.
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeVP8, ClockRate: 90000, Channels: 0},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	settings := webrtc.SettingEngine{}
	if config.UDPMin != 0 && config.UDPMax >= config.UDPMin {
		if err := settings.SetEphemeralUDPPortRange(config.UDPMin, config.UDPMax); err != nil {
			return nil, err
		}
	}
	// A forwarding unit never needs to *receive* a muxed candidate from the public internet on a well-known
	// port; it gathers host and server-reflexive candidates like any WebRTC endpoint.
	settings.SetICETimeouts(10*time.Second, 5*time.Second, 2*time.Second)

	// No interceptor registry: the forwarder copies RTP and does not need NACK, TWCC or a jitter buffer. Asking
	// for them would add per-packet bookkeeping for fifty receivers to no purpose.
	return &Service{
		api:   webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithSettingEngine(settings)),
		rooms: map[string]*room{},
	}, nil
}

// EnsureRoom creates the forwarder state for a class, idempotently.
func (s *Service) EnsureRoom(roomID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return errors.New("media server is shutting down")
	}
	if _, ok := s.rooms[roomID]; ok {
		return nil
	}
	r := &room{id: roomID, sessions: map[string]*Session{}, occupants: make([]string, StageSlots)}
	for index := 0; index < StageSlots; index++ {
		built, err := newSlot(roomID, index)
		if err != nil {
			return err
		}
		r.slots = append(r.slots, built)
	}
	s.rooms[roomID] = r
	return nil
}

// SlotOf reports which stage slot a user currently occupies, or -1.
func (s *Service) SlotOf(roomID, userID string) int {
	s.mu.Lock()
	r := s.rooms[roomID]
	s.mu.Unlock()
	if r == nil {
		return -1
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for index, sessionID := range r.occupants {
		if session := r.sessions[sessionID]; session != nil && session.UserID == userID {
			return index
		}
	}
	return -1
}

// Offer performs the single negotiation a participant needs: it accepts an offer laid out for the caller's role
// and returns the answer, plus the slot the caller publishes into (-1 for a viewer).
//
// `publish` is a request, not an authorisation: the caller passes the slot the room assigned it, and the
// forwarder refuses a slot that is unassigned, already taken, or out of range. Authority over slots lives in the
// class service (`stage_role`); this package only enforces that one slot has one publisher.
func (s *Service) Offer(roomID, userID, sessionID string, slot int, publish bool, offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	if err := s.EnsureRoom(roomID); err != nil {
		return webrtc.SessionDescription{}, err
	}
	s.mu.Lock()
	r := s.rooms[roomID]
	s.mu.Unlock()

	r.mu.Lock()
	if publish {
		if slot < 0 || slot >= StageSlots {
			r.mu.Unlock()
			return webrtc.SessionDescription{}, fmt.Errorf("stage slot %d does not exist", slot)
		}
		if occupant := r.occupants[slot]; occupant != "" && occupant != sessionID {
			r.mu.Unlock()
			return webrtc.SessionDescription{}, fmt.Errorf("stage slot %d is already publishing", slot)
		}
		r.occupants[slot] = sessionID
	}
	// One connection per user: a reconnect (a flaky phone, a reload) must not leave a ghost publishing.
	for id, existing := range r.sessions {
		if existing.UserID == userID && id != sessionID {
			delete(r.sessions, id)
			existing.Close()
		}
	}
	r.mu.Unlock()

	pc, err := s.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	closed := false
	var closeMu sync.Mutex
	closeOnce := func() {
		closeMu.Lock()
		defer closeMu.Unlock()
		if closed {
			return
		}
		closed = true
		_ = pc.Close()
	}

	// The m-line order below mirrors exactly what the browser builds, because the answer has to line up with
	// the offer one for one. A client that sends a different shape fails here rather than producing a call that
	// quietly has no video.
	if publish {
		for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeAudio, webrtc.RTPCodecTypeVideo} {
			if _, err := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{
				Direction: webrtc.RTPTransceiverDirectionRecvonly,
			}); err != nil {
				closeOnce()
				return webrtc.SessionDescription{}, err
			}
		}
	}
	for index := 0; index < StageSlots; index++ {
		if _, err := pc.AddTransceiverFromTrack(r.slots[index].video, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionSendonly,
		}); err != nil {
			closeOnce()
			return webrtc.SessionDescription{}, err
		}
		if _, err := pc.AddTransceiverFromTrack(r.slots[index].audio, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionSendonly,
		}); err != nil {
			closeOnce()
			return webrtc.SessionDescription{}, err
		}
	}

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		if !publish {
			return
		}
		target := r.slots[slot].audio
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			target = r.slots[slot].video
		}
		// Ask for a keyframe immediately: publishers are usually already sending deltas by the time the first
		// viewer arrives.
		go requestKeyframes(pc, track)

		for {
			packet, _, readErr := track.ReadRTP()
			if readErr != nil {
				return
			}
			if writeErr := target.WriteRTP(packet); writeErr != nil {
				return
			}
		}
	})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			closeOnce()
		}
	})

	session := &Session{
		ID: sessionID, UserID: userID, RoomID: roomID, Slot: -1, Close: closeOnce,
	}
	if publish {
		session.Slot = slot
	}

	r.mu.Lock()
	r.sessions[sessionID] = session
	r.mu.Unlock()

	if err := pc.SetRemoteDescription(offer); err != nil {
		s.remove(roomID, sessionID)
		return webrtc.SessionDescription{}, err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		s.remove(roomID, sessionID)
		return webrtc.SessionDescription{}, err
	}
	// Non-trickle: gathering completes before the answer is returned, so a browser needs exactly one round trip
	// and no candidate exchange endpoint.
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		s.remove(roomID, sessionID)
		return webrtc.SessionDescription{}, err
	}
	select {
	case <-gatherComplete:
	case <-time.After(8 * time.Second):
		// A slow gather is not fatal: candidates already gathered are in the SDP, and server-reflexive ones
		// arrive within this window in practice.
	}

	return *pc.LocalDescription(), nil
}

// requestKeyframes keeps asking a publisher for a fresh keyframe, so anybody who joins mid-session gets a
// picture instead of waiting for the publisher's own keyframe interval.
func requestKeyframes(pc *webrtc.PeerConnection, track *webrtc.TrackRemote) {
	ticker := time.NewTicker(pliInterval)
	defer ticker.Stop()
	for range ticker.C {
		if pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
			if pc.ConnectionState() == webrtc.PeerConnectionStateClosed ||
				pc.ConnectionState() == webrtc.PeerConnectionStateFailed {
				return
			}
			continue
		}
		if err := pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(track.SSRC())}}); err != nil {
			return
		}
	}
}

// LeaveUser closes whatever connection a user currently holds in a room, wherever it lives.
//
// The browser does not reliably tell the server it hung up — a closed laptop, a tunnel, a force-quit — so a
// role change has to be able to stop a stream from the server side. Without this, demoting a speaker leaves
// their camera being forwarded until the connection times out.
func (s *Service) LeaveUser(roomID, userID string) {
	s.mu.Lock()
	r := s.rooms[roomID]
	s.mu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	ids := []string{}
	for id, session := range r.sessions {
		if session.UserID == userID {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	for _, id := range ids {
		s.remove(roomID, id)
	}
}

// Leave removes a participant and frees any stage slot it held.
func (s *Service) Leave(roomID, sessionID string) {
	s.remove(roomID, sessionID)
}

// remove is the single place a session disappears, so a slot can never be left occupied by a dead connection.
func (s *Service) remove(roomID, sessionID string) {
	s.mu.Lock()
	r := s.rooms[roomID]
	s.mu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	session := r.sessions[sessionID]
	delete(r.sessions, sessionID)
	for index, occupant := range r.occupants {
		if occupant == sessionID {
			r.occupants[index] = ""
		}
	}
	r.mu.Unlock()
	if session != nil {
		session.Close()
	}
}

// Participants reports how many connections a room currently holds. It exists for metrics and tests, not for
// routing.
func (s *Service) Participants(roomID string) int {
	s.mu.Lock()
	r := s.rooms[roomID]
	s.mu.Unlock()
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

// CloseRoom tears down every connection in a class, which is what "end class" has to mean.
func (s *Service) CloseRoom(roomID string) {
	s.mu.Lock()
	r := s.rooms[roomID]
	delete(s.rooms, roomID)
	s.mu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	sessions := make([]*Session, 0, len(r.sessions))
	for _, session := range r.sessions {
		sessions = append(sessions, session)
	}
	r.sessions = map[string]*Session{}
	for index := range r.occupants {
		r.occupants[index] = ""
	}
	r.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
}

// Close shuts the forwarder down: every room, every connection.
func (s *Service) Close() {
	s.mu.Lock()
	s.closing = true
	rooms := make([]string, 0, len(s.rooms))
	for id := range s.rooms {
		rooms = append(rooms, id)
	}
	s.mu.Unlock()
	for _, id := range rooms {
		s.CloseRoom(id)
	}
}
