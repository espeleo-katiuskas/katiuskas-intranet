package httpapi

import "net/http"

// AuthHandler exposes the public, unauthenticated session endpoints.
type AuthHandler interface {
	Login(w http.ResponseWriter, r *http.Request)
	Refresh(w http.ResponseWriter, r *http.Request)
	Logout(w http.ResponseWriter, r *http.Request)
}

// ResourceHandler lets each domain package own and register its own routes,
// including whatever auth/role protection each one needs.
type ResourceHandler interface {
	RegisterRoutes(mux *http.ServeMux)
}

type Routes struct {
	Auth        AuthHandler
	Users       ResourceHandler
	Memberships ResourceHandler
	Activities  ResourceHandler
	Inventory   ResourceHandler
}

func NewRouter(routes Routes) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/auth/login", routes.Auth.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh", routes.Auth.Refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", routes.Auth.Logout)

	routes.Users.RegisterRoutes(mux)
	routes.Memberships.RegisterRoutes(mux)
	routes.Activities.RegisterRoutes(mux)
	routes.Inventory.RegisterRoutes(mux)

	return Recoverer(RequestLogger(mux))
}
