package httpapi

import "net/http"

// ResourceHandler lets each domain package own and register its own routes,
// including whatever auth/role protection each one needs.
type ResourceHandler interface {
	RegisterRoutes(mux *http.ServeMux)
}

type Routes struct {
	Auth        ResourceHandler
	Users       ResourceHandler
	Roles       ResourceHandler
	Tokens      ResourceHandler
	Memberships ResourceHandler
	Licenses    ResourceHandler
	Activities  ResourceHandler
	Inventory   ResourceHandler
	Board       ResourceHandler
}

func NewRouter(routes Routes) http.Handler {
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

	return Recoverer(RequestLogger(mux))
}
