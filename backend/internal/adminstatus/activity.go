package adminstatus

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/requestcount"
)

// periodCounts is a figure over the last hour, the last day and the last month.
type periodCounts struct {
	LastHour  int64 `json:"last_hour"`
	LastDay   int64 `json:"last_day"`
	LastMonth int64 `json:"last_month"`
}

// activityStatus says how much the API is used: how many different people made a request, and how
// many requests there were.
type activityStatus struct {
	Users    periodCounts `json:"users"`
	Requests periodCounts `json:"requests"`
}

// activity reads the figures, or nil if they cannot be read (the rest of the status is still
// worth showing). The people are those with a session used in the period (`tokens.last_used_at`,
// so a session counts in the period of its last use only), counting one person once however many
// sessions they have and leaving out an administrator acting as somebody else; the requests are
// the counts the API keeps per minute (package requestcount), so a few seconds may be missing.
func (h *Handler) activity(ctx context.Context) *activityStatus {
	var a activityStatus
	err := h.pool.QueryRow(ctx, `
		SELECT
			count(DISTINCT user_id) FILTER (WHERE last_used_at >= now() - interval '1 hour'),
			count(DISTINCT user_id) FILTER (WHERE last_used_at >= now() - interval '1 day'),
			count(DISTINCT user_id)
		FROM tokens
		WHERE impersonated_by IS NULL AND last_used_at >= now() - interval '1 month'
	`).Scan(&a.Users.LastHour, &a.Users.LastDay, &a.Users.LastMonth)
	if err != nil {
		return nil
	}
	err = h.pool.QueryRow(ctx, `
		SELECT
			COALESCE(sum(requests) FILTER (WHERE minute >= now() - interval '1 hour'), 0),
			COALESCE(sum(requests) FILTER (WHERE minute >= now() - interval '1 day'), 0),
			COALESCE(sum(requests), 0)
		FROM request_counts
		WHERE minute >= now() - interval '1 month'
	`).Scan(&a.Requests.LastHour, &a.Requests.LastDay, &a.Requests.LastMonth)
	if err != nil {
		return nil
	}
	return &a
}

// ActivityDay is the people who used the API one day, by login, with how many requests each made.
type ActivityDay struct {
	Date  string           `json:"date"`
	Users []ActivityPerson `json:"users"`
}

type ActivityPerson struct {
	Login    string `json:"login"`
	Requests int64  `json:"requests"`
}

type activityCalendar struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Days are only those with some activity, oldest first.
	Days []ActivityDay `json:"days"`
}

// defaultCalendarDays and maxCalendarDays bound ?days= of GET /admin/activity: a year by default, two at
// most.
const (
	defaultCalendarDays = 365
	maxCalendarDays     = 730
)

// activityCalendar answers GET /admin/activity: who used the API each day of the last `days` days (today
// included, in the club's time zone). Reads what the API stores for it (table user_activity, see package
// requestcount) after having it store what is still in memory.
func (h *Handler) activityCalendar(w http.ResponseWriter, r *http.Request) {
	days := defaultCalendarDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxCalendarDays {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_days", "days must be a number from 1 to 730")
			return
		}
		days = n
	}
	if h.flusher != nil {
		_ = h.flusher.Flush(r.Context())
	}
	now := time.Now()
	to := requestcount.Day(now)
	from := requestcount.Day(now.AddDate(0, 0, -(days - 1)))

	rows, err := h.pool.Query(r.Context(), `
		SELECT a.day::text, u.login, a.requests
		FROM user_activity a JOIN users u ON u.id = a.user_id
		WHERE a.day BETWEEN $1::date AND $2::date
		ORDER BY a.day, u.login
	`, from, to)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not read the activity")
		return
	}
	defer rows.Close()
	calendar := activityCalendar{From: from, To: to, Days: []ActivityDay{}}
	for rows.Next() {
		var date, login string
		var requests int64
		if err := rows.Scan(&date, &login, &requests); err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not read the activity")
			return
		}
		if n := len(calendar.Days); n == 0 || calendar.Days[n-1].Date != date {
			calendar.Days = append(calendar.Days, ActivityDay{Date: date, Users: []ActivityPerson{}})
		}
		day := &calendar.Days[len(calendar.Days)-1]
		day.Users = append(day.Users, ActivityPerson{Login: login, Requests: requests})
	}
	if err := rows.Err(); err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not read the activity")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, calendar)
}
