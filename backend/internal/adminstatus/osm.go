package adminstatus

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// What the status says of the mirror of OpenStreetMap (docs/osm-integration.md): for each extract it is fed
// from, how many elements it holds, the date of the data of the last load that went well, and how the last
// load went. The weekly load runs outside the API (cmd/osmsync, from a systemd timer), so nobody would notice
// it stopping: a mirror whose data is older than OSMMaxAge, or whose last load failed, is a problem.

type osmSourceStatus struct {
	Source string `json:"source"`
	// Elements are those of the source that are in OpenStreetMap as far as the last load knows, and Gone
	// those it no longer has (they stay in the table, since a place may point at them).
	Elements int `json:"elements"`
	Gone     int `json:"gone"`
	// LastRunAt, LastRunStatus and LastError are about the latest load, whatever came of it.
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	LastRunStatus string     `json:"last_run_status,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	// LastOKAt, DataTimestamp and what follows are about the latest load that went well: when it ended, the
	// date of the data in the extract, and what it did.
	LastOKAt      *time.Time `json:"last_ok_at,omitempty"`
	DataTimestamp *time.Time `json:"data_timestamp,omitempty"`
	DataAgeDays   int        `json:"data_age_days"`
	Added         int        `json:"added"`
	Changed       int        `json:"changed"`
	Deleted       int        `json:"deleted"`
	Unchanged     int        `json:"unchanged"`
	// UnknownZones is how many municipal boundaries of the extract the club has no zone for (the sign that
	// its list of municipalities is behind), and UnknownZoneCodes the first of their codes. Not a problem.
	UnknownZones     int    `json:"unknown_zones"`
	UnknownZoneCodes string `json:"unknown_zone_codes,omitempty"`
	// Stale says the data is older than the limit; Failing, that the latest load failed.
	Stale   bool `json:"stale"`
	Failing bool `json:"failing"`
}

type osmStatus struct {
	// Elements is how many the mirror holds, not marked as gone.
	Elements int `json:"elements"`
	// MaxAgeDays is how old the data may be before it is a problem (0: not checked).
	MaxAgeDays int               `json:"max_age_days"`
	Sources    []osmSourceStatus `json:"sources"`
}

// osmStatus reads the mirror and its log of loads; nil if they cannot be read (the database is not answering,
// which is a problem of its own).
func (h *Handler) osmStatus(ctx context.Context) *osmStatus {
	rows, err := h.pool.Query(ctx, `
		WITH last_ok AS (
			SELECT DISTINCT ON (source) source, source_timestamp, finished_at, added, changed, deleted, unchanged,
				unknown_zones, unknown_zone_codes
			FROM osm_sync_runs WHERE status = 'ok' ORDER BY source, started_at DESC, id DESC
		), last_run AS (
			SELECT DISTINCT ON (source) source, started_at, status, error
			FROM osm_sync_runs ORDER BY source, started_at DESC, id DESC
		), counts AS (
			SELECT source, count(*) FILTER (WHERE deleted_at IS NULL) AS elements,
				count(*) FILTER (WHERE deleted_at IS NOT NULL) AS gone
			FROM osm_elements GROUP BY source
		)
		SELECT s.source, coalesce(c.elements, 0), coalesce(c.gone, 0),
			r.started_at, r.status, coalesce(r.error, ''),
			o.finished_at, o.source_timestamp,
			coalesce(o.added, 0), coalesce(o.changed, 0), coalesce(o.deleted, 0), coalesce(o.unchanged, 0),
			coalesce(o.unknown_zones, 0), coalesce(o.unknown_zone_codes, '')
		FROM (SELECT source FROM osm_sync_runs UNION SELECT source FROM osm_elements) s
		LEFT JOIN counts c ON c.source = s.source
		LEFT JOIN last_run r ON r.source = s.source
		LEFT JOIN last_ok o ON o.source = s.source
		ORDER BY s.source
	`)
	if err != nil {
		slog.Warn("admin status: cannot read the OpenStreetMap mirror", "error", err)
		return nil
	}
	defer rows.Close()
	status := &osmStatus{Sources: []osmSourceStatus{}, MaxAgeDays: int(h.info.OSMMaxAge.Hours() / 24)}
	now := time.Now()
	for rows.Next() {
		var s osmSourceStatus
		var runStatus *string
		if err := rows.Scan(&s.Source, &s.Elements, &s.Gone, &s.LastRunAt, &runStatus, &s.LastError,
			&s.LastOKAt, &s.DataTimestamp, &s.Added, &s.Changed, &s.Deleted, &s.Unchanged,
			&s.UnknownZones, &s.UnknownZoneCodes); err != nil {
			slog.Warn("admin status: cannot read the OpenStreetMap mirror", "error", err)
			return nil
		}
		if runStatus != nil {
			s.LastRunStatus = *runStatus
		}
		s.Failing = s.LastRunStatus == "failed"
		if s.DataTimestamp != nil {
			s.DataAgeDays = int(now.Sub(*s.DataTimestamp).Hours() / 24)
			s.Stale = h.info.OSMMaxAge > 0 && now.Sub(*s.DataTimestamp) > h.info.OSMMaxAge
		}
		status.Elements += s.Elements
		status.Sources = append(status.Sources, s)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("admin status: cannot read the OpenStreetMap mirror", "error", err)
		return nil
	}
	return status
}

// osmProblems says what is wrong with the mirror: a source whose last load failed, and a source whose data is
// older than the limit (which is what a timer that stopped looks like). A mirror that has never been loaded is
// not a problem: it is optional, and a development machine has none.
func osmProblems(s *osmStatus) []Problem {
	if s == nil {
		return nil
	}
	var problems []Problem
	for _, src := range s.Sources {
		if src.Failing {
			text := fmt.Sprintf("La última carga del mirror de OpenStreetMap («%s») falló", src.Source)
			if src.LastError != "" {
				text += ": " + shorten(src.LastError, 200)
			}
			problems = append(problems, Problem{"osm:" + src.Source + ":failed", text})
		}
		if src.Stale {
			problems = append(problems, Problem{
				"osm:" + src.Source + ":stale",
				fmt.Sprintf("Los datos del mirror de OpenStreetMap («%s») tienen %d días (el límite es %d): la carga semanal no los renueva",
					src.Source, src.DataAgeDays, s.MaxAgeDays),
			})
		}
	}
	return problems
}

func shorten(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n]) + "…"
}
