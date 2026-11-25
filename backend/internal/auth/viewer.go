package auth

import (
	"context"
	"net/http"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// IntranetIssuer is the issuer of the viewers that the intranet makes of its own sessions: the first
// issuer of the catalogue, and for now the only one.
const IntranetIssuer = access.IntranetIssuer

// ViewerOf is the assertion the intranet makes about a signed-in person: who they are for the intranet
// and, if they are admin, board or a current member (BaselineRoles), a member of the club's group. The
// subject is the person's own opaque one (AuthenticatedUser.Subject), which says nothing of the
// intranet's tables, and the name is their login; a session without a subject is not anybody. It
// carries no expiry, because the session behind it is resolved afresh on every request.
func ViewerOf(user AuthenticatedUser) access.Viewer {
	v := access.Viewer{Issuer: IntranetIssuer, Subject: user.Subject, Name: user.Login}
	if HasBaselineRole(user.Roles) {
		v.Groups = []string{access.ClubGroup}
	}
	return v
}

// ViewerFromContext is who is asking, for the question of what they may see: the person of the session
// if the request has one, and anonymous otherwise. A route made with Protect always has one; one made
// with WithViewer may not.
func ViewerFromContext(ctx context.Context) access.Viewer {
	if user, ok := UserFromContext(ctx); ok {
		return ViewerOf(user)
	}
	return access.Anonymous()
}

// WithViewer is the authentication of a route that answers anybody, and answers more to some: with a
// bearer token the person is resolved as RequireAuth does and the handler finds them (UserFromContext,
// ViewerFromContext); with none the request goes on as anonymous. A token that does not hold is 401, not
// an anonymous request, so that a client never gets the public answer by mistake when its session has
// expired. Unlike Protect it does not ask for a baseline role: what each viewer may see is for the
// handler to ask of the rules (access.Rules.Allows), and it must, since nothing here refuses anybody.
func (a *Authenticator) WithViewer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		user, err := a.resolver.Authenticate(r.Context(), token, httpapi.ClientIP(r), r.UserAgent())
		if err != nil {
			httpapi.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid, expired, or revoked token")
			return
		}
		httpapi.SetRequestUserID(r.Context(), user.ID)
		httpapi.SetRequestActor(r.Context(), user.ID, user.ImpersonatedBy != nil)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	})
}
