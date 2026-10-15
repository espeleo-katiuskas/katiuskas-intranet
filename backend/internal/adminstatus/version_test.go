package adminstatus

import (
	"runtime/debug"
	"testing"
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
