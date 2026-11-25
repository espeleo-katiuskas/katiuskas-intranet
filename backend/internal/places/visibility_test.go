package places_test

import (
	"slices"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
)

var now = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

type viewer struct {
	name string
	v    access.Viewer
	club bool
}

// Everybody who can ask: nobody, an account, the club's members (and one who is also in another
// group), a member of another group and a club member whose assertion has expired.
var viewers = []viewer{
	{"anonymous", access.Anonymous(), false},
	{"registered", access.Viewer{Issuer: "site", Subject: "r"}, false},
	{"club", access.Viewer{Issuer: "intranet", Subject: "m", Groups: []string{access.ClubGroup}}, true},
	{"club and another group", access.Viewer{Issuer: "intranet", Subject: "b", Groups: []string{access.ClubGroup, "x"}}, true},
	{"another group", access.Viewer{Issuer: "x", Subject: "o", Groups: []string{"x"}}, false},
	{"expired club member", access.Viewer{Issuer: "intranet", Subject: "e", Groups: []string{access.ClubGroup}, ExpiresAt: now.Add(-time.Minute)}, false},
}

func vis(place, location, content places.Audience) places.Visibility {
	return places.Visibility{Place: place, Location: location, Content: content}
}

func TestVisibilityValidate(t *testing.T) {
	const club, public = places.AudienceClub, places.AudiencePublic
	cases := []struct {
		name string
		v    places.Visibility
		want error
	}{
		{"all club", vis(club, club, club), nil},
		{"place public only", vis(public, club, club), nil},
		{"place and location", vis(public, public, club), nil},
		{"everything public", vis(public, public, public), nil},
		{"location public on a closed place", vis(club, public, club), places.ErrVisibilityTooOpen},
		{"content public on a closed place", vis(club, club, public), places.ErrVisibilityTooOpen},
		{"an unknown audience", vis("everyone", club, club), places.ErrInvalidVisibility},
		{"an empty one", places.Visibility{}, places.ErrInvalidVisibility},
		{"protected and closed", places.Visibility{Place: club, Location: club, Content: club, Protected: true}, nil},
		{"protected with the place public", places.Visibility{Place: public, Location: club, Content: club, Protected: true}, places.ErrPlaceProtected},
		{"protected with the content public", places.Visibility{Place: public, Location: public, Content: public, Protected: true}, places.ErrPlaceProtected},
	}
	for _, c := range cases {
		if got := c.v.Validate(); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// What each viewer may see, for each visibility, written out by hand (not derived from the rules): the
// club sees every part whatever the visibility is; anybody else sees the levels that are public, and
// never who wrote it nor the activities.
func TestVisibilityRulesCrossedWithEveryViewerAndPart(t *testing.T) {
	const club, public = places.AudienceClub, places.AudiencePublic
	level := map[access.Part]string{
		access.PartPlace: "place", access.PartMeasures: "place",
		access.PartCoordinates: "location", access.PartPoints: "location", access.PartAccess: "location",
		access.PartSheet: "content", access.PartFiles: "content",
		access.PartAuthors: "never", access.PartActivities: "never",
	}
	for _, v := range []places.Visibility{
		vis(club, club, club), vis(public, club, club), vis(public, public, club), vis(public, club, public),
		vis(public, public, public),
	} {
		open := map[string]bool{"place": v.Place == public, "location": v.Location == public, "content": v.Content == public}
		for _, who := range viewers {
			for _, part := range access.Parts {
				want := who.club || open[level[part]]
				if got := v.CanSee(who.v, now, part); got != want {
					t.Errorf("%+v, %s, %s: sees = %v, want %v", v, who.name, part, got, want)
				}
				// Nobody outside the club writes anything.
				if got := v.Rules().Allows(who.v, now, access.ActionWrite, part); got != who.club {
					t.Errorf("%+v, %s, %s: writes = %v, want %v", v, who.name, part, got, who.club)
				}
			}
		}
	}
}

func samplePlace(v places.Visibility) places.Place {
	access, description, sheet := "by the road", "a cave", "pitches"
	depth, altitude, geology := 40, 900, int64(1)
	geologyName := "Caliza"
	return places.Place{
		ListItem: places.ListItem{
			ID: 1, Name: "Cueva", Lat: 40.5, Lon: -3.7, AltitudeM: &altitude, DepthM: &depth,
			GeologyID: &geology, Geology: &geologyName, ActivityCount: 3, OsmChanged: true, Visibility: v,
		},
		Access: &access, Description: &description, TechnicalSheet: &sheet,
		OsmType: new("node"), OsmID: new(int64(7)), OsmChanges: []places.OsmChange{{Key: "position"}},
		CreatedBy: "ana", CreatedByID: 5, CreatedAt: now, UpdatedBy: "beto", UpdatedByID: 6, UpdatedAt: now,
		Points: []places.Point{{ID: 1}}, FileCount: 2,
	}
}

// A place seen by who may not know it is there is gone; one whose parts are closed has them left out,
// and says which.
func TestPlaceViewedBy(t *testing.T) {
	const club, public = places.AudienceClub, places.AudiencePublic
	for _, who := range viewers {
		// Closed: not there for anybody outside the club; whole for the club, with nothing withheld.
		p, ok := samplePlace(vis(club, club, club)).ViewedBy(who.v, now)
		if ok != who.club {
			t.Errorf("closed place, %s: seen = %v, want %v", who.name, ok, who.club)
		}
		if who.club && (len(p.Withheld) != 0 || p.Lat != 40.5 || p.CreatedBy != "ana" || p.ActivityCount != 3 || len(p.Points) != 1) {
			t.Errorf("closed place, the club (%s) must see all of it, got %+v", who.name, p)
		}

		// Public with the rest closed: the place and its measures, and nothing of where, what or who.
		p, ok = samplePlace(vis(public, club, club)).ViewedBy(who.v, now)
		if !ok {
			t.Errorf("a public place must be seen by %s", who.name)
			continue
		}
		if who.club {
			if len(p.Withheld) != 0 {
				t.Errorf("%s is the club: nothing is withheld, got %v", who.name, p.Withheld)
			}
			continue
		}
		wantWithheld := []access.Part{access.PartCoordinates, access.PartPoints, access.PartAccess, access.PartSheet,
			access.PartFiles, access.PartAuthors, access.PartActivities}
		if !slices.Equal(p.Withheld, wantWithheld) {
			t.Errorf("%s: withheld %v, want %v", who.name, p.Withheld, wantWithheld)
		}
		if p.Name != "Cueva" || p.DepthM == nil || *p.DepthM != 40 || p.Geology == nil {
			t.Errorf("%s: the place and its measures must stay, got %+v", who.name, p)
		}
		if p.Lat != 0 || p.Lon != 0 || p.AltitudeM != nil || p.OsmChanged || p.OsmType != nil || p.OsmID != nil || p.OsmChanges != nil {
			t.Errorf("%s: the coordinates, and the element that gives them away, must be left out: %+v", who.name, p)
		}
		if len(p.Points) != 0 || p.Access != nil || p.Description != nil || p.TechnicalSheet != nil || p.FileCount != 0 {
			t.Errorf("%s: points, access, sheet and files must be left out: %+v", who.name, p)
		}
		if p.CreatedBy != "" || p.UpdatedBy != "" || p.CreatedByID != 0 || !p.CreatedAt.IsZero() || p.ActivityCount != 0 {
			t.Errorf("%s: authors and activities must be left out: %+v", who.name, p)
		}
	}

	// The location open and the content closed: the coordinate, the points and the way there are seen,
	// the sheet and the files are not.
	p, _ := samplePlace(vis(public, public, club)).ViewedBy(viewers[0].v, now)
	if p.Lat != 40.5 || len(p.Points) != 1 || p.Access == nil || p.Description != nil || p.FileCount != 0 {
		t.Errorf("location open, content closed: got %+v", p)
	}
	if !slices.Equal(p.Withheld, []access.Part{access.PartSheet, access.PartFiles, access.PartAuthors, access.PartActivities}) {
		t.Errorf("withheld %v", p.Withheld)
	}
}

// The listing keeps the places a viewer may know exist, each as they may see it, and counts nothing
// else: the places that are closed are not in it.
func TestVisibleToListsOnlyWhatMayBeKnown(t *testing.T) {
	const club, public = places.AudienceClub, places.AudiencePublic
	list := []places.ListItem{
		{ID: 1, Name: "A", Lat: 1, Lon: 1, Visibility: vis(club, club, club)},
		{ID: 2, Name: "B", Lat: 2, Lon: 2, Visibility: vis(public, club, club)},
		{ID: 3, Name: "C", Lat: 3, Lon: 3, Visibility: vis(public, public, public)},
	}
	ids := func(l []places.ListItem) []int64 {
		out := []int64{}
		for _, it := range l {
			out = append(out, it.ID)
		}
		return out
	}
	if got := ids(places.VisibleTo(list, viewers[0].v, now)); !slices.Equal(got, []int64{2, 3}) {
		t.Errorf("anonymous: %v, want [2 3]", got)
	}
	if got := ids(places.VisibleTo(list, viewers[2].v, now)); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Errorf("club: %v, want [1 2 3]", got)
	}
	seen := places.VisibleTo(list, viewers[0].v, now)
	if seen[0].Lat != 0 || seen[1].Lat != 3 {
		t.Errorf("only the place with its location open keeps its coordinate: %+v", seen)
	}
	if list[1].Lat != 2 {
		t.Error("the list that was given must not be changed")
	}
}

// A system links caves: a viewer sees the cavities they may see, and the system is not there when they
// see none. Its description and files need the content of every cavity.
func TestSystemViewedBy(t *testing.T) {
	const club, public = places.AudienceClub, places.AudiencePublic
	cavity := func(id int64, v places.Visibility) places.SystemPlace {
		return places.SystemPlace{ListItem: places.ListItem{ID: id, Lat: 5, Visibility: v}, Points: []places.Point{{ID: 1}}, Files: []places.File{{ID: 1}}}
	}
	description := "connected"
	system := places.System{
		Name: "Sistema", Description: &description, FileCount: 1, CreatedBy: "ana", CreatedByID: 5,
		Places: []places.SystemPlace{cavity(1, vis(public, public, public)), cavity(2, vis(club, club, club)), cavity(3, vis(public, club, club))},
	}
	anon, ok := system.ViewedBy(viewers[0].v, now)
	if !ok || len(anon.Places) != 2 || anon.Places[0].ID != 1 || anon.Places[1].ID != 3 {
		t.Fatalf("anonymous must see the two public cavities, got ok=%v %+v", ok, anon.Places)
	}
	if anon.Description != nil || anon.FileCount != 0 || anon.CreatedBy != "" {
		t.Errorf("a system with a cavity whose content is closed has no description, files or author: %+v", anon)
	}
	if anon.Places[0].Lat != 5 || len(anon.Places[0].Files) != 1 {
		t.Errorf("the open cavity keeps its coordinate and files: %+v", anon.Places[0])
	}
	if anon.Places[1].Lat != 0 || len(anon.Places[1].Points) != 0 || len(anon.Places[1].Files) != 0 {
		t.Errorf("the cavity with its location and content closed has them left out: %+v", anon.Places[1])
	}
	club1, ok := system.ViewedBy(viewers[2].v, now)
	if !ok || len(club1.Places) != 3 || club1.Description == nil || club1.FileCount != 1 || club1.CreatedBy != "ana" {
		t.Errorf("the club sees the whole system: ok=%v %+v", ok, club1)
	}
	open := places.System{Description: &description, FileCount: 1, Places: []places.SystemPlace{cavity(1, vis(public, public, public))}}
	if s, ok := open.ViewedBy(viewers[0].v, now); !ok || s.Description == nil || s.FileCount != 1 {
		t.Errorf("a system whose cavities are all open in content shows its description and files: ok=%v %+v", ok, s)
	}
	closed := places.System{Places: []places.SystemPlace{cavity(2, vis(club, club, club))}}
	if _, ok := closed.ViewedBy(viewers[0].v, now); ok {
		t.Error("a system none of whose cavities are seen is not there")
	}
	// A system with no cavities is for the club alone.
	empty := places.System{Name: "Vacío"}
	if _, ok := empty.ViewedBy(viewers[0].v, now); ok {
		t.Error("a system with no cavities is not there for who is not in the club")
	}
	if s, ok := empty.ViewedBy(viewers[2].v, now); !ok || s.Name != "Vacío" {
		t.Error("the club sees a system with no cavities")
	}
}
