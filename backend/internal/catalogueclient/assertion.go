package catalogueclient

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"time"
)

// maxLifetime is how long the catalogue lets an assertion hold (ten minutes); the intranet makes them
// much shorter, one per request.
const maxLifetime = 10 * time.Minute

// assertionLifetime is how long the assertions of the intranet last: a request needs one for a few
// seconds, so there is no reason to leave a leaked one useful for longer.
const assertionLifetime = time.Minute

// claims is what an assertion says (docs/catalogue-extraction.md, "The contract"). The same few lines
// are in the catalogue (internal/assertion), which checks it: they are copied, not shared, so that the
// two projects depend on nothing of each other. assertion_test.go holds a vector that both must agree on.
type claims struct {
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	Name      string   `json:"name"`
	Groups    []string `json:"groups"`
	Roles     []string `json:"roles"`
	IssuedAt  int64    `json:"iat"`
	ExpiresAt int64    `json:"exp"`
}

var b64 = base64.RawURLEncoding

// sign makes the assertion `v1.<payload>.<signature>` of the claims.
func sign(key ed25519.PrivateKey, c claims) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signed := "v1." + b64.EncodeToString(payload)
	return signed + "." + b64.EncodeToString(ed25519.Sign(key, []byte(signed))), nil
}
