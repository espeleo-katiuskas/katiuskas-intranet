package adminstatus

import "context"

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
