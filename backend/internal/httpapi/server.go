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
	Inventory         ResourceHandler
	Board             ResourceHandler
	Library           ResourceHandler
	UserFiles         ResourceHandler
	ActivityResources ResourceHandler
	Accounting        ResourceHandler
	Distributions     ResourceHandler
	AdminStatus       ResourceHandler
}

func NewRouter(routes Routes) http.Handler {
	mux := http.NewServeMux()

	routes.Health.RegisterRoutes(mux)
	routes.Auth.RegisterRoutes(mux)
	routes.Users.RegisterRoutes(mux)
	routes.Roles.RegisterRoutes(mux)
	routes.Tokens.RegisterRoutes(mux)
	routes.Memberships.RegisterRoutes(mux)
	routes.Licenses.RegisterRoutes(mux)
	routes.Activities.RegisterRoutes(mux)
	routes.Inventory.RegisterRoutes(mux)
	routes.Distributions.RegisterRoutes(mux)
	routes.Board.RegisterRoutes(mux)
	routes.Library.RegisterRoutes(mux)
	routes.UserFiles.RegisterRoutes(mux)
	routes.ActivityResources.RegisterRoutes(mux)
	routes.Accounting.RegisterRoutes(mux)
	routes.AdminStatus.RegisterRoutes(mux)

	// Recoverer has to be the *inner* layer, closer to mux, not the
	// outer one: RequestLogger's own next.ServeHTTP call has no
	// defer/recover of its own, so a panic reaching it from further in
	// unwinds straight past the code that would otherwise log the
	// method/path/status/duration/request_id line — Recoverer(next)
	// wrapping RequestLogger(mux) meant a panic never gave RequestLogger
	// a chance to log anything at all, leaving only Recoverer's own bare
	// "panic recovered" line (message and stack, nothing else). With
	// Recoverer innermost, it turns the panic into an ordinary 500
	// response before returning control to RequestLogger, which then
	// logs its full line exactly as it would for any other response.
	return RequestLogger(Recoverer(mux))
}
