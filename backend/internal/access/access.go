// Package access decides who may see and change what, for the catalogue of places and whatever comes
// after it. It knows nothing about the club: a Viewer is an assertion made by some issuer (the intranet
// is the first), the rules say which audiences may do which action on which part of a resource, and a
// question about them is always answered by Rules.Allows, never by a handler or by the frontend.
//
// An audience is one of three kinds, in a scale: anybody (public), anybody who has an account
// (registered) and the members of a group (the club's is ClubGroup). Nothing is public unless a rule
// says so: no rule, no access.
package access

import "time"

// ClubGroup is the group of the members of the club, the first group of the catalogue.
const ClubGroup = "katiuskas"

// Viewer is who is asking: an assertion made by an issuer (the intranet signs people in and says who
// they are), not a row of a table of people. An anonymous viewer has no subject; a registered one has a
// subject and no groups; a member of a group has it in Groups.
type Viewer struct {
	// Issuer is who made the assertion, and Subject who the viewer is for that issuer. The catalogue
	// never reads anything of the person from the subject: it is a name, nothing else.
	Issuer  string
	Subject string
	// Groups are the groups the viewer belongs to.
	Groups []string
	// ExpiresAt is when the assertion stops being true; the zero value means it does not expire (a
	// session that is checked on every request).
	ExpiresAt time.Time
}

// Anonymous is the viewer of somebody who has not signed in.
func Anonymous() Viewer { return Viewer{} }

// Kind is how much the catalogue knows of a viewer: nothing, an account, or a group.
type Kind string

const (
	KindAnonymous  Kind = "anonymous"
	KindRegistered Kind = "registered"
	KindMember     Kind = "member"
)

// active says whether the assertion is still true at `now`: it has a subject and has not expired. One
// that is not says nothing, whatever groups it lists.
func (v Viewer) active(now time.Time) bool {
	return v.Subject != "" && (v.ExpiresAt.IsZero() || now.Before(v.ExpiresAt))
}

// InGroup says whether the viewer belongs to the group, at `now`: an assertion that has expired, or
// has no subject, says nothing.
func (v Viewer) InGroup(group string, now time.Time) bool {
	if !v.active(now) {
		return false
	}
	for _, g := range v.Groups {
		if g == group {
			return true
		}
	}
	return false
}

// KindAt is what kind of viewer this is at `now`; one whose assertion has expired is anonymous.
func (v Viewer) KindAt(now time.Time) Kind {
	switch {
	case !v.active(now):
		return KindAnonymous
	case len(v.Groups) > 0:
		return KindMember
	default:
		return KindRegistered
	}
}

// AudienceKind is the kind of an audience: anybody, anybody with an account, or a group.
type AudienceKind string

const (
	AudiencePublic     AudienceKind = "public"
	AudienceRegistered AudienceKind = "registered"
	AudienceGroup      AudienceKind = "group"
)

// Audience is who a rule is for.
type Audience struct {
	Kind AudienceKind
	// Group is the group of an audience of kind group; empty otherwise.
	Group string
}

// Public is anybody, signed in or not.
func Public() Audience { return Audience{Kind: AudiencePublic} }

// Registered is anybody with an account, whatever they belong to (a member of a group has one).
func Registered() Audience { return Audience{Kind: AudienceRegistered} }

// Group is the members of a group.
func Group(name string) Audience { return Audience{Kind: AudienceGroup, Group: name} }

// Admits says whether the viewer is in the audience at `now`. An audience of a kind that is not one of
// the three admits nobody.
func (a Audience) Admits(v Viewer, now time.Time) bool {
	switch a.Kind {
	case AudiencePublic:
		return true
	case AudienceRegistered:
		return v.KindAt(now) != KindAnonymous
	case AudienceGroup:
		return a.Group != "" && v.InGroup(a.Group, now)
	default:
		return false
	}
}

// Part is a part of a place that can be more or less open than the rest (the classification the roadmap
// proposes). A route that is about the place as a whole asks for PartPlace.
type Part string

const (
	// PartPlace is the place as a whole: that it exists, with its name, type, state and zone.
	PartPlace Part = "place"
	// PartMeasures are its depth, horizontal length and geology.
	PartMeasures Part = "measures"
	// PartCoordinates is the main coordinate, the one that must not leak.
	PartCoordinates Part = "coordinates"
	// PartPoints are the other points (parking, approach, access).
	PartPoints Part = "points"
	// PartAccess is how to get there.
	PartAccess Part = "access"
	// PartSheet is the description and the technical sheet.
	PartSheet Part = "sheet"
	// PartFiles are the photos and documents.
	PartFiles Part = "files"
	// PartAuthors is who created and changed it, and when.
	PartAuthors Part = "authors"
	// PartActivities is how many activities went there, and which.
	PartActivities Part = "activities"
)

// Parts are all the parts, in the order of the classification.
var Parts = []Part{
	PartPlace, PartMeasures, PartCoordinates, PartPoints, PartAccess, PartSheet, PartFiles, PartAuthors,
	PartActivities,
}

// Action is what is done to a part.
type Action string

const (
	// ActionSee is reading it.
	ActionSee Action = "see"
	// ActionWrite is creating, changing or deleting it. It does not include seeing it: a rule says it.
	ActionWrite Action = "write"
)

// Rule says that an audience may do an action on a part.
type Rule struct {
	Part     Part
	Action   Action
	Audience Audience
}

// Rules are the rules that apply to a resource. What no rule allows is denied.
type Rules []Rule

// Allows says whether the viewer may do the action on the part, at `now`: some rule must say so.
func (rs Rules) Allows(v Viewer, now time.Time, action Action, part Part) bool {
	for _, r := range rs {
		if r.Part == part && r.Action == action && r.Audience.Admits(v, now) {
			return true
		}
	}
	return false
}

// ClubOnly is the rules of a resource that is for the club's members alone: they see and change every
// part, and nobody else sees or changes anything. It is what every place has until somebody opens it.
func ClubOnly() Rules {
	rules := make(Rules, 0, 2*len(Parts))
	for _, part := range Parts {
		for _, action := range []Action{ActionSee, ActionWrite} {
			rules = append(rules, Rule{Part: part, Action: action, Audience: Group(ClubGroup)})
		}
	}
	return rules
}
