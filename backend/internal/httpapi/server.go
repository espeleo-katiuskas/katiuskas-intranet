package httpapi

import "net/http"

// ResourceHandler lets each domain package own and register its own routes,
// including whatever auth/role protection each one needs.
type ResourceHandler interface {
	RegisterRoutes(mux *http.ServeMux)
}

type Routes struct {
	Health            ResourceHandler
	Auth              ResourceHandler
	Users             ResourceHandler
	Roles             ResourceHandler
	Tokens            ResourceHandler
	Memberships       ResourceHandler
	Licenses          ResourceHandler
	Activities        ResourceHandler
	Material          ResourceHandler
	Board             ResourceHandler
	Archive           ResourceHandler
	UserFiles         ResourceHandler
	ActivityResources ResourceHandler
	Accounting        ResourceHandler
	Distributions     ResourceHandler
	AdminStatus       ResourceHandler
	ClubInfo          ResourceHandler
	Budgets           ResourceHandler
	TransactionFiles  ResourceHandler
	Library           ResourceHandler
	Places            ResourceHandler
	Areas             ResourceHandler
	OSM               ResourceHandler
	PublicPlaces      ResourceHandler
	PublicSite        ResourceHandler
	// Catalogue answers the routes of the catalogue (places, systems, zones and the mirror of OpenStreetMap)
	// by forwarding them to the catalogue when it is a project of its own: then Places, Areas, OSM,
	// PublicPlaces and PublicSite are left nil.
	Catalogue ResourceHandler
}

func NewRouter(routes Routes) http.Handler {
	mux := http.NewServeMux()

	// A handler that is not there (the catalogue of places is somebody else's) is skipped.
	for _, h := range []ResourceHandler{
		routes.Health,
		routes.Auth,
		routes.Users,
		routes.Roles,
		routes.Tokens,
		routes.Memberships,
		routes.Licenses,
		routes.Activities,
		routes.Material,
		routes.Distributions,
		routes.Board,
		routes.Archive,
		routes.UserFiles,
		routes.ActivityResources,
		routes.Accounting,
		routes.AdminStatus,
		routes.ClubInfo,
		routes.Budgets,
		routes.TransactionFiles,
		routes.Library,
		routes.Places,
		routes.Areas,
		routes.OSM,
		routes.PublicPlaces,
		routes.PublicSite,
		routes.Catalogue,
	} {
		if h != nil {
			h.RegisterRoutes(mux)
		}
	}

	// Recoverer has to be the *inner* layer, closer to mux, not the
	// outer one: RequestLogger's own next.ServeHTTP call has no
	// defer/recover of its own, so a panic reaching it from further in
	// unwinds straight past the code that would otherwise log the
	// method/path/status/duration line — Recoverer(next)
	// wrapping RequestLogger(mux) meant a panic never gave RequestLogger
	// a chance to log anything at all, leaving only Recoverer's own bare
	// "panic recovered" line (message and stack, nothing else). With
	// Recoverer innermost, it turns the panic into an ordinary 500
	// response before returning control to RequestLogger, which then
	// logs its full line exactly as it would for any other response.
	return RequestLogger(Recoverer(mux))
}
