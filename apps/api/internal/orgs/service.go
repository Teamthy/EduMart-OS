// Package orgs owns institutions: the organisations themselves, their memberships, the invitations
// that create memberships, and the org-scoped permissions derived from them.
//
// The rule this package exists to enforce: privilege inside an organisation comes from a membership
// or an invitation, never from a role a user picked for themselves. Creating an organisation makes you
// its owner — scoped to that organisation and recorded in an auditable membership — and grants nothing
// platform-wide. Accepting a teacher invitation is a deliberate act by an owner or administrator, which
// is why that is the one path that can confer the global teacher role.
package orgs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/notify"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Membership roles, ordered from most to least authority within an organisation.
const (
	RoleOwner   = "owner"
	RoleAdmin   = "admin"
	RoleTeacher = "teacher"
	RoleStudent = "student"
)

// Organisation lifecycle.
const (
	// StatusPendingVerification is where every new organisation starts. It can hold members and
	// classes immediately; it cannot be treated as a verified institution.
	StatusPendingVerification = "pending_verification"
	// StatusActive is a verified, approved institution.
	StatusActive = "active"
	// StatusSuspended blocks administrative actions without destroying data.
	StatusSuspended = "suspended"
	// StatusRejected is the end of an application. The organisation keeps its data and its owner, but it
	// is not an institution anybody can be expected to trust.
	StatusRejected = "rejected"
)

// invitationTTL is how long an invitation link stays usable.
const invitationTTL = 7 * 24 * time.Hour

// maxInvitePerHour caps how many invitations one organisation can mint in an hour.
const maxInvitePerHour = 200

var (
	slugPattern  = regexp.MustCompile(`[^a-z0-9]+`)
	emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// Organization is an institution as a client sees it. Role is the caller's own membership role, so a
// list of organisations is also a list of "what can I do here".
type Organization struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Verified   bool   `json:"verified"`
	Role       string `json:"role"`
	Members    int    `json:"members"`
	Classes    int    `json:"classes"`
	CreatedAt  string `json:"createdAt"`
	VerifiedAt string `json:"verifiedAt,omitempty"`
	// OwnerEmail is only populated on the staff approval queue: approving an institution means being able
	// to contact the person who registered it.
	OwnerEmail string `json:"ownerEmail,omitempty"`
}

// Member is one person's membership of an organisation.
type Member struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	JoinedAt    string `json:"joinedAt"`
}

// Invitation is an offer of membership. The token is only ever returned at creation.
type Invitation struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expiresAt"`
	CreatedAt string `json:"createdAt"`
	MaxUses   int    `json:"maxUses"`
	UseCount  int    `json:"useCount"`
}

// Service exposes institution use cases.
type Service struct {
	db *store.Store
	// courier is optional. An invitation is written to the database whether or not a message can be
	// sent, so a relay outage degrades to "share the link yourself" rather than "invitation failed".
	courier notify.Courier
	webURL  string
}

// WithNotifier attaches a courier and the web origin used to build invitation links.
func (s *Service) WithNotifier(c notify.Courier, webURL string) *Service {
	s.courier = c
	s.webURL = strings.TrimRight(webURL, "/")
	return s
}

// New builds the organisation service.
func New(db *store.Store) *Service { return &Service{db: db} }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func slugBase(name string) string {
	slug := slugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" {
		slug = "organisation"
	}
	return slug
}

func randomSuffix(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	out := make([]byte, n)
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out)
}

func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// validRole reports whether a role may be granted through an invitation. Ownership is never handed
// out by link: it is transferred deliberately by another owner, if that feature is ever added.
func validRole(role string) bool {
	switch role {
	case RoleAdmin, RoleTeacher, RoleStudent:
		return true
	}
	return false
}

// rank orders roles so an actor can only act on lower-ranked members.
func rank(role string) int {
	switch role {
	case RoleOwner:
		return 4
	case RoleAdmin:
		return 3
	case RoleTeacher:
		return 2
	case RoleStudent:
		return 1
	}
	return 0
}

// Create makes an organisation and its first owner. The caller becomes owner *of this organisation*
// and gains nothing globally: that distinction is the whole point of the authorisation model.
func (s *Service) Create(ctx context.Context, actor *auth.User, name, kind string) (*Organization, error) {
	// An organisation is a claim about an institution. Without at least a proven contact address there
	// is no one to hold responsible for it, and no way to reach them if it is abused.
	if !actor.EmailVerified {
		return nil, httpx.NewError(httpx.CodeEmailUnverified,
			"Confirm your email address before registering an organisation. It is how we reach you about it.")
	}
	name = strings.TrimSpace(name)
	if len(name) < 3 {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "name", Message: "Give your organisation a name."}})
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "school"
	}
	switch kind {
	case "school", "centre", "ngo", "company":
	default:
		return nil, httpx.Validation([]httpx.FieldError{{
			Field: "kind", Message: "Choose school, centre, ngo or company.",
		}})
	}

	now := store.Now()
	orgID := store.NewID()
	slug := slugBase(name) + "-" + randomSuffix(4)

	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO organizations (id, name, slug, status, kind, created_by, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?)`,
			orgID, name, slug, StatusPendingVerification, kind, actor.ID, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO organization_memberships (id, organization_id, user_id, role, status, joined_at)
			VALUES (?,?,?,?,'active',?)`, store.NewID(), orgID, actor.ID, RoleOwner, now); err != nil {
			return err
		}
		return writeAudit(ctx, tx, actor.ID, "organization.created", "organization", orgID, map[string]any{
			"kind": kind, "status": StatusPendingVerification,
		}, now)
	})
	if err != nil {
		return nil, err
	}
	return &Organization{
		ID: orgID, Name: name, Slug: slug, Kind: kind, Status: StatusPendingVerification,
		Verified: false, Role: RoleOwner, Members: 1, CreatedAt: now,
	}, nil
}

// List returns the organisations the caller belongs to.
func (s *Service) List(ctx context.Context, userID string) ([]Organization, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT o.id, o.name, o.slug, o.kind, o.status, COALESCE(o.verified_at, ''), o.created_at,
		       m.role,
		       (SELECT COUNT(*) FROM organization_memberships x
		         WHERE x.organization_id = o.id AND x.status = 'active'),
		       (SELECT COUNT(*) FROM classes c WHERE c.organization_id = o.id)
		  FROM organizations o
		  JOIN organization_memberships m ON m.organization_id = o.id
		 WHERE m.user_id = ? AND m.status = 'active'
		 ORDER BY o.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Organization{}
	for rows.Next() {
		var org Organization
		var verifiedAt string
		var members, classes int
		if err := rows.Scan(&org.ID, &org.Name, &org.Slug, &org.Kind, &org.Status, &verifiedAt,
			&org.CreatedAt, &org.Role, &members, &classes); err != nil {
			return nil, err
		}
		org.Verified = verifiedAt != ""
		org.VerifiedAt = verifiedAt
		org.Members, org.Classes = members, classes
		out = append(out, org)
	}
	return out, rows.Err()
}

// membershipRole returns the caller's role in the organisation, or "" when they are not a member.
func (s *Service) membershipRole(ctx context.Context, orgID, userID string) (string, error) {
	var role string
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT role FROM organization_memberships
		 WHERE organization_id = ? AND user_id = ? AND status = 'active'`, orgID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return role, nil
}

// memberOrAdmin resolves the caller's access to an organisation.
//
// A non-member gets 404, not 403: whether an organisation exists is itself information, and a
// distinguishable 403 turns id enumeration into a way to map other institutions.
func (s *Service) memberOrAdmin(ctx context.Context, actor *auth.User, orgID string, needAdmin bool) (string, error) {
	role, err := s.membershipRole(ctx, orgID, actor.ID)
	if err != nil {
		return "", err
	}
	if role == "" {
		if actor.HasRole(auth.RolePlatformAdmin) {
			return "platform_admin", nil
		}
		return "", httpx.NewError(httpx.CodeNotFound, "That organisation does not exist.")
	}
	if needAdmin && rank(role) < rank(RoleAdmin) {
		return "", httpx.NewError(httpx.CodeForbidden, "Only organisation owners and administrators can do that.")
	}
	return role, nil
}

// Get returns one organisation the caller may see.
func (s *Service) Get(ctx context.Context, actor *auth.User, orgID string) (*Organization, error) {
	role, err := s.memberOrAdmin(ctx, actor, orgID, false)
	if err != nil {
		return nil, err
	}
	var org Organization
	var verifiedAt string
	var members, classes int
	err = s.db.DB.QueryRowContext(ctx, `
		SELECT o.id, o.name, o.slug, o.kind, o.status, COALESCE(o.verified_at, ''), o.created_at,
		       (SELECT COUNT(*) FROM organization_memberships x
		         WHERE x.organization_id = o.id AND x.status = 'active'),
		       (SELECT COUNT(*) FROM classes c WHERE c.organization_id = o.id)
		  FROM organizations o WHERE o.id = ?`, orgID).Scan(
		&org.ID, &org.Name, &org.Slug, &org.Kind, &org.Status, &verifiedAt, &org.CreatedAt, &members, &classes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That organisation does not exist.")
	}
	if err != nil {
		return nil, err
	}
	org.Verified = verifiedAt != ""
	org.VerifiedAt = verifiedAt
	org.Members, org.Classes = members, classes
	org.Role = role
	return &org, nil
}

// Members lists an organisation's active members. Owners and administrators only: a roster is
// personal data, and a student has no business reading their classmates' email addresses.
func (s *Service) Members(ctx context.Context, actor *auth.User, orgID string) ([]Member, error) {
	if _, err := s.memberOrAdmin(ctx, actor, orgID, true); err != nil {
		return nil, err
	}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT u.id, u.display_name, u.email_normalized, m.role, m.status, m.joined_at
		  FROM organization_memberships m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.organization_id = ? AND m.status = 'active'
		 ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'teacher' THEN 2 ELSE 3 END,
		          u.display_name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.DisplayName, &m.Email, &m.Role, &m.Status, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Invite creates a single-use, email-locked invitation and returns its token exactly once.
//
// The token is stored as a hash: the link is a credential, and a database reader should not be able
// to use one. The email lock is what stops a leaked link from being claimed by whoever finds it.
func (s *Service) Invite(ctx context.Context, actor *auth.User, orgID, email, role string) (*Invitation, string, error) {
	if _, err := s.memberOrAdmin(ctx, actor, orgID, true); err != nil {
		return nil, "", err
	}
	email = auth.NormalizeEmail(email)
	if !emailPattern.MatchString(email) {
		return nil, "", httpx.Validation([]httpx.FieldError{{Field: "email", Message: "Enter a valid email address."}})
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		role = RoleTeacher
	}
	if !validRole(role) {
		return nil, "", httpx.Validation([]httpx.FieldError{{
			Field: "role", Message: "Invite someone as admin, teacher or student.",
		}})
	}

	// Already a member: inviting again would either be a no-op or, worse, silently change a role.
	var existing string
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT id FROM organization_memberships WHERE organization_id = ? AND user_id =
			(SELECT id FROM users WHERE email_normalized = ?)`, orgID, email).Scan(&existing)
	if err == nil {
		return nil, "", httpx.NewError(httpx.CodeConflict, "That person is already a member.")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}

	now := time.Now().UTC()
	var sent int
	if err := s.db.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM invitations WHERE organization_id = ? AND created_at >= ?`,
		orgID, store.TS(now.Add(-time.Hour))).Scan(&sent); err != nil {
		return nil, "", err
	}
	if sent >= maxInvitePerHour {
		return nil, "", httpx.NewError(httpx.CodeRateLimited,
			"Too many invitations sent from this organisation in the last hour.")
	}

	token, err := newToken()
	if err != nil {
		return nil, "", err
	}
	invitationID := store.NewID()
	expires := now.Add(invitationTTL)

	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		// An earlier, still-open invitation for the same address is superseded rather than left to
		// compete: two live links for one person is how duplicate memberships get created.
		if _, err := tx.ExecContext(ctx, `
			UPDATE invitations SET revoked_at = ?
			 WHERE organization_id = ? AND email = ? AND accepted_at IS NULL AND revoked_at IS NULL`,
			store.TS(now), orgID, email); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO invitations
				(id, organization_id, email, token_hash, invited_by, intended_role, expires_at, max_uses, use_count, created_at)
			VALUES (?,?,?,?,?,?,?,1,0,?)`,
			invitationID, orgID, email, hashToken(token), actor.ID, role, store.TS(expires), store.TS(now)); err != nil {
			return err
		}
		return writeAudit(ctx, tx, actor.ID, "organization.invitation_created", "organization", orgID,
			map[string]any{"email": email, "role": role}, store.TS(now))
	})
	if err != nil {
		return nil, "", err
	}

	s.sendInvitation(ctx, actor, orgID, email, role, invitationID, token, expires.Sub(now))

	return &Invitation{
		ID: invitationID, Email: email, Role: role, Status: "pending",
		ExpiresAt: store.TS(expires), CreatedAt: store.TS(now), MaxUses: 1, UseCount: 0,
	}, token, nil
}

// sendInvitation emails the link. It is deliberately best-effort: the invitation row is already
// committed and the token is returned to the inviter, so a failed send costs a retry, not the invite.
func (s *Service) sendInvitation(ctx context.Context, actor *auth.User, orgID, email, role, invitationID, token string, ttl time.Duration) {
	if s.courier == nil {
		return
	}
	var organisation string
	if err := s.db.DB.QueryRowContext(ctx, `SELECT name FROM organizations WHERE id = ?`, orgID).Scan(&organisation); err != nil {
		organisation = "an Edumart institution"
	}
	link := s.webURL
	if link == "" {
		link = "https://edumart.ng"
	}
	msg := notify.Render(notify.TemplateInvitation, notify.ChannelEmail, email, map[string]string{
		"inviter":      actor.DisplayName,
		"organisation": organisation,
		"role":         role,
		"link":         link + "/join?invitation=" + token,
		"ttl":          humaniseTTL(ttl),
	})
	// errors are the caller's queue problem: Send records the message and reports transport failures
	// through the delivery record, and there is nothing useful the inviter could do about a relay.
	_ = s.courier.Send(ctx, msg, notify.Ref{Type: notify.RefInvitation, ID: invitationID})
}

// humaniseTTL renders a duration the way a person would say it.
func humaniseTTL(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days >= 1 {
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return fmt.Sprintf("%d minutes", int(d.Minutes()))
}

// Invitations lists open and recent invitations for administrators.
func (s *Service) Invitations(ctx context.Context, actor *auth.User, orgID string) ([]Invitation, error) {
	if _, err := s.memberOrAdmin(ctx, actor, orgID, true); err != nil {
		return nil, err
	}
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT id, COALESCE(email, ''), intended_role, expires_at, created_at, max_uses, use_count,
		       accepted_at, revoked_at
		  FROM invitations WHERE organization_id = ? ORDER BY created_at DESC LIMIT 100`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().UTC()
	out := []Invitation{}
	for rows.Next() {
		var inv Invitation
		var acceptedAt, revokedAt sql.NullString
		if err := rows.Scan(&inv.ID, &inv.Email, &inv.Role, &inv.ExpiresAt, &inv.CreatedAt,
			&inv.MaxUses, &inv.UseCount, &acceptedAt, &revokedAt); err != nil {
			return nil, err
		}
		switch {
		case acceptedAt.Valid && acceptedAt.String != "":
			inv.Status = "accepted"
		case revokedAt.Valid && revokedAt.String != "":
			inv.Status = "revoked"
		default:
			if expiry, err := store.ParseTS(inv.ExpiresAt); err == nil && now.After(expiry) {
				inv.Status = "expired"
			} else {
				inv.Status = "pending"
			}
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RevokeInvitation withdraws an open invitation. The link stops working immediately.
func (s *Service) RevokeInvitation(ctx context.Context, actor *auth.User, orgID, invitationID string) error {
	if _, err := s.memberOrAdmin(ctx, actor, orgID, true); err != nil {
		return err
	}
	now := store.TS(time.Now().UTC())
	res, err := s.db.DB.ExecContext(ctx, `
		UPDATE invitations SET revoked_at = ?
		 WHERE id = ? AND organization_id = ? AND accepted_at IS NULL AND revoked_at IS NULL`,
		now, invitationID, orgID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return httpx.NewError(httpx.CodeNotFound, "That invitation is not open.")
	}
	return s.auditOne(ctx, actor.ID, "organization.invitation_revoked", orgID, map[string]any{
		"invitationId": invitationID,
	})
}

// Accept joins the organisation behind an invitation token.
//
// Three things must all be true: the token is live, it has uses left, and it was addressed to the
// signed-in account. The last one is why a forwarded email cannot hand someone else a membership.
func (s *Service) Accept(ctx context.Context, user *auth.User, token string) (*Organization, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, httpx.Validation([]httpx.FieldError{{Field: "token", Message: "This invitation link is incomplete."}})
	}

	var invitationID, orgID, role, expiresAt, email string
	var acceptedAt, revokedAt sql.NullString
	var useCount, maxUses int
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT id, organization_id, intended_role, expires_at, COALESCE(email, ''), accepted_at, revoked_at,
		       use_count, max_uses
		  FROM invitations WHERE token_hash = ?`, hashToken(token)).Scan(
		&invitationID, &orgID, &role, &expiresAt, &email, &acceptedAt, &revokedAt, &useCount, &maxUses)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NewError(httpx.CodeNotFound, "That invitation is not valid. Ask for a new one.")
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	switch {
	case acceptedAt.Valid && acceptedAt.String != "":
		return nil, httpx.NewError(httpx.CodeConflict, "That invitation has already been used.")
	case revokedAt.Valid && revokedAt.String != "":
		return nil, httpx.NewError(httpx.CodeConflict, "That invitation was withdrawn.")
	case useCount >= maxUses:
		return nil, httpx.NewError(httpx.CodeConflict, "That invitation has already been used.")
	}
	if expiry, parseErr := store.ParseTS(expiresAt); parseErr == nil && now.After(expiry) {
		return nil, httpx.NewError(httpx.CodeConflict, "That invitation has expired. Ask for a new one.")
	}
	if email != "" && email != auth.NormalizeEmail(user.Email) {
		return nil, httpx.NewError(httpx.CodeForbidden,
			"That invitation was sent to a different email address. Sign in as that account to accept it.")
	}

	teacherGranted := false
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		// Claim a use. The affected-row count is the authority, so two simultaneous accepts with one
		// remaining use cannot both succeed.
		res, err := tx.ExecContext(ctx, `
			UPDATE invitations SET use_count = use_count + 1, accepted_at = ?, accepted_by = ?
			 WHERE id = ? AND use_count < max_uses AND revoked_at IS NULL AND accepted_at IS NULL`,
			store.TS(now), user.ID, invitationID)
		if err != nil {
			return err
		}
		claimed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if claimed != 1 {
			return httpx.NewError(httpx.CodeConflict, "That invitation has already been used.")
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO organization_memberships (id, organization_id, user_id, role, status, joined_at)
			VALUES (?,?,?,?,'active',?)
			ON CONFLICT (organization_id, user_id) DO NOTHING`,
			store.NewID(), orgID, user.ID, role, store.TS(now)); err != nil {
			return err
		}

		// A teacher membership is a deliberate grant by an owner, so it is the one invitation type
		// that confers the platform teacher role. Students and admins gain nothing global.
		if role == RoleTeacher {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO role_assignments (id, user_id, role, scope) VALUES (?,?,?,'global')
				ON CONFLICT (user_id, role, scope) DO NOTHING`,
				store.NewID(), user.ID, auth.RoleTeacher); err != nil {
				return err
			}
			teacherGranted = true
		}

		return writeAudit(ctx, tx, user.ID, "organization.invitation_accepted", "organization", orgID,
			map[string]any{"role": role, "teacherRoleGranted": teacherGranted}, store.TS(now))
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, user, orgID)
}

// RemoveMember ends a membership. Access ends in the same transaction, including the teacher role when
// no other organisation still grants it.
func (s *Service) RemoveMember(ctx context.Context, actor *auth.User, orgID, memberUserID string) error {
	actorRole, err := s.memberOrAdmin(ctx, actor, orgID, true)
	if err != nil {
		return err
	}

	var targetRole string
	err = s.db.DB.QueryRowContext(ctx, `
		SELECT role FROM organization_memberships
		 WHERE organization_id = ? AND user_id = ? AND status = 'active'`, orgID, memberUserID).Scan(&targetRole)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.NewError(httpx.CodeNotFound, "That person is not a member of this organisation.")
	}
	if err != nil {
		return err
	}

	// An administrator cannot act on a peer or a superior; only an owner can remove another owner.
	// Anyone may leave on their own behalf — that is not an administrative action — which is why self
	// removal skips the rank check and lands on the last-owner rule below instead.
	if actorRole != "platform_admin" && actor.ID != memberUserID {
		if rank(targetRole) >= rank(actorRole) {
			return httpx.NewError(httpx.CodeForbidden, "You cannot remove someone at your level or above.")
		}
	}

	now := store.TS(time.Now().UTC())
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if targetRole == RoleOwner {
			var owners int
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM organization_memberships
				 WHERE organization_id = ? AND role = 'owner' AND status = 'active'`, orgID).Scan(&owners); err != nil {
				return err
			}
			if owners <= 1 {
				return httpx.NewError(httpx.CodeConflict,
					"An organisation needs an owner. Make someone else an owner first.")
			}
		}

		res, err := tx.ExecContext(ctx, `
			DELETE FROM organization_memberships WHERE organization_id = ? AND user_id = ?`, orgID, memberUserID)
		if err != nil {
			return err
		}
		removed, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if removed != 1 {
			return httpx.NewError(httpx.CodeNotFound, "That person is not a member of this organisation.")
		}

		// Membership is the reason for the teacher role. Without another organisation granting it, the
		// role goes too — and with it, the class-creation and roster access it carries.
		if targetRole == RoleTeacher {
			var remaining int
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM organization_memberships
				 WHERE user_id = ? AND role = 'teacher' AND status = 'active'`, memberUserID).Scan(&remaining); err != nil {
				return err
			}
			if remaining == 0 {
				if _, err := tx.ExecContext(ctx, `
					DELETE FROM role_assignments WHERE user_id = ? AND role = ? AND scope = 'global'`,
					memberUserID, auth.RoleTeacher); err != nil {
					return err
				}
			}
		}

		return writeAudit(ctx, tx, actor.ID, "organization.member_removed", "organization", orgID,
			map[string]any{"userId": memberUserID, "role": targetRole}, now)
	})
	return err
}

// Verify approves an organisation. Restricted to platform administrators: an institution vouching for
// itself is not verification.
func (s *Service) Verify(ctx context.Context, actor *auth.User, orgID string) (*Organization, error) {
	if !actor.HasRole(auth.RolePlatformAdmin) {
		return nil, httpx.NewError(httpx.CodeForbidden, "Only platform administrators can verify an organisation.")
	}
	now := store.TS(time.Now().UTC())
	res, err := s.db.DB.ExecContext(ctx, `
		UPDATE organizations SET status = ?, verified_at = ?, verified_by = ?, updated_at = ?
		 WHERE id = ? AND verified_at IS NULL`, StatusActive, now, actor.ID, now, orgID)
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		var existing string
		if err := s.db.DB.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id = ?`, orgID).Scan(&existing); err != nil {
			return nil, httpx.NewError(httpx.CodeNotFound, "That organisation does not exist.")
		}
		return nil, httpx.NewError(httpx.CodeConflict, "That organisation is already verified.")
	}
	if err := s.auditOne(ctx, actor.ID, "organization.verified", orgID, map[string]any{"status": StatusActive}); err != nil {
		return nil, err
	}
	return s.Get(ctx, actor, orgID)
}

// ApprovalQueue is the staff view of institutions waiting on a decision.
//
// It lists organisations rather than applications because there is no separate application object: an
// institution exists from the moment it is created, it just is not verified. Status "" means "everything
// that is not active", which is what a reviewer actually wants to see.
func (s *Service) ApprovalQueue(ctx context.Context, actor *auth.User, status string, limit int) ([]Organization, error) {
	if !actor.HasRole(auth.RolePlatformAdmin) {
		return nil, httpx.NewError(httpx.CodeForbidden, "Only platform administrators can review institutions.")
	}
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "", StatusPendingVerification, StatusActive, StatusSuspended, StatusRejected:
	default:
		return nil, httpx.Validation([]httpx.FieldError{{
			Field: "status", Message: "Filter by pending_verification, active, suspended or rejected.",
		}})
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}

	query := `
		SELECT o.id, o.name, o.slug, o.kind, o.status, COALESCE(o.verified_at, ''), o.created_at,
		       (SELECT COUNT(*) FROM organization_memberships m
		         WHERE m.organization_id = o.id AND m.status = 'active'),
		       (SELECT COUNT(*) FROM classes c
		         WHERE c.organization_id = o.id AND c.status = 'active'),
		       COALESCE((SELECT u.email_normalized FROM organization_memberships m
		                  JOIN users u ON u.id = m.user_id
		                 WHERE m.organization_id = o.id AND m.role = 'owner'
		                 ORDER BY m.joined_at LIMIT 1), '')
		  FROM organizations o`
	args := []any{}
	if status == "" {
		// Oldest first: an approval queue that shows the newest applications is a queue nobody works
		// from the front.
		query += ` WHERE o.status <> ?`
		args = append(args, StatusActive)
	} else {
		query += ` WHERE o.status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY o.created_at ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Organization{}
	for rows.Next() {
		var organization Organization
		if err := rows.Scan(&organization.ID, &organization.Name, &organization.Slug, &organization.Kind,
			&organization.Status, &organization.VerifiedAt, &organization.CreatedAt,
			&organization.Members, &organization.Classes, &organization.OwnerEmail); err != nil {
			return nil, err
		}
		organization.Verified = organization.Status == StatusActive
		out = append(out, organization)
	}
	return out, rows.Err()
}

// Reject ends an application. The record stays — an institution that was refused is exactly the record a
// future reviewer needs — and the owner is told why.
func (s *Service) Reject(ctx context.Context, actor *auth.User, orgID, reason string) (*Organization, error) {
	if !actor.HasRole(auth.RolePlatformAdmin) {
		return nil, httpx.NewError(httpx.CodeForbidden, "Only platform administrators can reject an institution.")
	}
	reason = strings.TrimSpace(reason)
	if len(reason) < 3 {
		return nil, httpx.Validation([]httpx.FieldError{{
			Field: "reason", Message: "Say why the institution is being rejected.",
		}})
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}

	now := store.TS(time.Now().UTC())
	var err error
	var res sql.Result
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err = tx.ExecContext(ctx, `
			UPDATE organizations SET status = ?, updated_at = ?
			 WHERE id = ? AND status = ?`, StatusRejected, now, orgID, StatusPendingVerification)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return httpx.NewError(httpx.CodeConflict, "That institution is not waiting for approval.")
		}
		return writeAudit(ctx, tx, actor.ID, "organization.rejected", "organization", orgID,
			map[string]any{"reason": reason}, now)
	})
	if err != nil {
		var apiErr *httpx.Error
		if errors.As(err, &apiErr) && apiErr.Code == httpx.CodeConflict {
			var exists string
			if lookupErr := s.db.DB.QueryRowContext(ctx, `SELECT status FROM organizations WHERE id = ?`, orgID).
				Scan(&exists); lookupErr != nil {
				return nil, httpx.NewError(httpx.CodeNotFound, "That organisation does not exist.")
			}
		}
		return nil, err
	}
	return s.Get(ctx, actor, orgID)
}

func (s *Service) auditOne(ctx context.Context, actorID, action, orgID string, metadata map[string]any) error {
	return writeAudit(ctx, s.db.DB, actorID, action, "organization", orgID, metadata, store.Now())
}

// executor is the subset of *sql.DB and *sql.Tx an audit write needs, so an audit row can share the
// transaction it describes.
type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func writeAudit(ctx context.Context, db executor, actorID, action, resourceType, resourceID string,
	metadata map[string]any, occurredAt string) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, metadata_json, occurred_at)
		VALUES (?,?,?,?,?,?,?)`, store.NewID(), actorID, action, resourceType, resourceID, raw, occurredAt)
	return err
}
