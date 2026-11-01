// Package virtualclass owns live classes: the room, who paid what to be in it, the whiteboard, and the stage.
//
// What this package deliberately does not do:
//
//   - **read the whiteboard.** Board events are opaque ciphertext with a client-held room key that travels in
//     the join link's URL fragment — a part of a URL no browser sends to a server. The API stores and orders
//     strokes so a late joiner can replay the board, and cannot decrypt one.
//   - **decide who is on stage.** It records the host's decision (`stage_role`, `stage_slot`); the media
//     forwarder in internal/media only enforces that one slot has one publisher.
//   - **record the session.** There is no recording, no transcription and no server-side mixing. A recording is
//     a custody decision about other people's voices, and it should not arrive as a side effect of a feature.
//
// Media is forwarded rather than meshed. A mesh is cheap for two people and impossible for fifty: each
// participant would upload one stream per peer. The forwarder in internal/media gives every participant one
// connection to one server that copies packets to the people who want them, so the room holds 50 seats.
package virtualclass

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pion/webrtc/v4"
	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/media"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Class is a live session a host has scheduled.
type Class struct {
	ID          string  `json:"id"`
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	SubjectID   *string `json:"subjectId"`
	// CreditsCost is the price in learning credits. 0 means free.
	CreditsCost     int    `json:"creditsCost"`
	Capacity        int    `json:"capacity"`
	Status          string `json:"status"`
	StartsAt        string `json:"startsAt"`
	DurationMinutes int    `json:"durationMinutes"`
	HostUserID      string `json:"hostUserId"`
	HostName        string `json:"hostName"`
	HostAvatar      string `json:"hostAvatar"`
	Attendees       int    `json:"attendees"`
	// JoinURL is the unique link a host sends to learners. Stable per class.
	JoinURL string `json:"joinUrl"`
	// Role is the caller's relationship to the class: host, attendee, or none.
	Role      string  `json:"role"`
	CreatedAt string  `json:"createdAt"`
	EndedAt   *string `json:"endedAt"`
}

// Attendee is a roster row, including the avatar the room renders.
type Attendee struct {
	UserID      string  `json:"userId"`
	DisplayName string  `json:"displayName"`
	Avatar      Avatar  `json:"avatar"`
	Role        string  `json:"role"`
	Payment     string  `json:"paymentMethod"`
	CreditsPaid int     `json:"creditsPaid"`
	JoinedAt    string  `json:"joinedAt"`
	LeftAt      *string `json:"leftAt"`
	// Present is true when the attendee has been seen within the presence window. A browser that crashes
	// never sends a leave, so presence is a heartbeat, not a flag.
	Present bool `json:"present"`
	// StageRole is "host", "speaker" or "audience". Only host and speaker publish audio and video; everybody
	// else is in the room, watching the stage and raising a hand.
	StageRole string `json:"stageRole"`
	// StageSlot is the stage position the attendee publishes into, or -1. It is server-assigned: a client
	// asking for slot 3 is asking.
	StageSlot int `json:"stageSlot"`
}

// Avatar is what the room draws for someone who has no photograph: initials and a stable colour, both derived
// from the user id so two clients render the same person identically.
type Avatar struct {
	Initials string `json:"initials"`
	Color    string `json:"color"`
}

// BoardEvent is one ciphertext stroke (or eraser pass, or shape) on the whiteboard.
type BoardEvent struct {
	Seq        int    `json:"seq"`
	AuthorID   string `json:"authorId"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	CreatedAt  string `json:"createdAt"`
}

// Signal is a WebRTC signalling message. The API cannot read the payload beyond its type, and does not try.
type Signal struct {
	Seq        int     `json:"seq"`
	FromUserID string  `json:"fromUserId"`
	ToUserID   *string `json:"toUserId"`
	Kind       string  `json:"kind"`
	Payload    string  `json:"payload"`
	CreatedAt  string  `json:"createdAt"`
}

// Attendance is the host's session-tracking view.
type Attendance struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Sessions    int    `json:"sessions"`
	Minutes     int    `json:"minutes"`
	FirstJoin   string `json:"firstJoinedAt"`
	LastSeen    string `json:"lastSeenAt"`
	CreditsPaid int    `json:"creditsPaid"`
}

// DiscountCode lets a host hand out cheaper or free entry to one class.
type DiscountCode struct {
	Code       string  `json:"code"`
	PercentOff int     `json:"percentOff"`
	MaxUses    int     `json:"maxUses"`
	Uses       int     `json:"uses"`
	ExpiresAt  *string `json:"expiresAt"`
	CreatedAt  string  `json:"createdAt"`
}

// Config is the deployment-shaped part of the service.
type Config struct {
	// WebURL is the origin a join link points at. Links are sent to other people, so they must be absolute.
	WebURL string
	// ICEServers is the JSON list handed to browsers so they can negotiate NAT traversal. The room still needs
	// a TURN relay for participants behind symmetric NAT — see docs/VIRTUAL_CLASSES.md.
	ICEServers []map[string]any
	// Media is the forwarding unit that carries the audio and video. It is optional so that a test can build
	// the service without one and get a clear refusal instead of a nil panic.
	Media *media.Service
}

// Service implements the use cases.
type Service struct {
	db     *store.Store
	config Config
	now    func() time.Time
}

// WithMedia attaches the forwarding unit. It is separate from New so that the media server can be built by the
// process that also owns the API, in one place, and closed on shutdown.
func (s *Service) WithMedia(forwarder *media.Service) *Service {
	s.config.Media = forwarder
	return s
}

// New builds the service.
func New(db *store.Store, config Config) *Service {
	return &Service{db: db, config: config, now: func() time.Time { return time.Now().UTC() }}
}

const (
	StatusScheduled = "scheduled"
	StatusLive      = "live"
	StatusEnded     = "ended"
	StatusCancelled = "cancelled"
)

const (
	// MaxCapacity is how many people one class holds. It is a hard ceiling, not a suggestion: the forwarder is
	// built and load-tested for fifty connections, and a room that admits the fifty-first would fail in the
	// middle of a lesson rather than at the door.
	MaxCapacity = 50
	// FreeSessionMinutes is the length of a free class. A free room is the on-ramp; an all-day free room is a
	// way to run a paid service without paying for it, and it also means a host can leave a room "open"
	// forever and let attendance decay into noise. Paid classes set their own length.
	FreeSessionMinutes = 45
)

// presenceWindow is how recently an attendee must have been seen to count as in the room.
const presenceWindow = 75 * time.Second

// signalRetention is how long a signalling message is kept. SDP and ICE candidates are useless within seconds,
// and they contain the peers' IP addresses.
const signalRetention = 10 * time.Minute

// slugAlphabet excludes characters that are read aloud badly. A join link is copied and pasted far more often
// than typed, but the fallback is a person reading it out.
const slugAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newSlug(length int) (string, error) {
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, length)
	for index, value := range raw {
		out[index] = slugAlphabet[int(value)%len(slugAlphabet)]
	}
	return string(out), nil
}

// ── creating and reading classes ────────────────────────────────────────────────────────────────────

// CreateInput is what a host submits.
type CreateInput struct {
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	SubjectID       *string `json:"subjectId"`
	CreditsCost     int     `json:"creditsCost"`
	Capacity        int     `json:"capacity"`
	StartsAt        string  `json:"startsAt"`
	DurationMinutes int     `json:"durationMinutes"`
}

// Create schedules a class. Any signed-in account can host — a student running a revision session is as valid
// as an institution running a lecture — so there is no role check here. The controls that do matter are
// economic (a host cannot award themselves credits) and per-class (only the host starts, ends and prices it).
func (s *Service) Create(ctx context.Context, hostID string, in CreateInput) (*Class, error) {
	fields := []httpx.FieldError{}
	title := strings.TrimSpace(in.Title)
	if len(title) < 3 || len(title) > 120 {
		fields = append(fields, httpx.FieldError{Field: "title", Message: "Give the class a title (3–120 characters)."})
	}
	if in.CreditsCost < 0 || in.CreditsCost > 10000 {
		fields = append(fields, httpx.FieldError{Field: "creditsCost", Message: "A price is between 0 and 10000 credits."})
	}
	if in.Capacity == 0 {
		in.Capacity = MaxCapacity
	}
	if in.Capacity < 2 || in.Capacity > MaxCapacity {
		fields = append(fields, httpx.FieldError{Field: "capacity",
			Message: fmt.Sprintf("A room holds between 2 and %d people.", MaxCapacity)})
	}
	if in.DurationMinutes == 0 {
		// The default follows the price: an hour is the usual lecture, and a free session cannot be an hour.
		if in.CreditsCost == 0 {
			in.DurationMinutes = FreeSessionMinutes
		} else {
			in.DurationMinutes = 60
		}
	}
	if in.DurationMinutes < 5 || in.DurationMinutes > 480 {
		fields = append(fields, httpx.FieldError{Field: "durationMinutes", Message: "A class lasts between 5 and 480 minutes."})
	}
	// The free limit is enforced here, in the one place a class is created, and again at the door (a session
	// that outlives its booking is closed by requireOpen). A rule that lives only in a form is a rule a script
	// does not follow.
	if in.CreditsCost == 0 && in.DurationMinutes > FreeSessionMinutes {
		fields = append(fields, httpx.FieldError{Field: "durationMinutes",
			Message: fmt.Sprintf("A free class runs for at most %d minutes. Set a credit price to run longer.", FreeSessionMinutes)})
	}
	starts, err := time.Parse(time.RFC3339, strings.TrimSpace(in.StartsAt))
	if err != nil {
		fields = append(fields, httpx.FieldError{Field: "startsAt", Message: "Give a start time in RFC 3339."})
	}
	if len(fields) > 0 {
		return nil, httpx.Validation(fields)
	}

	id := store.NewID()
	var slug string
	for attempt := 0; attempt < 6; attempt++ {
		candidate, genErr := newSlug(16)
		if genErr != nil {
			return nil, genErr
		}
		slug = candidate
		_, err := s.db.DB.ExecContext(ctx, `
			INSERT INTO virtual_classes
			  (id, host_user_id, slug, title, description, subject_id, credits_cost, capacity, status,
			   starts_at, duration_minutes, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, hostID, slug, title, strings.TrimSpace(in.Description), in.SubjectID, in.CreditsCost,
			in.Capacity, StatusScheduled, store.TS(starts), in.DurationMinutes, store.Now(), store.Now())
		if err == nil {
			if _, counterErr := s.db.DB.ExecContext(ctx,
				`INSERT INTO virtual_class_counters (class_id, board_seq, signal_seq) VALUES (?,0,0)
				 ON CONFLICT (class_id) DO NOTHING`, id); counterErr != nil {
				return nil, counterErr
			}
			return s.Get(ctx, id, hostID)
		}
		// A slug collision is astronomically unlikely (32^16) and a unique violation could also be the id; retry
		// once per attempt rather than surfacing a 23505 to the host.
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not allocate a join code for the class")
}

const classColumns = `
	c.id, c.slug, c.title, c.description, c.subject_id, c.credits_cost, c.capacity, c.status,
	c.starts_at, c.duration_minutes, c.host_user_id,
	COALESCE(u.display_name, 'A host'), c.created_at, c.ended_at,
	(SELECT COUNT(*) FROM virtual_class_attendees a WHERE a.class_id = c.id)`

func (s *Service) scanClass(scan func(...any) error) (*Class, error) {
	var class Class
	var subjectID sql.NullString
	var endedAt sql.NullString
	if err := scan(&class.ID, &class.Slug, &class.Title, &class.Description, &subjectID, &class.CreditsCost,
		&class.Capacity, &class.Status, &class.StartsAt, &class.DurationMinutes, &class.HostUserID,
		&class.HostName, &class.CreatedAt, &endedAt, &class.Attendees); err != nil {
		return nil, err
	}
	if subjectID.Valid {
		class.SubjectID = &subjectID.String
	}
	if endedAt.Valid && endedAt.String != "" {
		class.EndedAt = &endedAt.String
	}
	class.HostAvatar = avatarFor(class.HostUserID, class.HostName).Initials
	class.JoinURL = s.joinURL(class.Slug)
	return &class, nil
}

// Get returns one class, with the caller's role in it.
func (s *Service) Get(ctx context.Context, idOrSlug, viewerID string) (*Class, error) {
	row := s.db.DB.QueryRowContext(ctx, `SELECT `+classColumns+`
		  FROM virtual_classes c LEFT JOIN users u ON u.id = c.host_user_id
		 WHERE c.id = ? OR c.slug = ?`, idOrSlug, strings.ToUpper(idOrSlug))
	class, err := s.scanClass(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That class does not exist.")
	}
	if err != nil {
		return nil, err
	}
	class.Role = s.roleOf(ctx, class.ID, viewerID)
	return class, nil
}

func (s *Service) roleOf(ctx context.Context, classID, userID string) string {
	if userID == "" {
		return ""
	}
	var hostID string
	if err := s.db.DB.QueryRowContext(ctx, `SELECT host_user_id FROM virtual_classes WHERE id = ?`,
		classID).Scan(&hostID); err != nil {
		return ""
	}
	if hostID == userID {
		return "host"
	}
	var role string
	err := s.db.DB.QueryRowContext(ctx, `SELECT role FROM virtual_class_attendees
		WHERE class_id = ? AND user_id = ? AND left_at IS NULL`, classID, userID).Scan(&role)
	if err == nil {
		return role
	}
	return ""
}

// List returns classes the caller can see: their own, and the upcoming public catalogue.
//
// There is no "private" flag yet — a class is discoverable by everyone once scheduled, and the invite link is
// what gets shared. Hiding a class is a moderation feature and is deliberately not guessed at here.
func (s *Service) List(ctx context.Context, viewerID, scope string) ([]Class, error) {
	where := " c.status IN ('scheduled','live') "
	args := []any{}
	switch scope {
	case "mine":
		where = " (c.host_user_id = ? OR EXISTS (SELECT 1 FROM virtual_class_attendees a WHERE a.class_id = c.id AND a.user_id = ?)) "
		args = append(args, viewerID, viewerID)
	case "past":
		where = " (c.status = 'ended' AND (c.host_user_id = ? OR EXISTS (SELECT 1 FROM virtual_class_attendees a WHERE a.class_id = c.id AND a.user_id = ?))) "
		args = append(args, viewerID, viewerID)
	}
	// The caller's role is part of the same row rather than a second query per class. On SQLite the pool holds a
	// single connection, so querying inside an open result set deadlocks the whole process — a list endpoint
	// that looks fine against PostgreSQL would take the API down in development.
	rows, err := s.db.DB.QueryContext(ctx, `SELECT `+classColumns+`,
		CASE WHEN c.host_user_id = ? THEN 'host'
		     ELSE COALESCE((SELECT a.role FROM virtual_class_attendees a
		                     WHERE a.class_id = c.id AND a.user_id = ? AND a.left_at IS NULL), '') END
		  FROM virtual_classes c LEFT JOIN users u ON u.id = c.host_user_id
		 WHERE `+where+`
		 ORDER BY c.starts_at ASC LIMIT 100`, append([]any{viewerID, viewerID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Class{}
	for rows.Next() {
		var role string
		class, scanErr := s.scanClass(func(dest ...any) error { return rows.Scan(append(dest, &role)...) })
		if scanErr != nil {
			return nil, scanErr
		}
		class.Role = role
		out = append(out, *class)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) joinURL(slug string) string {
	base := strings.TrimRight(strings.TrimSpace(s.config.WebURL), "/")
	if base == "" {
		return "/live/" + slug
	}
	return base + "/live/" + slug
}

// ── joining ─────────────────────────────────────────────────────────────────────────────────────────

// JoinInput carries how the learner is paying.
type JoinInput struct {
	// DiscountCode is optional. It applies to the credit price of the class.
	DiscountCode string `json:"discountCode"`
}

// JoinResult describes what the join cost, and what the caller holds now.
type JoinResult struct {
	Class           *Class    `json:"class"`
	Attendee        *Attendee `json:"attendee"`
	CreditsSpent    int       `json:"creditsSpent"`
	DiscountApplied int       `json:"discountApplied"`
	AlreadyJoined   bool      `json:"alreadyJoined"`
}

// Join seats a learner, charging credits only when the class is not free and no full discount applies.
//
// Everything that can go wrong here is economic or a race, so it happens in one transaction that re-reads the
// price, the capacity and the discount inside the lock rather than trusting anything the caller sent:
//
//   - two people joining the last seat: capacity is checked after the class row is locked, so exactly one wins;
//   - a discount code used past its limit: the counter is incremented with the limit in the WHERE clause;
//   - a double-submit: the attendee row is unique per (class, user) and the ledger entry is idempotent, so a
//     retried request charges nothing the second time.
func (s *Service) Join(ctx context.Context, classID, userID string, in JoinInput) (*JoinResult, error) {
	if err := s.requireOpen(ctx, classID); err != nil {
		return nil, err
	}
	var result JoinResult
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		// Lock the class row: on PostgreSQL this serialises joins for one class; SQLite serialises writers
		// anyway. Without it, a capacity check is a suggestion.
		var (
			hostID      string
			status      string
			creditsCost int
			capacity    int
		)
		if err := tx.QueryRowContext(ctx, `SELECT host_user_id, status, credits_cost, capacity
			FROM virtual_classes WHERE id = ?`+lockSuffix(s.db.Driver), classID).
			Scan(&hostID, &status, &creditsCost, &capacity); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return httpx.NewError(httpx.CodeNotFound, "That class does not exist.")
			}
			return err
		}
		if status == StatusEnded || status == StatusCancelled {
			return httpx.NewError(httpx.CodeConflict, "That class has already finished.")
		}

		// Idempotent: a learner who is already seated pays nothing and gets the same answer.
		var existingID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM virtual_class_attendees
			WHERE class_id = ? AND user_id = ?`, classID, userID).Scan(&existingID)
		if err == nil {
			result.AlreadyJoined = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		// A room never admits more people than the forwarder can carry, however the row was written. 0 used to
		// mean "unlimited"; it now means "the fifty the room is built for".
		if capacity <= 0 || capacity > MaxCapacity {
			capacity = MaxCapacity
		}
		if capacity > 0 {
			var seated int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM virtual_class_attendees WHERE class_id = ?`, classID).Scan(&seated); err != nil {
				return err
			}
			if seated >= capacity {
				return httpx.NewError(httpx.CodeConflict, "This class is full.")
			}
		}

		price := creditsCost
		method := "free"
		paid := 0
		if price > 0 {
			method = "credits"
		}
		if hostID == userID {
			// The host never pays to enter their own room.
			price = 0
			method = "host"
		}
		if code := strings.ToUpper(strings.TrimSpace(in.DiscountCode)); code != "" && price > 0 {
			var (
				codeID  string
				percent int
				maxUses int
				uses    int
				expires sql.NullString
			)
			if err := tx.QueryRowContext(ctx, `SELECT id, percent_off, max_uses, uses, expires_at
				FROM virtual_class_discount_codes WHERE class_id = ? AND code = ?`, classID, code).
				Scan(&codeID, &percent, &maxUses, &uses, &expires); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return httpx.NewError(httpx.CodeConflict, "That discount code is not valid for this class.")
				}
				return err
			}
			if expires.Valid && expires.String != "" {
				if expiry, parseErr := store.ParseTS(expires.String); parseErr == nil && s.now().After(expiry) {
					return httpx.NewError(httpx.CodeConflict, "That discount code has expired.")
				}
			}
			if maxUses > 0 && uses >= maxUses {
				return httpx.NewError(httpx.CodeConflict, "That discount code has been used up.")
			}
			// The counter is incremented with the limit in the WHERE clause, so two simultaneous uses of the
			// last one cannot both succeed.
			res, updateErr := tx.ExecContext(ctx, `UPDATE virtual_class_discount_codes
				SET uses = uses + 1 WHERE id = ? AND (max_uses = 0 OR uses < max_uses)`, codeID)
			if updateErr != nil {
				return updateErr
			}
			affected, _ := res.RowsAffected()
			if affected == 0 {
				return httpx.NewError(httpx.CodeConflict, "That discount code has been used up.")
			}
			paid = price * (100 - percent) / 100
			if paid < 0 {
				paid = 0
			}
			method = "discount"
			result.DiscountApplied = percent
		} else if price > 0 {
			paid = price
		}

		if paid > 0 {
			var balance int
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount),0) FROM points_ledger_entries
				WHERE user_id = ?`, userID).Scan(&balance); err != nil {
				return err
			}
			if balance < paid {
				return httpx.NewError(httpx.CodeConflict,
					fmt.Sprintf("You need %d more credits to join this class.", paid-balance))
			}
			// The ledger is append-only: spending is a negative row, never a mutated balance. The idempotency
			// key includes the class, so a retried join cannot charge twice even if the attendee insert is the
			// part that failed.
			if _, err := tx.ExecContext(ctx, `INSERT INTO points_ledger_entries
				(id, user_id, amount, entry_type, source_type, source_id, rule_version, idempotency_key, note, created_at)
				VALUES (?,?,?,'spend','virtual_class',?,'v1',?,'Joined a live class',?)
				ON CONFLICT (idempotency_key) DO NOTHING`,
				store.NewID(), userID, -paid, classID, "credits:virtual-class:"+classID+":"+userID, store.Now()); err != nil {
				return err
			}
		}

		var sessionID sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT id FROM virtual_class_sessions
			WHERE class_id = ? AND ended_at IS NULL ORDER BY started_at DESC LIMIT 1`, classID).
			Scan(&sessionID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		role := "attendee"
		stageRole := "audience"
		stageSlot := -1
		if hostID == userID {
			// The host is always on stage, in slot 0. It is reserved rather than allocated, so the host can
			// rejoin a room they were dropped from and still be the person talking.
			role = "host"
			stageRole = "host"
			stageSlot = 0
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO virtual_class_attendees
			(id, class_id, user_id, role, session_id, payment_method, credits_paid, discount_code,
			 joined_at, last_seen_at, stage_role, stage_slot)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT (class_id, user_id) DO NOTHING`,
			store.NewID(), classID, userID, role, sessionID, method, paid,
			nullIfEmpty(strings.ToUpper(strings.TrimSpace(in.DiscountCode))),
			store.Now(), store.Now(), stageRole, stageSlot); err != nil {
			return err
		}
		result.CreditsSpent = paid
		return nil
	})
	if err != nil {
		return nil, err
	}
	class, err := s.Get(ctx, classID, userID)
	if err != nil {
		return nil, err
	}
	result.Class = class
	attendee, err := s.attendee(ctx, classID, userID)
	if err != nil {
		return nil, err
	}
	result.Attendee = attendee
	return &result, nil
}

// nextSeq allocates the next value of a per-class counter inside a transaction.
//
// PostgreSQL can hand the new value back from the UPDATE itself, which is one round trip and one lock. SQLite has
// no RETURNING in the driver path used here, so it increments and then reads in the same transaction — safe
// because SQLite serialises writers, and the row is locked for the rest of the transaction either way.
//
// The counter is a column on one row rather than a table of events because both clients need a dense, gap-free
// order: a whiteboard that replayed 5, 6, 9 would be a board with holes in it.
func nextSeq(ctx context.Context, tx *sql.Tx, driver, classID, column string) (int, error) {
	if driver == "postgres" || driver == "pgx" {
		var seq int
		err := tx.QueryRowContext(ctx, `UPDATE virtual_class_counters SET `+column+` = `+column+` + 1
			WHERE class_id = ? RETURNING `+column, classID).Scan(&seq)
		return seq, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE virtual_class_counters SET `+column+` = `+column+` + 1
		WHERE class_id = ?`, classID); err != nil {
		return 0, err
	}
	var seq int
	if err := tx.QueryRowContext(ctx, `SELECT `+column+` FROM virtual_class_counters WHERE class_id = ?`,
		classID).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, nil
}

// lockSuffix is " FOR UPDATE" on PostgreSQL and nothing on SQLite, which has no row locks.
func lockSuffix(driver string) string {
	if driver == "postgres" || driver == "pgx" {
		return " FOR UPDATE"
	}
	return ""
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// Leave records that someone left. It is advisory: a browser that crashes never calls it, which is why presence
// also depends on a heartbeat.
func (s *Service) Leave(ctx context.Context, classID, userID string) error {
	_, err := s.db.DB.ExecContext(ctx, `UPDATE virtual_class_attendees SET left_at = ?, last_seen_at = ?
		WHERE class_id = ? AND user_id = ? AND left_at IS NULL`, store.Now(), store.Now(), classID, userID)
	return err
}

// ── the room ────────────────────────────────────────────────────────────────────────────────────────

// Start opens the room. Only the host can, and starting an already-live class is a no-op rather than an error:
// a host reconnecting will call it again.
func (s *Service) Start(ctx context.Context, classID, userID string) (*Class, error) {
	if err := s.requireHost(ctx, classID, userID); err != nil {
		return nil, err
	}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM virtual_classes WHERE id = ?`+lockSuffix(s.db.Driver),
			classID).Scan(&status); err != nil {
			return err
		}
		if status == StatusEnded {
			return httpx.NewError(httpx.CodeConflict, "That class has already ended.")
		}
		if status == StatusLive {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE virtual_classes SET status = ?, updated_at = ? WHERE id = ?`,
			StatusLive, store.Now(), classID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO virtual_class_sessions (id, class_id, started_by, started_at)
			VALUES (?,?,?,?)`, store.NewID(), classID, userID, store.Now())
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, classID, userID)
}

// End closes the room and every open attendance row with it, so nobody is left "present" in a finished class.
func (s *Service) End(ctx context.Context, classID, userID string) (*Class, error) {
	if err := s.requireHost(ctx, classID, userID); err != nil {
		return nil, err
	}
	now := store.Now()
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE virtual_classes SET status = ?, ended_at = ?, updated_at = ?
			WHERE id = ?`, StatusEnded, now, now, classID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE virtual_class_sessions SET ended_at = ?
			WHERE class_id = ? AND ended_at IS NULL`, now, classID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE virtual_class_attendees SET left_at = ?
			WHERE class_id = ? AND left_at IS NULL`, now, classID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, classID, userID)
}

// requireOpen refuses a room whose free window has run out, and closes it while it is at it.
//
// The rule is "a free session is 45 minutes". Enforcing it only when the class is created would leave the door
// open for a host who started a free class and simply never ended it, which is exactly the shape the rule
// exists to stop. So the check happens at the door — joining, publishing, drawing — and it *closes* the class
// rather than refusing one caller: seats and stage slots should not stay open past the booking.
func (s *Service) requireOpen(ctx context.Context, classID string) error {
	var cost int
	var status string
	err := s.db.DB.QueryRowContext(ctx, `SELECT credits_cost, status FROM virtual_classes WHERE id = ?`,
		classID).Scan(&cost, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.NewError(httpx.CodeNotFound, "That class does not exist.")
	}
	if err != nil {
		return err
	}
	if status == StatusEnded || status == StatusCancelled {
		return httpx.NewError(httpx.CodeConflict, "That class has already finished.")
	}
	if cost > 0 {
		// A paid class runs for the length its host booked. That length was on the listing, so nobody can be
		// surprised by it.
		return nil
	}
	var startedAt sql.NullString
	if err := s.db.DB.QueryRowContext(ctx, `SELECT started_at FROM virtual_class_sessions
		WHERE class_id = ? AND ended_at IS NULL ORDER BY started_at DESC LIMIT 1`, classID).
		Scan(&startedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if !startedAt.Valid || startedAt.String == "" {
		return nil
	}
	start, err := store.ParseTS(startedAt.String)
	if err != nil {
		return nil
	}
	if s.now().Before(start.Add(FreeSessionMinutes * time.Minute)) {
		return nil
	}

	now := store.Now()
	if _, err := s.db.DB.ExecContext(ctx, `UPDATE virtual_classes SET status = ?, ended_at = ?, updated_at = ?
		WHERE id = ? AND status <> ?`, StatusEnded, now, now, classID, StatusEnded); err != nil {
		return err
	}
	if _, err := s.db.DB.ExecContext(ctx, `UPDATE virtual_class_sessions SET ended_at = ?
		WHERE class_id = ? AND ended_at IS NULL`, now, classID); err != nil {
		return err
	}
	if _, err := s.db.DB.ExecContext(ctx, `UPDATE virtual_class_attendees SET left_at = ?
		WHERE class_id = ? AND left_at IS NULL`, now, classID); err != nil {
		return err
	}
	if s.config.Media != nil {
		s.config.Media.CloseRoom(classID)
	}
	return httpx.NewError(httpx.CodeConflict,
		fmt.Sprintf("A free class closes %d minutes after it starts. Start a new one, or price it next time.",
			FreeSessionMinutes))
}

func (s *Service) requireHost(ctx context.Context, classID, userID string) error {
	var hostID string
	err := s.db.DB.QueryRowContext(ctx, `SELECT host_user_id FROM virtual_classes WHERE id = ?`,
		classID).Scan(&hostID)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.NewError(httpx.CodeNotFound, "That class does not exist.")
	}
	if err != nil {
		return err
	}
	if hostID != userID {
		return httpx.NewError(httpx.CodeForbidden, "Only the host can do that.")
	}
	return nil
}

// requireMember lets the host and anyone seated in the room through. It is the check on every room-internal
// read: the board, the roster and the signalling channel are not public just because the class is.
func (s *Service) requireMember(ctx context.Context, classID, userID string) error {
	role := s.roleOf(ctx, classID, userID)
	if role == "" {
		return httpx.NewError(httpx.CodeForbidden, "Join the class to take part in it.")
	}
	return nil
}

// Roster lists everyone in the class with their avatar and presence.
func (s *Service) Roster(ctx context.Context, classID, userID string) ([]Attendee, error) {
	if err := s.requireMember(ctx, classID, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT a.user_id, COALESCE(u.display_name, 'Learner'), a.role, a.payment_method, a.credits_paid,
		       a.joined_at, a.left_at, a.last_seen_at, a.stage_role, a.stage_slot
		  FROM virtual_class_attendees a LEFT JOIN users u ON u.id = a.user_id
		 WHERE a.class_id = ?
		 ORDER BY CASE a.role WHEN 'host' THEN 0 ELSE 1 END, a.joined_at ASC`, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Attendee{}
	for rows.Next() {
		var attendee Attendee
		var leftAt sql.NullString
		var lastSeen string
		if err := rows.Scan(&attendee.UserID, &attendee.DisplayName, &attendee.Role, &attendee.Payment,
			&attendee.CreditsPaid, &attendee.JoinedAt, &leftAt, &lastSeen, &attendee.StageRole,
			&attendee.StageSlot); err != nil {
			return nil, err
		}
		if leftAt.Valid && leftAt.String != "" {
			attendee.LeftAt = &leftAt.String
		}
		attendee.Avatar = avatarFor(attendee.UserID, attendee.DisplayName)
		attendee.Present = attendee.LeftAt == nil && s.seenRecently(lastSeen)
		out = append(out, attendee)
	}
	return out, rows.Err()
}

func (s *Service) seenRecently(at string) bool {
	seen, err := store.ParseTS(at)
	if err != nil {
		return false
	}
	return s.now().Sub(seen) <= presenceWindow
}

func (s *Service) attendee(ctx context.Context, classID, userID string) (*Attendee, error) {
	roster, err := s.Roster(ctx, classID, userID)
	if err != nil {
		return nil, err
	}
	for index := range roster {
		if roster[index].UserID == userID {
			return &roster[index], nil
		}
	}
	return nil, nil
}

// avatarFor derives initials and a colour from the identity, so every client draws the same person the same
// way without a photo upload and without inventing a gravatar-style identifier.
func avatarFor(userID, displayName string) Avatar {
	parts := strings.Fields(displayName)
	initials := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		initials += strings.ToUpper(string([]rune(part)[0]))
		if len([]rune(initials)) >= 2 {
			break
		}
	}
	if initials == "" {
		initials = "ED"
	}
	colors := []string{"#0F766E", "#7C3AED", "#B45309", "#BE123C", "#1D4ED8", "#047857", "#4B5563"}
	sum := 0
	for _, char := range userID {
		sum = (sum + int(char)) % 9973
	}
	return Avatar{Initials: initials, Color: colors[sum%len(colors)]}
}

// ICEConfig is what a browser needs to negotiate media. It contains no secrets: TURN credentials, when there
// are any, are short-lived and issued by the TURN provider.
func (s *Service) ICEConfig(ctx context.Context, classID, userID string) ([]map[string]any, error) {
	if err := s.requireMember(ctx, classID, userID); err != nil {
		return nil, err
	}
	if len(s.config.ICEServers) == 0 {
		return []map[string]any{{"urls": []string{"stun:stun.l.google.com:19302"}}}, nil
	}
	return s.config.ICEServers, nil
}

// ── media and the stage ─────────────────────────────────────────────────────────────────────────────

// MediaAnswer is what a browser needs to start publishing: the forwarder's answer, and the stage slot it owns
// (or -1 when it is only watching).
type MediaAnswer struct {
	SDP       string `json:"sdp"`
	StageRole string `json:"stageRole"`
	StageSlot int    `json:"stageSlot"`
}

// ConnectMedia performs the single negotiation a participant needs.
//
// One request, one response, no renegotiation later: the answer has the same shape whether the caller is
// publishing or watching, and a promotion takes effect the next time they connect. That is the trade that makes
// a fifty-seat room possible — a connection whose shape can change mid-call has to be renegotiated for every
// speaker swap, and every renegotiation is a chance for somebody's video to stop.
func (s *Service) ConnectMedia(ctx context.Context, classID, userID, offerSDP string) (*MediaAnswer, error) {
	if err := s.requireMember(ctx, classID, userID); err != nil {
		return nil, err
	}
	if err := s.requireOpen(ctx, classID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(offerSDP) == "" || len(offerSDP) > 65536 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "sdp", Message: "Send the browser's SDP offer."}})
	}
	var stageRole string
	var stageSlot int
	if err := s.db.DB.QueryRowContext(ctx, `SELECT stage_role, stage_slot FROM virtual_class_attendees
		WHERE class_id = ? AND user_id = ? AND left_at IS NULL`, classID, userID).
		Scan(&stageRole, &stageSlot); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httpx.NewError(httpx.CodeForbidden, "Join the class before connecting to it.")
		}
		return nil, err
	}
	if s.config.Media == nil {
		return nil, httpx.NewError(httpx.CodeConflict, "The media server is not running on this deployment.")
	}

	publishing := stageRole == "host" || stageRole == "speaker"
	if !publishing {
		stageSlot = -1
	}
	answer, err := s.config.Media.Offer(classID, userID, store.NewID(), stageSlot, publishing,
		webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP})
	if err != nil {
		// A slot taken by a reconnecting tab is a normal race, not a server fault: the caller retries and the
		// forwarder has already evicted the ghost.
		return nil, httpx.NewError(httpx.CodeConflict, "Could not open the media connection. Try again.")
	}
	return &MediaAnswer{SDP: answer.SDP, StageRole: stageRole, StageSlot: stageSlot}, nil
}

// StageUpdate is one host decision about who is on stage.
type StageUpdate struct {
	UserID string `json:"userId"`
	// Role is "speaker" or "audience".
	Role string `json:"role"`
}

// SetStage promotes a participant to a speaker slot or returns them to the audience.
//
// Slots are allocated by the server from the lowest free one, and a host can never be demoted by this call. The
// stage holds media.StageSlots people including the host, which is a limit of the forwarder's fan-out and of
// what a viewer's downlink can carry, not an arbitrary number.
func (s *Service) SetStage(ctx context.Context, classID, hostID string, in StageUpdate) ([]Attendee, error) {
	if err := s.requireHost(ctx, classID, hostID); err != nil {
		return nil, err
	}
	if in.Role != "speaker" && in.Role != "audience" {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "role",
			Message: "A stage role is speaker or audience."}})
	}
	if in.UserID == "" {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "userId", Message: "Choose somebody in the room."}})
	}

	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var (
			currentRole string
			currentSlot int
			attendeeID  string
		)
		err := tx.QueryRowContext(ctx, `SELECT id, stage_role, stage_slot FROM virtual_class_attendees
			WHERE class_id = ? AND user_id = ?`, classID, in.UserID).
			Scan(&attendeeID, &currentRole, &currentSlot)
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NewError(httpx.CodeNotFound, "That person is not in this class.")
		}
		if err != nil {
			return err
		}
		if currentRole == "host" {
			return httpx.NewError(httpx.CodeConflict, "The host is always on stage.")
		}
		if in.Role == "audience" {
			if _, err := tx.ExecContext(ctx, `UPDATE virtual_class_attendees
				SET stage_role = 'audience', stage_slot = -1 WHERE id = ?`, attendeeID); err != nil {
				return err
			}
			// Demotion has to stop the stream, not just relabel it: the forwarder would otherwise keep
			// copying a demoted speaker's camera to the room.
			if s.config.Media != nil {
				s.config.Media.LeaveUser(classID, in.UserID)
			}
			return nil
		}
		if currentSlot > 0 {
			return nil // already on stage; promoting twice is not an error
		}
		// Lowest free slot, host's slot 0 excluded.
		var taken []int
		rows, err := tx.QueryContext(ctx, `SELECT stage_slot FROM virtual_class_attendees
			WHERE class_id = ? AND stage_role = 'speaker' AND stage_slot > 0`, classID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var slot int
			if err := rows.Scan(&slot); err != nil {
				rows.Close()
				return err
			}
			taken = append(taken, slot)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		free := 0
		for candidate := 1; candidate < media.StageSlots; candidate++ {
			used := false
			for _, slot := range taken {
				if slot == candidate {
					used = true
					break
				}
			}
			if !used {
				free = candidate
				break
			}
		}
		if free == 0 {
			return httpx.NewError(httpx.CodeConflict,
				fmt.Sprintf("The stage is full: %d speakers plus the host.", media.StageSlots-1))
		}
		_, err = tx.ExecContext(ctx, `UPDATE virtual_class_attendees
			SET stage_role = 'speaker', stage_slot = ? WHERE id = ?`, free, attendeeID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Roster(ctx, classID, hostID)
}

// ── the whiteboard ──────────────────────────────────────────────────────────────────────────────────

// AppendBoardEvent stores one opaque, client-encrypted board event.
//
// The server validates *shape* and nothing else: base64url nonce and ciphertext of sane size. It never decodes
// them, never logs them, and has no key. A payload that is not base64 cannot be stored, which is what stops this
// endpoint quietly becoming a plaintext notes service the first time someone finds it easier.
func (s *Service) AppendBoardEvent(ctx context.Context, classID, userID, nonce, ciphertext string) (int, error) {
	if err := s.requireMember(ctx, classID, userID); err != nil {
		return 0, err
	}
	if err := validateOpaque("nonce", nonce, 8, 64); err != nil {
		return 0, err
	}
	if err := validateOpaque("ciphertext", ciphertext, 16, 16384); err != nil {
		return 0, err
	}

	seq := 0
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var seqErr error
		if seq, seqErr = nextSeq(ctx, tx, s.db.Driver, classID, "board_seq"); seqErr != nil {
			return seqErr
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO virtual_class_board_events
			(id, class_id, seq, author_user_id, nonce, ciphertext, created_at)
			VALUES (?,?,?,?,?,?,?)`,
			store.NewID(), classID, seq, userID, nonce, ciphertext, store.Now()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE virtual_class_attendees SET last_seen_at = ?
			WHERE class_id = ? AND user_id = ?`, store.Now(), classID, userID)
		return err
	})
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// BoardEvents returns everything after `since`, so a late joiner replays the board instead of seeing a blank
// canvas.
func (s *Service) BoardEvents(ctx context.Context, classID, userID string, since int) ([]BoardEvent, error) {
	if err := s.requireMember(ctx, classID, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.DB.QueryContext(ctx, `SELECT seq, author_user_id, nonce, ciphertext, created_at
		FROM virtual_class_board_events WHERE class_id = ? AND seq > ? ORDER BY seq ASC LIMIT 2000`,
		classID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BoardEvent{}
	for rows.Next() {
		var event BoardEvent
		if err := rows.Scan(&event.Seq, &event.AuthorID, &event.Nonce, &event.Ciphertext, &event.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

var base64URL = regexp.MustCompile(`^[A-Za-z0-9_\-+/=]+$`)

func validateOpaque(field, value string, minLen, maxLen int) error {
	if len(value) < minLen || len(value) > maxLen {
		return httpx.Validation([]httpx.FieldError{{Field: field,
			Message: fmt.Sprintf("Must be opaque ciphertext between %d and %d characters.", minLen, maxLen)}})
	}
	if !base64URL.MatchString(value) {
		return httpx.Validation([]httpx.FieldError{{Field: field,
			Message: "Must be base64 ciphertext. The whiteboard is end-to-end encrypted, so the API cannot store plaintext."}})
	}
	return nil
}

// ── signalling ─────────────────────────────────────────────────────────────────────────────────────

// PostSignal queues one signalling message for the room or for one peer.
func (s *Service) PostSignal(ctx context.Context, classID, userID, kind string, toUserID, payload string) (int, error) {
	if err := s.requireMember(ctx, classID, userID); err != nil {
		return 0, err
	}
	switch kind {
	case "offer", "answer", "ice", "presence", "leave", "chat":
	default:
		return 0, httpx.Validation([]httpx.FieldError{{Field: "kind",
			Message: "Signal kind must be offer, answer, ice, presence, leave or chat."}})
	}
	if len(payload) == 0 || len(payload) > 65536 {
		return 0, httpx.Validation([]httpx.FieldError{{Field: "payload", Message: "A signal payload is 1–64 KiB."}})
	}
	if toUserID != "" && s.roleOf(ctx, classID, toUserID) == "" {
		return 0, httpx.NewError(httpx.CodeNotFound, "That person is not in this class.")
	}

	seq := 0
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		// Pruning on write keeps the table bounded without a scheduled job: signalling rows are worthless after
		// a few minutes and carry addresses that should not accumulate.
		if _, err := tx.ExecContext(ctx, `DELETE FROM virtual_class_signals
			WHERE class_id = ? AND created_at < ?`, classID,
			store.TS(s.now().Add(-signalRetention))); err != nil {
			return err
		}
		var seqErr error
		if seq, seqErr = nextSeq(ctx, tx, s.db.Driver, classID, "signal_seq"); seqErr != nil {
			return seqErr
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO virtual_class_signals
			(id, class_id, seq, from_user_id, to_user_id, kind, payload, created_at)
			VALUES (?,?,?,?,?,?,?,?)`,
			store.NewID(), classID, seq, userID, nullIfEmpty(toUserID), kind, payload, store.Now()); err != nil {
			return err
		}
		// Any signalling traffic is proof of life: this is the heartbeat that keeps presence honest without a
		// separate ping endpoint.
		_, err := tx.ExecContext(ctx, `UPDATE virtual_class_attendees SET last_seen_at = ?
			WHERE class_id = ? AND user_id = ?`, store.Now(), classID, userID)
		return err
	})
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// PollSignals returns messages addressed to the caller (or broadcast), after `since`.
//
// This is a long poll rather than a WebSocket: the API has no WebSocket dependency, a long poll survives every
// proxy in front of it, and signalling here is low-volume — a handful of messages per peer at the start of a
// call. It is also honest about its cost: each waiting client holds one request.
func (s *Service) PollSignals(ctx context.Context, classID, userID string, since, waitSeconds int) ([]Signal, error) {
	if err := s.requireMember(ctx, classID, userID); err != nil {
		return nil, err
	}
	if waitSeconds < 0 {
		waitSeconds = 0
	}
	if waitSeconds > 25 {
		waitSeconds = 25
	}
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	for {
		signals, err := s.signalsSince(ctx, classID, userID, since)
		if err != nil {
			return nil, err
		}
		if len(signals) > 0 || time.Now().After(deadline) {
			return signals, nil
		}
		select {
		case <-ctx.Done():
			return []Signal{}, nil
		case <-time.After(400 * time.Millisecond):
		}
	}
}

func (s *Service) signalsSince(ctx context.Context, classID, userID string, since int) ([]Signal, error) {
	rows, err := s.db.DB.QueryContext(ctx, `SELECT seq, from_user_id, to_user_id, kind, payload, created_at
		FROM virtual_class_signals
		WHERE class_id = ? AND seq > ? AND (to_user_id IS NULL OR to_user_id = ? OR from_user_id = ?)
		ORDER BY seq ASC LIMIT 200`, classID, since, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Signal{}
	for rows.Next() {
		var signal Signal
		var to sql.NullString
		if err := rows.Scan(&signal.Seq, &signal.FromUserID, &to, &signal.Kind, &signal.Payload,
			&signal.CreatedAt); err != nil {
			return nil, err
		}
		if to.Valid && to.String != "" {
			signal.ToUserID = &to.String
		}
		out = append(out, signal)
	}
	return out, rows.Err()
}

// ── host: attendance and discount codes ────────────────────────────────────────────────────────────

// Attendance reports who came, for how long, and what they paid.
func (s *Service) Attendance(ctx context.Context, classID, userID string) ([]Attendance, error) {
	if err := s.requireHost(ctx, classID, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT a.user_id, COALESCE(u.display_name,'Learner'), a.joined_at, a.left_at, a.last_seen_at, a.credits_paid
		  FROM virtual_class_attendees a LEFT JOIN users u ON u.id = a.user_id
		 WHERE a.class_id = ? ORDER BY a.joined_at ASC`, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Attendance{}
	for rows.Next() {
		var row Attendance
		var joinedAt, lastSeen string
		var leftAt sql.NullString
		if err := rows.Scan(&row.UserID, &row.DisplayName, &joinedAt, &leftAt, &lastSeen, &row.CreditsPaid); err != nil {
			return nil, err
		}
		row.FirstJoin = joinedAt
		row.LastSeen = lastSeen
		row.Sessions = 1
		start, err := store.ParseTS(joinedAt)
		if err != nil {
			continue
		}
		end, endErr := store.ParseTS(lastSeen)
		if leftAt.Valid && leftAt.String != "" {
			if parsed, parseErr := store.ParseTS(leftAt.String); parseErr == nil {
				end, endErr = parsed, nil
			}
		}
		if endErr == nil && end.After(start) {
			row.Minutes = int(end.Sub(start).Minutes())
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// CreateDiscountCode lets a host give cheaper access to their own class.
func (s *Service) CreateDiscountCode(ctx context.Context, classID, userID string, percent, maxUses int, expiresAt string) (*DiscountCode, error) {
	if err := s.requireHost(ctx, classID, userID); err != nil {
		return nil, err
	}
	if percent < 1 || percent > 100 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "percentOff", Message: "A discount is 1–100 percent."}})
	}
	if maxUses < 0 || maxUses > 10000 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "maxUses", Message: "Max uses is 0 (unlimited) to 10000."}})
	}
	var expires any
	if strings.TrimSpace(expiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(expiresAt))
		if err != nil {
			return nil, httpx.Validation([]httpx.FieldError{{Field: "expiresAt", Message: "Use RFC 3339."}})
		}
		expires = store.TS(parsed)
	}

	code := ""
	for attempt := 0; attempt < 6; attempt++ {
		candidate, err := newSlug(8)
		if err != nil {
			return nil, err
		}
		code = candidate
		_, err = s.db.DB.ExecContext(ctx, `INSERT INTO virtual_class_discount_codes
			(id, class_id, code, percent_off, max_uses, uses, expires_at, created_at)
			VALUES (?,?,?,?,?,0,?,?)`,
			store.NewID(), classID, code, percent, maxUses, expires, store.Now())
		if err == nil {
			return &DiscountCode{Code: code, PercentOff: percent, MaxUses: maxUses, CreatedAt: store.Now()}, nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not allocate a discount code")
}

// DiscountCodes lists a class's codes for its host.
func (s *Service) DiscountCodes(ctx context.Context, classID, userID string) ([]DiscountCode, error) {
	if err := s.requireHost(ctx, classID, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.DB.QueryContext(ctx, `SELECT code, percent_off, max_uses, uses, expires_at, created_at
		FROM virtual_class_discount_codes WHERE class_id = ? ORDER BY created_at DESC`, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DiscountCode{}
	for rows.Next() {
		var code DiscountCode
		var expires sql.NullString
		if err := rows.Scan(&code.Code, &code.PercentOff, &code.MaxUses, &code.Uses, &expires, &code.CreatedAt); err != nil {
			return nil, err
		}
		if expires.Valid && expires.String != "" {
			code.ExpiresAt = &expires.String
		}
		out = append(out, code)
	}
	return out, rows.Err()
}

// ── handlers ───────────────────────────────────────────────────────────────────────────────────────

// Handlers exposes the virtual-class API.
type Handlers struct{ svc *Service }

// NewHandlers builds the HTTP handlers.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func classID(r *http.Request) string { return chi.URLParam(r, "id") }

// Create handles POST /v1/virtual-classes.
func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in CreateInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	class, err := h.svc.Create(r.Context(), user.ID, in)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"class": class})
	return nil
}

// List handles GET /v1/virtual-classes.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	classes, err := h.svc.List(r.Context(), user.ID, r.URL.Query().Get("scope"))
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"classes": classes})
	return nil
}

// Get handles GET /v1/virtual-classes/{id}.
func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	class, err := h.svc.Get(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"class": class})
	return nil
}

// Join handles POST /v1/virtual-classes/{id}/join.
func (h *Handlers) Join(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in JoinInput
	if r.ContentLength > 0 {
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
	}
	result, err := h.svc.Join(r.Context(), classID(r), user.ID, in)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// Leave handles POST /v1/virtual-classes/{id}/leave.
func (h *Handlers) Leave(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	if err := h.svc.Leave(r.Context(), classID(r), user.ID); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"left": true})
	return nil
}

// Start handles POST /v1/virtual-classes/{id}/start.
func (h *Handlers) Start(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	class, err := h.svc.Start(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"class": class})
	return nil
}

// End handles POST /v1/virtual-classes/{id}/end.
func (h *Handlers) End(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	class, err := h.svc.End(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"class": class})
	return nil
}

// Roster handles GET /v1/virtual-classes/{id}/roster.
func (h *Handlers) Roster(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	attendees, err := h.svc.Roster(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"attendees": attendees})
	return nil
}

// ICE handles GET /v1/virtual-classes/{id}/ice.
func (h *Handlers) ICE(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	servers, err := h.svc.ICEConfig(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"iceServers": servers})
	return nil
}

// Board handles GET /v1/virtual-classes/{id}/board and POST to append.
func (h *Handlers) Board(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	if r.Method == http.MethodPost {
		var in struct {
			Nonce      string `json:"nonce"`
			Ciphertext string `json:"ciphertext"`
		}
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
		seq, err := h.svc.AppendBoardEvent(r.Context(), classID(r), user.ID, in.Nonce, in.Ciphertext)
		if err != nil {
			return err
		}
		httpx.JSON(w, http.StatusCreated, map[string]any{"seq": seq})
		return nil
	}
	since := atoi(r.URL.Query().Get("since"))
	events, err := h.svc.BoardEvents(r.Context(), classID(r), user.ID, since)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"events": events})
	return nil
}

// Signal handles POST /v1/virtual-classes/{id}/signal and the long-poll GET.
func (h *Handlers) Signal(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	if r.Method == http.MethodPost {
		var in struct {
			Kind     string `json:"kind"`
			ToUserID string `json:"toUserId"`
			Payload  string `json:"payload"`
		}
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
		seq, err := h.svc.PostSignal(r.Context(), classID(r), user.ID, in.Kind, in.ToUserID, in.Payload)
		if err != nil {
			return err
		}
		httpx.JSON(w, http.StatusCreated, map[string]any{"seq": seq})
		return nil
	}
	since := atoi(r.URL.Query().Get("since"))
	wait := atoi(r.URL.Query().Get("wait"))
	signals, err := h.svc.PollSignals(r.Context(), classID(r), user.ID, since, wait)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"signals": signals})
	return nil
}

// Attendance handles GET /v1/virtual-classes/{id}/attendance.
func (h *Handlers) Attendance(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	rows, err := h.svc.Attendance(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"attendance": rows})
	return nil
}

// DiscountCodes handles GET and POST /v1/virtual-classes/{id}/discount-codes.
func (h *Handlers) DiscountCodes(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	if r.Method == http.MethodPost {
		var in struct {
			PercentOff int    `json:"percentOff"`
			MaxUses    int    `json:"maxUses"`
			ExpiresAt  string `json:"expiresAt"`
		}
		if err := httpx.Decode(r, &in); err != nil {
			return err
		}
		code, err := h.svc.CreateDiscountCode(r.Context(), classID(r), user.ID, in.PercentOff, in.MaxUses, in.ExpiresAt)
		if err != nil {
			return err
		}
		httpx.JSON(w, http.StatusCreated, map[string]any{"discountCode": code})
		return nil
	}
	codes, err := h.svc.DiscountCodes(r.Context(), classID(r), user.ID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"discountCodes": codes})
	return nil
}

// Media handles POST /v1/virtual-classes/{id}/media: the one negotiation a participant needs.
func (h *Handlers) Media(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in struct {
		SDP string `json:"sdp"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	answer, err := h.svc.ConnectMedia(r.Context(), classID(r), user.ID, in.SDP)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, answer)
	return nil
}

// Stage handles POST /v1/virtual-classes/{id}/stage: the host moving somebody onto or off the stage.
func (h *Handlers) Stage(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	var in StageUpdate
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	roster, err := h.svc.SetStage(r.Context(), classID(r), user.ID, in)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"attendees": roster})
	return nil
}

// Resolve handles GET /v1/join/{slug}: the invite link a host sends to someone.
//
// It answers what the class is and what it costs before the visitor signs in, because a link that demands an
// account before saying what it is for is a link nobody clicks twice.
func (h *Handlers) Resolve(w http.ResponseWriter, r *http.Request) error {
	slug := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "slug")))
	class, err := h.svc.Get(r.Context(), slug, "")
	if err != nil {
		return err
	}
	type invite struct {
		ID              string `json:"id"`
		Slug            string `json:"slug"`
		Title           string `json:"title"`
		Description     string `json:"description"`
		HostName        string `json:"hostName"`
		StartsAt        string `json:"startsAt"`
		CreditsCost     int    `json:"creditsCost"`
		Capacity        int    `json:"capacity"`
		DurationMinutes int    `json:"durationMinutes"`
		Status          string `json:"status"`
		JoinURL         string `json:"joinUrl"`
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"class": invite{
		ID: class.ID, Slug: class.Slug, Title: class.Title, Description: class.Description,
		HostName: class.HostName, StartsAt: class.StartsAt, CreditsCost: class.CreditsCost,
		Capacity: class.Capacity, DurationMinutes: class.DurationMinutes,
		Status: class.Status, JoinURL: class.JoinURL,
	}})
	return nil
}

func atoi(value string) int {
	out := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return out
		}
		out = out*10 + int(char-'0')
		if out > 1_000_000 {
			return out
		}
	}
	return out
}
