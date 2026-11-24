package access_test

import (
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
)

var now = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

type viewerCase struct {
	name   string
	viewer access.Viewer
	kind   access.Kind
}

// Every kind of viewer there is: nobody, an account, a member of the club, a member of another group
// (and of both), and a member whose assertion has expired or has no subject.
func viewers() []viewerCase {
	return []viewerCase{
		{"anonymous", access.Anonymous(), access.KindAnonymous},
		{"registered", access.Viewer{Issuer: "site", Subject: "r1"}, access.KindRegistered},
		{"club member", access.Viewer{Issuer: "intranet", Subject: "m1", Groups: []string{access.ClubGroup}}, access.KindMember},
		{"other group", access.Viewer{Issuer: "other", Subject: "o1", Groups: []string{"other"}}, access.KindMember},
		{"both groups", access.Viewer{Issuer: "intranet", Subject: "b1", Groups: []string{access.ClubGroup, "other"}}, access.KindMember},
		{"expired member", access.Viewer{Issuer: "intranet", Subject: "e1", Groups: []string{access.ClubGroup}, ExpiresAt: now.Add(-time.Second)}, access.KindAnonymous},
		{"not yet expired", access.Viewer{Issuer: "intranet", Subject: "n1", Groups: []string{access.ClubGroup}, ExpiresAt: now.Add(time.Second)}, access.KindMember},
		{"groups with no subject", access.Viewer{Groups: []string{access.ClubGroup}}, access.KindAnonymous},
	}
}

func TestKindAt(t *testing.T) {
	for _, c := range viewers() {
		if got := c.viewer.KindAt(now); got != c.kind {
			t.Errorf("%s: kind %q, want %q", c.name, got, c.kind)
		}
	}
}

// Who each audience admits, for every viewer.
func TestAudienceAdmits(t *testing.T) {
	audiences := []struct {
		name string
		a    access.Audience
		// admitted lists the viewers (by name) it admits.
		admitted map[string]bool
	}{
		{"public", access.Public(), map[string]bool{
			"anonymous": true, "registered": true, "club member": true, "other group": true, "both groups": true,
			"expired member": true, "not yet expired": true, "groups with no subject": true,
		}},
		{"registered", access.Registered(), map[string]bool{
			"registered": true, "club member": true, "other group": true, "both groups": true, "not yet expired": true,
		}},
		{"club", access.Group(access.ClubGroup), map[string]bool{
			"club member": true, "both groups": true, "not yet expired": true,
		}},
		{"other group", access.Group("other"), map[string]bool{"other group": true, "both groups": true}},
		{"a group nobody is in", access.Group("nobody"), map[string]bool{}},
		{"a group with no name", access.Group(""), map[string]bool{}},
		{"a kind that does not exist", access.Audience{Kind: "everyone"}, map[string]bool{}},
	}
	for _, a := range audiences {
		for _, v := range viewers() {
			want := a.admitted[v.name]
			if got := a.a.Admits(v.viewer, now); got != want {
				t.Errorf("audience %s, viewer %s: admits %v, want %v", a.name, v.name, got, want)
			}
		}
	}
}

// ClubOnly crossed with every viewer, part and action: only the club's members, and every part.
func TestClubOnlyAllowsOnlyTheClub(t *testing.T) {
	rules := access.ClubOnly()
	club := map[string]bool{"club member": true, "both groups": true, "not yet expired": true}
	for _, v := range viewers() {
		for _, part := range access.Parts {
			for _, action := range []access.Action{access.ActionSee, access.ActionWrite} {
				if got := rules.Allows(v.viewer, now, action, part); got != club[v.name] {
					t.Errorf("viewer %s, %s %s: %v, want %v", v.name, action, part, got, club[v.name])
				}
			}
		}
	}
}

// With no rules nothing is allowed, and a rule opens only its own part and action: writing does not
// include seeing, and one part does not open another.
func TestRulesDenyByDefaultAndOpenOnlyWhatTheySay(t *testing.T) {
	if (access.Rules{}).Allows(viewers()[2].viewer, now, access.ActionSee, access.PartPlace) {
		t.Error("no rules must allow nothing")
	}
	rules := access.Rules{
		{Part: access.PartPlace, Action: access.ActionSee, Audience: access.Public()},
		{Part: access.PartCoordinates, Action: access.ActionSee, Audience: access.Registered()},
		{Part: access.PartPlace, Action: access.ActionWrite, Audience: access.Group(access.ClubGroup)},
	}
	for _, v := range viewers() {
		kind := v.viewer.KindAt(now)
		inClub := v.viewer.InGroup(access.ClubGroup, now)
		for _, part := range access.Parts {
			for _, action := range []access.Action{access.ActionSee, access.ActionWrite} {
				want := false
				switch {
				case part == access.PartPlace && action == access.ActionSee:
					want = true
				case part == access.PartCoordinates && action == access.ActionSee:
					want = kind != access.KindAnonymous
				case part == access.PartPlace && action == access.ActionWrite:
					want = inClub
				}
				if got := rules.Allows(v.viewer, now, action, part); got != want {
					t.Errorf("viewer %s, %s %s: %v, want %v", v.name, action, part, got, want)
				}
			}
		}
	}
}

// An assertion stops being true at its expiry, exactly then.
func TestExpiryIsExact(t *testing.T) {
	v := access.Viewer{Subject: "m", Groups: []string{access.ClubGroup}, ExpiresAt: now}
	if v.InGroup(access.ClubGroup, now.Add(-time.Nanosecond)) != true || v.InGroup(access.ClubGroup, now) != false {
		t.Error("the assertion must hold until its expiry and not at it")
	}
}
