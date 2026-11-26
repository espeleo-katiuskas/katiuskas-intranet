// Package catalogueclient is how the intranet uses the catalogue of places when that is a project of its
// own (Cuevas y simas, with its own API, server and database) instead of a package of this program: it
// vouches for the person who asks with a signed assertion, forwards the requests of the members to the
// catalogue (Proxy), asks it the names of the places an activity goes to (Client.PlaceRefs) and tells it
// which places the club uses (Client.Refer, Client.Unrefer), so that it does not delete them.
//
// The intranet keeps working as before when CATALOGUE_URL is not set; see docs/catalogue-extraction.md.
package catalogueclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/placelinks"
)

// ErrNoPrincipal means a call needed to say who acts and the context has nobody: a request of a
// signed-in person has one, a task of the system must give it (WithPrincipal).
var ErrNoPrincipal = errors.New("nobody to vouch for")

// ErrRefused is an answer of the catalogue that is not a success.
type ErrRefused struct {
	Status int
	Code   string
}

func (e *ErrRefused) Error() string {
	return fmt.Sprintf("the catalogue answered %d (%s)", e.Status, e.Code)
}

// Principal is who an assertion is about.
type Principal struct {
	Subject string
	Name    string
	Groups  []string
	Roles   []string
}

type principalKey struct{}

// WithPrincipal says who acts in a context that has no signed-in person: a reconciliation, for one.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// SystemPrincipal is the club itself acting, for what is not done on behalf of any person.
func SystemPrincipal(groups ...string) Principal {
	return Principal{Subject: "system", Name: "intranet", Groups: groups}
}

// principalOf is who the context says: the person of the session, or whoever WithPrincipal named. A
// person's roles go as admin and board only, which is all that the catalogue asks for.
func principalOf(ctx context.Context) (Principal, error) {
	if user, ok := auth.UserFromContext(ctx); ok {
		if user.Subject == "" {
			return Principal{}, ErrNoPrincipal
		}
		v := auth.ViewerOf(user)
		p := Principal{Subject: v.Subject, Name: v.Name, Groups: v.Groups}
		for _, role := range user.Roles {
			if role == "admin" || role == "board" {
				p.Roles = append(p.Roles, role)
			}
		}
		return p, nil
	}
	if p, ok := ctx.Value(principalKey{}).(Principal); ok {
		return p, nil
	}
	return Principal{}, ErrNoPrincipal
}

// Client talks to the catalogue.
type Client struct {
	base   *url.URL
	issuer string
	key    ed25519.PrivateKey
	http   *http.Client
	now    func() time.Time
}

// New makes a client of the catalogue at baseURL, which makes assertions as issuer with the private key
// (base64 of the 64 bytes of an Ed25519 key, or of its 32-byte seed).
func New(baseURL, issuer, privateKey string) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("the address of the catalogue %q is not an http(s) URL", baseURL)
	}
	if strings.TrimSpace(issuer) == "" {
		return nil, errors.New("the issuer of the assertions is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privateKey))
	if err != nil {
		return nil, errors.New("the private key is not base64")
	}
	var key ed25519.PrivateKey
	switch len(raw) {
	case ed25519.PrivateKeySize:
		key = ed25519.PrivateKey(raw)
	case ed25519.SeedSize:
		key = ed25519.NewKeyFromSeed(raw)
	default:
		return nil, errors.New("the private key is neither an Ed25519 key nor its seed")
	}
	return &Client{
		base: base, issuer: issuer, key: key,
		http: &http.Client{Timeout: 15 * time.Second},
		now:  time.Now,
	}, nil
}

// Assertion makes the assertion about whoever the context says.
func (c *Client) Assertion(ctx context.Context) (string, error) {
	p, err := principalOf(ctx)
	if err != nil {
		return "", err
	}
	return c.assertionFor(p)
}

func (c *Client) assertionFor(p Principal) (string, error) {
	now := c.now()
	return sign(c.key, claims{
		Issuer: c.issuer, Subject: p.Subject, Name: p.Name, Groups: nonNil(p.Groups), Roles: nonNil(p.Roles),
		IssuedAt: now.Unix(), ExpiresAt: now.Add(assertionLifetime).Unix(),
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	token, err := c.Assertion(ctx)
	if err != nil {
		return nil, err
	}
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + path
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	return c.http.Do(req)
}

func refused(resp *http.Response) error {
	var body struct {
		Code string `json:"code"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = json.Unmarshal(data, &body)
	return &ErrRefused{Status: resp.StatusCode, Code: body.Code}
}

// maxRefIDs is how many ids the catalogue names in one question.
const maxRefIDs = 200

// PlaceRefs names the places with those ids as the person of the context may know them: one they may
// not know, or that does not exist, is left out. It satisfies activities.PlaceLookup.
func (c *Client) PlaceRefs(ctx context.Context, ids []int64) ([]placelinks.PlaceRef, error) {
	refs := []placelinks.PlaceRef{}
	for start := 0; start < len(ids); start += maxRefIDs {
		end := min(start+maxRefIDs, len(ids))
		parts := make([]string, 0, end-start)
		for _, id := range ids[start:end] {
			parts = append(parts, strconv.FormatInt(id, 10))
		}
		resp, err := c.do(ctx, http.MethodGet, "/api/v1/places/refs", url.Values{"ids": {strings.Join(parts, ",")}})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			err = refused(resp)
			_ = resp.Body.Close()
			return nil, err
		}
		var page []placelinks.PlaceRef
		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read the names of the places: %w", err)
		}
		refs = append(refs, page...)
	}
	return refs, nil
}

// Refer tells the catalogue that the club uses the place in the way key says ("activity:123"), so that
// it is not deleted. Saying it again changes nothing.
func (c *Client) Refer(ctx context.Context, placeID int64, key string) error {
	return c.reference(ctx, http.MethodPut, placeID, key)
}

// Unrefer tells it that the club stopped using the place in that way. One that was not there is no error.
func (c *Client) Unrefer(ctx context.Context, placeID int64, key string) error {
	return c.reference(ctx, http.MethodDelete, placeID, key)
}

func (c *Client) reference(ctx context.Context, method string, placeID int64, key string) error {
	path := "/api/v1/places/" + strconv.FormatInt(placeID, 10) + "/references/" + url.PathEscape(key)
	resp, err := c.do(ctx, method, path, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return refused(resp)
	}
	return nil
}
