package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"katiuskas-intranet/backend/internal/auth"
)

// AuthHandler exposes the public, unauthenticated session endpoints.
type AuthHandler interface {
	Login(w http.ResponseWriter, r *http.Request)
	Refresh(w http.ResponseWriter, r *http.Request)
	Logout(w http.ResponseWriter, r *http.Request)
}

// ResourceHandler lets each domain package own and mount its own routes.
type ResourceHandler interface {
	RegisterRoutes(r chi.Router)
}

type Routes struct {
	Auth        AuthHandler
	Users       ResourceHandler
	Memberships ResourceHandler
	Activities  ResourceHandler
	Inventory   ResourceHandler
}

func NewRouter(authenticator *auth.Authenticator, routes Routes) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(RequestLogger)
	r.Use(middleware.Recoverer)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", routes.Auth.Login)
		r.Post("/auth/refresh", routes.Auth.Refresh)
		r.Post("/auth/logout", routes.Auth.Logout)

		r.Group(func(r chi.Router) {
			r.Use(authenticator.RequireAuth)
			routes.Users.RegisterRoutes(r)
			routes.Memberships.RegisterRoutes(r)
			routes.Activities.RegisterRoutes(r)
			routes.Inventory.RegisterRoutes(r)
		})
	})

	return r
}
