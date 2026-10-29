package adminstatus

import (
	"context"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// monitorInterval is how often the monitor looks at the status.
	monitorInterval = 5 * time.Minute
	// monitorFirstCheck is the wait before the first look, so that the database a restart is
	// waiting for has time to come up.
	monitorFirstCheck = time.Minute
	// monitorReminder is how long a problem that is still there goes without being mentioned
	// again.
	monitorReminder = 24 * time.Hour
)

// Notifier is what the monitor warns through; telegram.Sender satisfies it.
type Notifier interface {
	Send(ctx context.Context, text string) error
}

// Monitor looks at the same status GET /admin/status shows, every few minutes, and tells an
// administrator when something goes wrong: a database that does not answer, a missing directory,
// a disk almost full. It speaks when the set of problems changes (a new one, or all of them gone)
// and, as a reminder, once a day while one remains, not at every look. It can only warn while the
// API itself is running.
type Monitor struct {
	handler  *Handler
	notifier Notifier
	now      func() time.Time

	mu       sync.Mutex
	known    []string // keys of the problems last reported
	reported time.Time
}

func NewMonitor(handler *Handler, notifier Notifier) *Monitor {
	return &Monitor{handler: handler, notifier: notifier, now: time.Now}
}

// Run looks until ctx is done.
func (m *Monitor) Run(ctx context.Context) {
	timer := time.NewTimer(monitorFirstCheck)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			m.Check(ctx)
			timer.Reset(monitorInterval)
		}
	}
}

// Check looks once and warns if there is something to say.
func (m *Monitor) Check(ctx context.Context) {
	problems := problemsOf(m.handler.collect(ctx), m.handler.info.DiskAlertPercent)
	keys := make([]string, len(problems))
	for i, p := range problems {
		keys[i] = p.Key
	}
	slices.Sort(keys)

	m.mu.Lock()
	changed := !slices.Equal(keys, m.known)
	remind := len(keys) > 0 && m.now().Sub(m.reported) >= monitorReminder
	if !changed && !remind {
		m.mu.Unlock()
		return
	}
	m.known = keys
	m.reported = m.now()
	m.mu.Unlock()

	where := m.where()
	var text string
	if len(problems) == 0 {
		text = "✅ " + where + ": ya no hay ningún problema"
	} else {
		lines := make([]string, len(problems))
		for i, p := range problems {
			lines[i] = "• " + p.Text
		}
		text = "⚠️ " + where + ":\n" + strings.Join(lines, "\n")
	}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := m.notifier.Send(sendCtx, text); err != nil {
		// Not logged at error level, which would try to notify through the same channel.
		slog.Warn("monitor: cannot send the notice", "error", err)
	}
}

// where names the installation by its public address, since testing and production can
// share a chat.
func (m *Monitor) where() string {
	if u, err := url.Parse(m.handler.info.PublicBaseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return "Intranet"
}
