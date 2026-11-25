package places

import (
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
)

// What a viewer may know of a place is decided here, from the visibility of the place, and nowhere
// else. A place the viewer may not see at all (its level `place` is closed to them) is not there for
// them: the functions answer ok=false and the caller treats it as it would a place that does not exist
// (404, left out of a list, not counted). Of a place they may see, each part they may not is left out
// of what they are given (empty or zero) and named in Withheld. For a member of the club nothing is
// ever left out.

// withheld are the parts of a place with this visibility that the viewer may not see; ok is whether
// they may see the place at all.
func (v Visibility) withheld(viewer access.Viewer, now time.Time) (parts []access.Part, ok bool) {
	rules := v.Rules()
	if !rules.Allows(viewer, now, access.ActionSee, access.PartPlace) {
		return nil, false
	}
	for _, part := range access.Parts {
		if !rules.Allows(viewer, now, access.ActionSee, part) {
			parts = append(parts, part)
		}
	}
	return parts, true
}

func hides(parts []access.Part, part access.Part) bool {
	for _, p := range parts {
		if p == part {
			return true
		}
	}
	return false
}

// redact leaves out of the listing's data what is withheld.
func (it *ListItem) redact(parts []access.Part) {
	it.Withheld = parts
	if hides(parts, access.PartCoordinates) {
		// The element of OpenStreetMap a place is gives its coordinates away, and so does knowing it moved.
		it.Lat, it.Lon, it.AltitudeM, it.OsmChanged = 0, 0, nil, false
	}
	if hides(parts, access.PartMeasures) {
		it.DepthM, it.HorizontalLengthM, it.GeologyID, it.Geology = nil, nil, nil, nil
	}
	if hides(parts, access.PartActivities) {
		it.ActivityCount = 0
	}
}

// ViewedBy is the listing's data of the place as the viewer may see it, and whether they may know it
// exists.
func (it ListItem) ViewedBy(viewer access.Viewer, now time.Time) (ListItem, bool) {
	parts, ok := it.Visibility.withheld(viewer, now)
	if !ok {
		return ListItem{}, false
	}
	it.redact(parts)
	return it, true
}

// VisibleTo is the places of a list that the viewer may know exist, each as they may see it.
func VisibleTo(list []ListItem, viewer access.Viewer, now time.Time) []ListItem {
	out := make([]ListItem, 0, len(list))
	for _, it := range list {
		if seen, ok := it.ViewedBy(viewer, now); ok {
			out = append(out, seen)
		}
	}
	return out
}

// ViewedBy is the place as the viewer may see it, and whether they may know it exists.
func (p Place) ViewedBy(viewer access.Viewer, now time.Time) (Place, bool) {
	parts, ok := p.Visibility.withheld(viewer, now)
	if !ok {
		return Place{}, false
	}
	p.ListItem.redact(parts)
	if hides(parts, access.PartCoordinates) {
		p.OsmType, p.OsmID, p.OsmChanges = nil, nil, nil
	}
	if hides(parts, access.PartPoints) {
		p.Points = []Point{}
	}
	if hides(parts, access.PartAccess) {
		p.Access = nil
	}
	if hides(parts, access.PartSheet) {
		p.Description, p.TechnicalSheet = nil, nil
	}
	if hides(parts, access.PartFiles) {
		p.FileCount = 0
	}
	if hides(parts, access.PartAuthors) {
		p.CreatedBy, p.CreatedByID, p.CreatedAt = "", 0, time.Time{}
		p.UpdatedBy, p.UpdatedByID, p.UpdatedAt = "", 0, time.Time{}
	}
	return p, true
}

// ViewedBy is the cavity of a system as the viewer may see it, and whether they may know it exists.
func (p SystemPlace) ViewedBy(viewer access.Viewer, now time.Time) (SystemPlace, bool) {
	parts, ok := p.Visibility.withheld(viewer, now)
	if !ok {
		return SystemPlace{}, false
	}
	p.ListItem.redact(parts)
	if hides(parts, access.PartPoints) {
		p.Points = []Point{}
	}
	if hides(parts, access.PartAccess) {
		p.Access = nil
	}
	if hides(parts, access.PartSheet) {
		p.Description, p.TechnicalSheet = nil, nil
	}
	if hides(parts, access.PartFiles) {
		p.Files = []File{}
	}
	return p, true
}

// ViewedBy is the system as the viewer may see it, and whether they may know it exists. It has the
// cavities they may see, each as they may see it: a system links caves, so showing one that is closed
// would say where another is. A system none of whose cavities they see is not there for them. What
// belongs to the system itself (its description and files) follows the most closed of its cavities:
// they get it only if they see the content of every one, and never who wrote it. The club sees every
// system whole, and a system with no cavities (which the club may keep) is for the club alone.
func (s System) ViewedBy(viewer access.Viewer, now time.Time) (System, bool) {
	if viewer.InGroup(access.ClubGroup, now) {
		return s, true
	}
	seen := make([]SystemPlace, 0, len(s.Places))
	allContent := true
	for _, p := range s.Places {
		v, ok := p.ViewedBy(viewer, now)
		if !ok {
			allContent = false
			continue
		}
		if hides(v.Withheld, access.PartSheet) || hides(v.Withheld, access.PartFiles) {
			allContent = false
		}
		seen = append(seen, v)
	}
	if len(seen) == 0 {
		return System{}, false
	}
	s.Places = seen
	if !allContent {
		s.Description, s.FileCount = nil, 0
	}
	s.CreatedBy, s.CreatedByID, s.CreatedAt = "", 0, time.Time{}
	s.UpdatedBy, s.UpdatedByID, s.UpdatedAt = "", 0, time.Time{}
	return s, true
}
