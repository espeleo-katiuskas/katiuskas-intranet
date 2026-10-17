package adminstatus

import (
	"runtime/debug"
	"testing"
	"time"
)

func TestModuleVersionHidesDevelPlaceholder(t *testing.T) {
	cases := map[string]string{
		"":                             "",
		"(devel)":                      "",
		"v1.2.3":                       "v1.2.3",
		"v0.0.0-20261001-abc123+dirty": "v0.0.0-20261001-abc123+dirty",
	}
	for in, want := range cases {
		if got := moduleVersion(&debug.BuildInfo{Main: debug.Module{Version: in}}); got != want {
			t.Errorf("version %q: got %q, want %q", in, got, want)
		}
	}
}

func TestBuildFromReadsVersionAndCommitTime(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.3.6"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.time", Value: "2026-10-17T08:30:00Z"},
		},
	}
	got := buildFrom(info)
	if got.Version != "v0.3.6" || !got.CommitTime.Equal(time.Date(2026, 10, 17, 8, 30, 0, 0, time.UTC)) {
		t.Errorf("unexpected build: %+v", got)
	}
	// Nothing recorded, or something unreadable, is just unknown.
	if got := buildFrom(&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.time", Value: "yesterday"}}}); got.Version != "" || !got.CommitTime.IsZero() {
		t.Errorf("expected nothing known, got %+v", got)
	}
}
