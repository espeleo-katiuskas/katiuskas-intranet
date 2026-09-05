package httpapi

import "net/http"

// ResourceHandler lets each domain package own and register its own routes,
// including whatever auth/role protection each one needs.
type ResourceHandler interface {
	RegisterRoutes(mux *http.ServeMux)
}

type Routes struct {
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
}

// mutationNotifier is nil-able — see NotifyMutatingCalls — so passing
// nil here (no Telegram configured) disables that middleware entirely.
func NewRouter(routes Routes, mutationNotifier MutationNotifier) http.Handler {
	mux := http.NewServeMux()

	routes.Auth.RegisterRoutes(mux)
	routes.Users.RegisterRoutes(mux)
	routes.Roles.RegisterRoutes(mux)
	routes.Tokens.RegisterRoutes(mux)
	routes.Memberships.RegisterRoutes(mux)
	routes.Licenses.RegisterRoutes(mux)
	routes.Activities.RegisterRoutes(mux)
	routes.Inventory.RegisterRoutes(mux)
	routes.Board.RegisterRoutes(mux)
	routes.Library.RegisterRoutes(mux)
	routes.UserFiles.RegisterRoutes(mux)
	routes.ActivityResources.RegisterRoutes(mux)

	return Recoverer(RequestLogger(NotifyMutatingCalls(mutationNotifier)(mux)))
}
