// Package consistency tracks whether a learner keeps turning up.
//
// # Why this is derived rather than counted
//
// A streak is the easiest number in a product to get wrong, and the usual mistake is to make it a column that
// something increments. Then a timezone, a retried request, a deleted row or a missed cron leaves a counter that
// disagrees with reality, and the only way to find out is a complaint. Here a streak is *computed* from the
// activity the rest of the system already records — an attempt started, a live class attended, credits earned —
// so it cannot drift: if the underlying rows say somebody studied on Tuesday, Tuesday is active, and if they do
// not, it is not.
//
// # What counts as a day
//
// Any of:
//   - a practice or exam attempt started (assessment_sessions.started_at),
//   - a live class attended (virtual_class_attendees.joined_at),
//   - credits earned (points_ledger_entries with a positive amount).
//
// Opening the app is deliberately *not* activity. A streak that counts page views measures the notification
// system, not the learning.
//
// Days are UTC days. The database stores UTC instants, and a "day" has to mean one thing on the server; a
// per-user timezone is a real feature (and a real column) rather than something to guess from a request header,
// so the response says which timezone the days are in and the UI shows it.
package consistency

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/auth"
	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// Day is one calendar day in the recent strip.
type Day struct {
	// Date is YYYY-MM-DD in UTC.
	Date string `json:"date"`
	// Active is whether anything above happened that day.
	Active bool `json:"active"`
	// Kinds names what happened, so the UI can say "class + practice" rather than just a dot.
	Kinds []string `json:"kinds"`
	// IsToday and InStreak tell the strip which cells are the run the learner is on.
	IsToday  bool `json:"isToday"`
	InStreak bool `json:"inStreak"`
}

// Summary is the consistency payload.
type Summary struct {
	CurrentStreak int `json:"currentStreak"`
	LongestStreak int `json:"longestStreak"`
	// ActiveDays is how many of the returned window were active.
	ActiveDays int `json:"activeDays"`
	// WindowDays is the length of the strip, so a client can say "12 of the last 30".
	WindowDays int `json:"windowDays"`
	// TotalActiveDays counts everything on record, not just the window.
	TotalActiveDays int `json:"totalActiveDays"`
	// TodayActive distinguishes "streak alive, today not done yet" from "already done".
	TodayActive bool `json:"todayActive"`
	// StreakAtRisk is true when today is not active and yesterday was: the run is alive but only until midnight
	// UTC. It is the one piece of state a reminder needs.
	StreakAtRisk bool `json:"streakAtRisk"`
	// TimeZone names what a "day" means here. It is UTC because the data is.
	TimeZone string `json:"timeZone"`
	Days     []Day  `json:"days"`
	// KindLabels documents the sources, so the UI does not have to hard-code them.
	KindLabels map[string]string `json:"kindLabels"`
}

// Service reads activity history. It owns no tables and writes nothing.
type Service struct {
	db  *store.Store
	now func() time.Time
}

// New builds the service.
func New(db *store.Store) *Service {
	return &Service{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// activity is one (day, kind) pair as recorded.
type activity struct {
	day  string
	kind string
}

// byDay returns every active day and what happened on it, newest first.
//
// These are three small index-backed reads rather than one UNION, because the SQL is portable, readable, and
// each source can be reasoned about on its own. They are also deliberately *not* issued inside an open cursor:
// SQLite runs on a single connection here, so a query inside a result set deadlocks the process.
func (s *Service) byDay(ctx context.Context, userID string) (map[string]map[string]bool, error) {
	queries := []struct {
		kind string
		sql  string
	}{
		{"practice", `SELECT DISTINCT substr(started_at, 1, 10) FROM assessment_sessions
			WHERE user_id = ? AND status <> 'cancelled'`},
		{"class", `SELECT DISTINCT substr(joined_at, 1, 10) FROM virtual_class_attendees
			WHERE user_id = ?`},
		{"credits", `SELECT DISTINCT substr(created_at, 1, 10) FROM points_ledger_entries
			WHERE user_id = ? AND amount > 0`},
	}
	days := map[string]map[string]bool{}
	for _, query := range queries {
		rows, err := s.db.DB.QueryContext(ctx, query.sql, userID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var day string
			if err := rows.Scan(&day); err != nil {
				rows.Close()
				return nil, err
			}
			if len(day) != 10 {
				continue
			}
			if days[day] == nil {
				days[day] = map[string]bool{}
			}
			days[day][query.kind] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return days, nil
}

// Summary computes the streak view for a user over the last `window` days.
func (s *Service) Summary(ctx context.Context, userID string, window int) (*Summary, error) {
	if window <= 0 || window > 180 {
		window = 30
	}
	days, err := s.byDay(ctx, userID)
	if err != nil {
		return nil, err
	}

	today := s.now().Truncate(24 * time.Hour)
	todayKey := today.Format("2006-01-02")

	// Current streak: count backwards from today, or from yesterday when today has not started yet. A run that
	// would break at midnight UTC is "at risk", not broken — a learner who studied last night has not lost
	// anything until the day is actually over.
	current := 0
	cursor := today
	if !hasActivity(days, todayKey) {
		cursor = today.AddDate(0, 0, -1)
	}
	for hasActivity(days, cursor.Format("2006-01-02")) {
		current++
		cursor = cursor.AddDate(0, 0, -1)
	}

	// Longest streak over everything on record.
	keys := make([]string, 0, len(days))
	for day := range days {
		keys = append(keys, day)
	}
	sort.Strings(keys)
	longest := 0
	run := 0
	var previous time.Time
	for _, day := range keys {
		parsed, err := time.Parse("2006-01-02", day)
		if err != nil {
			continue
		}
		if run > 0 && previous.AddDate(0, 0, 1).Equal(parsed) {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
		previous = parsed
	}

	// The strip ends today and runs backwards, then is reversed so it reads left to right in time order.
	strip := make([]Day, 0, window)
	activeInWindow := 0
	for offset := window - 1; offset >= 0; offset-- {
		date := today.AddDate(0, 0, -offset)
		key := date.Format("2006-01-02")
		kinds := make([]string, 0, 3)
		for _, kind := range []string{"class", "practice", "credits"} {
			if days[key][kind] {
				kinds = append(kinds, kind)
			}
		}
		inStreak := false
		if current > 0 {
			// The run covers today going back `current` days, shifted by one when today is still empty.
			start := today
			if !hasActivity(days, todayKey) {
				start = today.AddDate(0, 0, -1)
			}
			inStreak = !date.After(start) && date.After(start.AddDate(0, 0, -current))
		}
		if len(kinds) > 0 {
			activeInWindow++
		}
		strip = append(strip, Day{
			Date:     key,
			Active:   len(kinds) > 0,
			Kinds:    kinds,
			IsToday:  key == todayKey,
			InStreak: inStreak,
		})
	}

	yesterdayActive := hasActivity(days, today.AddDate(0, 0, -1).Format("2006-01-02"))
	todayActive := hasActivity(days, todayKey)
	return &Summary{
		CurrentStreak:   current,
		LongestStreak:   longest,
		ActiveDays:      activeInWindow,
		WindowDays:      window,
		TotalActiveDays: len(days),
		TodayActive:     todayActive,
		StreakAtRisk:    !todayActive && yesterdayActive,
		TimeZone:        "UTC",
		Days:            strip,
		KindLabels: map[string]string{
			"class":    "Live class",
			"practice": "Practice or exam",
			"credits":  "Credits earned",
		},
	}, nil
}

func hasActivity(days map[string]map[string]bool, key string) bool {
	return len(days[key]) > 0
}

// Handlers exposes the consistency API.
type Handlers struct{ svc *Service }

// NewHandlers builds the HTTP handlers.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// Summary handles GET /v1/consistency?days=30.
func (h *Handlers) Summary(w http.ResponseWriter, r *http.Request) error {
	user, err := auth.RequireUser(r)
	if err != nil {
		return err
	}
	window := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		parsed := 0
		for _, char := range raw {
			if char < '0' || char > '9' {
				parsed = 0
				break
			}
			parsed = parsed*10 + int(char-'0')
			if parsed > 1000 {
				break
			}
		}
		if parsed > 0 {
			window = parsed
		}
	}
	summary, err := h.svc.Summary(r.Context(), user.ID, window)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"consistency": summary})
	return nil
}
