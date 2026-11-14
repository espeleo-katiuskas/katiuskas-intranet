package library_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/library"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/users"
)

type fakeNotifier struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeNotifier) Send(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}

// wait takes the oldest notification, waiting for it a little: they are sent in the background.
func (f *fakeNotifier) wait(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		if len(f.sent) > 0 {
			msg := f.sent[0]
			f.sent = f.sent[1:]
			f.mu.Unlock()
			return msg
		}
		f.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for a notification")
	return ""
}

// A 1x1 PNG, so that it is recognised as an image by its bytes.
var pngBytes = func() []byte {
	b, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	if err != nil {
		panic(err)
	}
	return b
}()

type person struct {
	user  testutil.TestUser
	token string
}

type env struct {
	t        *testing.T
	root     string
	tx       pgx.Tx
	mux      *http.ServeMux
	notifier *fakeNotifier
	resolver testutil.FakeResolver
	// who: a plain member, the three roles that manage the library or its loans, and an admin.
	member, biblioteca, material, board, admin person
	libro                                      int64 // the id of the seeded type "Libro"
}

func newEnv(t *testing.T) *env {
	t.Helper()
	tx := testutil.NewTx(t)
	rolesService := roles.NewService(roles.NewRepository(tx))
	membershipsService := memberships.NewService(memberships.NewRepository(tx))
	licensesService := licenses.NewService(licenses.NewRepository(tx), membershipsService)
	usersService := users.NewService(users.NewRepository(tx), rolesService, membershipsService, licensesService)
	authenticator, resolver := testutil.NewTestAuthenticator()
	notifier := &fakeNotifier{}
	root := t.TempDir()
	storage, err := library.NewStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	library.NewHandler(library.NewService(library.NewRepository(tx), storage, rolesService), authenticator, usersService, notifier).RegisterRoutes(mux)
	e := &env{t: t, root: root, tx: tx, mux: mux, notifier: notifier, resolver: resolver}
	e.member = e.person("member")
	e.biblioteca = e.person("member", "biblioteca")
	e.material = e.person("member", "material")
	e.board = e.person("board")
	e.admin = e.person("admin")
	if err := tx.QueryRow(context.Background(), `SELECT id FROM library_types WHERE name = 'Libro'`).Scan(&e.libro); err != nil {
		t.Fatal(err)
	}
	return e
}

// person is a member (a real one: an item can be lent to them) with a token that carries the roles.
func (e *env) person(roles ...string) person {
	u := testutil.InsertMember(e.t, e.tx)
	return person{user: u, token: testutil.TokenFor(e.resolver, u.ID, roles...)}
}

func (e *env) do(req *http.Request, token string) *httptest.ResponseRecorder {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *env) json(method, path, token string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			e.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	return e.do(req, token)
}

func (e *env) upload(method, path, token, fileName string, content []byte, fields map[string]string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if fileName != "" {
		part, err := w.CreateFormFile("file", fileName)
		if err != nil {
			e.t.Fatal(err)
		}
		part.Write(content)
	}
	for k, v := range fields {
		w.WriteField(k, v)
	}
	w.Close()
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return e.do(req, token)
}

func status(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("expected %d, got %d: %s", want, rec.Code, rec.Body.String())
	}
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	var body httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if rec.Code != wantStatus || body.Error.Code != wantCode {
		t.Fatalf("expected %d %q, got %d %q", wantStatus, wantCode, rec.Code, body.Error.Code)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

// newItem creates an item as the librarian and returns it.
func (e *env) newItem(title string) library.Item {
	e.t.Helper()
	rec := e.json("POST", "/api/v1/library/items", e.biblioteca.token, map[string]any{"type_id": e.libro, "acquired_on": "2020-01-01", "title": title})
	status(e.t, rec, http.StatusCreated)
	return decode[library.Item](e.t, rec)
}

func itemPath(b library.Item, rest string) string {
	return fmt.Sprintf("/api/v1/library/items/%d%s", b.ID, rest)
}

// What each role may do: everybody who can sign in reads the items and adds comments and files; admin,
// board and biblioteca create and change items; admin, board, biblioteca and material read and record
// the loans; and a role that is not on top of a baseline one (member, board or admin) is refused
// everywhere, as always.
func TestPermissions(t *testing.T) {
	e := newEnv(t)
	item := e.newItem("Espeleología")
	lonely := testutil.TokenFor(e.resolver, e.member.user.ID, "biblioteca") // without member, board or admin
	loan := decode[library.Loan](t, e.json("POST", itemPath(item, "/loans"), e.board.token, map[string]any{"login": e.member.user.Login}))
	_ = loan

	type call struct {
		method, path string
		body         any
	}
	manageOnly := []call{
		{"POST", "/api/v1/library/items", map[string]any{"type_id": e.libro, "acquired_on": "2020-01-01", "title": "Otro"}},
		{"PUT", itemPath(item, ""), map[string]any{"type_id": e.libro, "acquired_on": "2020-01-01", "title": "Cambiado"}},
		{"PUT", itemPath(item, "/cover"), nil}, // refused before the body is read
		{"POST", "/api/v1/library/types", map[string]any{"name": "Folleto"}},
		{"PUT", fmt.Sprintf("/api/v1/library/types/%d", e.libro), map[string]any{"name": "Libro"}},
		{"DELETE", "/api/v1/library/types/987654", nil},
	}
	loansOnly := []call{
		{"GET", "/api/v1/library/loans", nil},
		{"GET", itemPath(item, "/loans"), nil},
		{"POST", itemPath(item, "/loans"), map[string]any{"login": e.member.user.Login}},
	}
	readers := []call{
		{"GET", "/api/v1/library/items", nil},
		{"GET", itemPath(item, ""), nil},
		{"GET", itemPath(item, "/comments"), nil},
		{"GET", itemPath(item, "/resources"), nil},
		{"GET", "/api/v1/library/types", nil},
	}
	forbidden := func(who string, token string, calls []call) {
		t.Helper()
		for _, c := range calls {
			if rec := e.json(c.method, c.path, token, c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s: %s %s: expected 403, got %d", who, c.method, c.path, rec.Code)
			}
		}
	}
	allowed := func(who string, token string, calls []call) {
		t.Helper()
		for _, c := range calls {
			if rec := e.json(c.method, c.path, token, c.body); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
				t.Errorf("%s: %s %s: refused with %d", who, c.method, c.path, rec.Code)
			}
		}
	}

	// A plain member reads everything about an item (availability included) and nothing about loans.
	allowed("member", e.member.token, readers)
	forbidden("member", e.member.token, manageOnly)
	forbidden("member", e.member.token, loansOnly)
	// Material records loans but does not change the items.
	allowed("material", e.material.token, loansOnly)
	forbidden("material", e.material.token, manageOnly)
	// Biblioteca and board do both; the role alone, without a baseline one, nothing.
	for who, token := range map[string]string{"biblioteca": e.biblioteca.token, "board": e.board.token, "admin": e.admin.token} {
		allowed(who, token, readers)
		allowed(who, token, manageOnly)
		allowed(who, token, loansOnly)
	}
	forbidden("biblioteca without member", lonely, append(append(readers, manageOnly...), loansOnly...))
	if rec := e.json("GET", "/api/v1/library/items", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a token: expected 401, got %d", rec.Code)
	}
}

// Items: created and replaced with every field, listed by title, deleted unless they have been lent.
func TestItemCRUD(t *testing.T) {
	e := newEnv(t)
	rec := e.json("POST", "/api/v1/library/items", e.biblioteca.token, map[string]any{
		"type_id": e.libro, "acquired_on": "2020-01-01", "title": "  La Cueva  ", "author": "Ana", "publisher": "  ", "year": 1999, "isbn": "978-1", "location": "Estante 2",
		"description": "Un ejemplar.",
	})
	status(t, rec, http.StatusCreated)
	item := decode[library.Item](t, rec)
	if item.Title != "La Cueva" || item.Publisher != nil || *item.Year != 1999 || !item.Available || item.HasCover {
		t.Errorf("unexpected item: %+v", item)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha añadido el ejemplar «La Cueva»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	e.newItem("abismo")
	e.newItem("Barranco")
	titles := []string{}
	for _, b := range decode[[]library.Item](t, e.json("GET", "/api/v1/library/items", e.member.token, nil)) {
		titles = append(titles, b.Title)
	}
	if strings.Join(titles, ",") != "abismo,Barranco,La Cueva" {
		t.Errorf("expected them by title, ignoring case, got %v", titles)
	}

	errorCode(t, e.json("POST", "/api/v1/library/items", e.biblioteca.token, map[string]any{"type_id": e.libro, "acquired_on": "2020-01-01", "title": "   "}), 400, "title_required")
	errorCode(t, e.json("POST", "/api/v1/library/items", e.biblioteca.token, map[string]any{"type_id": e.libro, "acquired_on": "2020-01-01", "title": "X", "year": 3000}), 400, "invalid_item_year")
	errorCode(t, e.json("POST", "/api/v1/library/items", e.biblioteca.token, map[string]any{"type_id": e.libro, "acquired_on": "2020-01-01", "title": "X", "year": 999}), 400, "invalid_item_year")

	// An edit replaces everything: what is left out is cleared.
	rec = e.json("PUT", itemPath(item, ""), e.board.token, map[string]any{"type_id": e.libro, "acquired_on": "2020-01-01", "title": "La Cueva II", "author": "Beto"})
	status(t, rec, http.StatusOK)
	edited := decode[library.Item](t, rec)
	if edited.Title != "La Cueva II" || *edited.Author != "Beto" || edited.Year != nil || edited.Location != nil {
		t.Errorf("an edit should replace every field: %+v", edited)
	}
	e.notifier.wait(t) // the creation of the other two
	e.notifier.wait(t)
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha editado el ejemplar «La Cueva»") || !strings.Contains(msg, "Title:") {
		t.Errorf("unexpected edit notice: %q", msg)
	}
	errorCode(t, e.json("PUT", "/api/v1/library/items/987654", e.board.token, map[string]any{"type_id": e.libro, "acquired_on": "2020-01-01", "title": "X"}), 404, "not_found")
	errorCode(t, e.json("GET", "/api/v1/library/items/abc", e.member.token, nil), 400, "invalid_id")

	status(t, e.json("DELETE", itemPath(item, ""), e.biblioteca.token, nil), http.StatusNoContent)
	errorCode(t, e.json("GET", itemPath(item, ""), e.member.token, nil), 404, "not_found")
	errorCode(t, e.json("DELETE", itemPath(item, ""), e.biblioteca.token, nil), 404, "not_found")
}

// An item that is out says so to everybody — and not who has it: nothing a member can read about a
// item carries the person. The loans do, for the roles that manage them.
func TestAvailabilityDoesNotSayWhoHasTheItem(t *testing.T) {
	e := newEnv(t)
	item := e.newItem("Topografías")
	borrower := e.person("member")

	rec := e.json("POST", itemPath(item, "/loans"), e.material.token, map[string]any{"login": borrower.user.Login, "notes": "para el sábado"})
	status(t, rec, http.StatusCreated)
	loan := decode[library.Loan](t, rec)
	if loan.UserLogin != borrower.user.Login || loan.ReturnedAt != nil || loan.RecordedByLogin == "" {
		t.Errorf("unexpected loan: %+v", loan)
	}

	// What the others read.
	for _, path := range []string{"/api/v1/library/items", itemPath(item, ""), itemPath(item, "/comments"), itemPath(item, "/resources")} {
		rec := e.json("GET", path, e.member.token, nil)
		status(t, rec, http.StatusOK)
		if strings.Contains(rec.Body.String(), borrower.user.Login) || strings.Contains(rec.Body.String(), "sábado") {
			t.Errorf("%s tells who has the item: %s", path, rec.Body.String())
		}
	}
	if b := decode[library.Item](t, e.json("GET", itemPath(item, ""), e.member.token, nil)); b.Available {
		t.Error("an item that is out must not be available")
	}
	// What whoever manages the loans reads.
	open := decode[[]library.Loan](t, e.json("GET", "/api/v1/library/loans", e.biblioteca.token, nil))
	if len(open) != 1 || open[0].UserLogin != borrower.user.Login || open[0].ItemTitle != "Topografías" {
		t.Errorf("unexpected open loans: %+v", open)
	}

	// It comes back: available again, and the loan is history.
	rec = e.json("POST", itemPath(item, fmt.Sprintf("/loans/%d/return", loan.ID)), e.biblioteca.token, nil)
	status(t, rec, http.StatusOK)
	if returned := decode[library.Loan](t, rec); returned.ReturnedAt == nil {
		t.Errorf("the return was not recorded: %+v", returned)
	}
	if b := decode[library.Item](t, e.json("GET", itemPath(item, ""), e.member.token, nil)); !b.Available {
		t.Error("an item that is back must be available")
	}
	if open := decode[[]library.Loan](t, e.json("GET", "/api/v1/library/loans", e.biblioteca.token, nil)); len(open) != 0 {
		t.Errorf("no loan should be open: %+v", open)
	}
	all := decode[[]library.Loan](t, e.json("GET", "/api/v1/library/loans?status=all", e.biblioteca.token, nil))
	history := decode[[]library.Loan](t, e.json("GET", itemPath(item, "/loans"), e.biblioteca.token, nil))
	if len(all) != 1 || len(history) != 1 {
		t.Errorf("the history should keep the loan: %d %d", len(all), len(history))
	}
	errorCode(t, e.json("GET", "/api/v1/library/loans?status=nope", e.biblioteca.token, nil), 400, "invalid_status")
}

// The rules of a loan.
func TestLoanRules(t *testing.T) {
	e := newEnv(t)
	item := e.newItem("Cuerdas")
	borrower := e.person("member")
	lend := func(body map[string]any) *httptest.ResponseRecorder {
		return e.json("POST", itemPath(item, "/loans"), e.biblioteca.token, body)
	}

	errorCode(t, lend(map[string]any{}), 400, "loan_login_required")
	errorCode(t, lend(map[string]any{"login": "nadie.nadie"}), 404, "not_found")
	// Only a member now can have an item: somebody who was never one, or has left, cannot.
	outsider := testutil.InsertUser(t, e.tx, "")
	errorCode(t, lend(map[string]any{"login": outsider.Login}), 409, "loan_recipient_not_member")
	errorCode(t, lend(map[string]any{"login": borrower.user.Login, "lent_at": "ayer"}), 400, "invalid_date")
	tomorrow := time.Now().AddDate(0, 0, 2).Format("2006-01-02")
	errorCode(t, lend(map[string]any{"login": borrower.user.Login, "lent_at": tomorrow}), 400, "invalid_dates")
	if b := decode[library.Item](t, e.json("GET", itemPath(item, ""), e.member.token, nil)); !b.Available {
		t.Fatal("none of the refused loans may leave the item out")
	}

	// A loan can be dated in the past, which is how one that was forgotten is written down.
	rec := lend(map[string]any{"login": borrower.user.Login, "lent_at": "2026-10-01"})
	status(t, rec, http.StatusCreated)
	loan := decode[library.Loan](t, rec)
	if loan.LentAt != "2026-10-01" {
		t.Errorf("unexpected date: %s", loan.LentAt)
	}
	// An item that is out cannot be lent again.
	errorCode(t, lend(map[string]any{"login": e.member.user.Login}), 409, "item_not_available")

	returnPath := itemPath(item, fmt.Sprintf("/loans/%d/return", loan.ID))
	ret := func(body any) *httptest.ResponseRecorder { return e.json("POST", returnPath, e.biblioteca.token, body) }
	errorCode(t, ret(map[string]any{"returned_at": "hoy"}), 400, "invalid_date")
	errorCode(t, ret(map[string]any{"returned_at": "2026-09-30"}), 400, "invalid_dates") // before it was lent
	errorCode(t, ret(map[string]any{"returned_at": tomorrow}), 400, "invalid_dates")
	rec = ret(map[string]any{"returned_at": "2026-10-15"})
	status(t, rec, http.StatusOK)
	if got := decode[library.Loan](t, rec); got.ReturnedAt == nil || *got.ReturnedAt != "2026-10-15" {
		t.Errorf("unexpected return: %+v", got)
	}
	errorCode(t, ret(nil), 409, "loan_already_returned")
	errorCode(t, e.json("POST", itemPath(item, "/loans/987654/return"), e.biblioteca.token, nil), 404, "not_found")

	// And now it can go out again.
	status(t, lend(map[string]any{"login": e.member.user.Login}), http.StatusCreated)

	// An item that has been lent cannot be deleted: its history stays.
	errorCode(t, e.json("DELETE", itemPath(item, ""), e.biblioteca.token, nil), 409, "item_has_loans")
}

// Deleting a loan, for one recorded by mistake, is for admin only.
func TestOnlyAdminDeletesALoan(t *testing.T) {
	e := newEnv(t)
	item := e.newItem("Equivocado")
	loan := decode[library.Loan](t, e.json("POST", itemPath(item, "/loans"), e.board.token, map[string]any{"login": e.member.user.Login}))
	path := itemPath(item, fmt.Sprintf("/loans/%d", loan.ID))
	for _, token := range []string{e.biblioteca.token, e.board.token, e.material.token, e.member.token} {
		if rec := e.json("DELETE", path, token, nil); rec.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", rec.Code)
		}
	}
	status(t, e.json("DELETE", path, e.admin.token, nil), http.StatusNoContent)
	errorCode(t, e.json("DELETE", path, e.admin.token, nil), 404, "not_found")
	if b := decode[library.Item](t, e.json("GET", itemPath(item, ""), e.member.token, nil)); !b.Available {
		t.Error("the item should be available after its loan is deleted")
	}
}

// Comments: everybody adds them; whoever wrote one removes it, and so do admin, board and biblioteca.
func TestComments(t *testing.T) {
	e := newEnv(t)
	item := e.newItem("Comentado")
	post := func(p person, body string) *httptest.ResponseRecorder {
		return e.json("POST", itemPath(item, "/comments"), p.token, map[string]any{"body": body})
	}
	rec := post(e.member, "  Muy bueno.  ")
	status(t, rec, http.StatusCreated)
	mine := decode[library.Comment](t, rec)
	if mine.Body != "Muy bueno." || mine.UserLogin != e.member.user.Login {
		t.Errorf("unexpected comment: %+v", mine)
	}
	other := decode[library.Comment](t, post(e.board, "De acuerdo."))
	errorCode(t, post(e.member, "   "), 400, "comment_required")
	errorCode(t, post(e.member, strings.Repeat("a", library.MaxCommentLength+1)), 400, "comment_too_long")
	status(t, post(e.member, strings.Repeat("é", library.MaxCommentLength)), http.StatusCreated) // characters, not bytes
	errorCode(t, e.json("POST", "/api/v1/library/items/987654/comments", e.member.token, map[string]any{"body": "x"}), 404, "not_found")

	list := decode[[]library.Comment](t, e.json("GET", itemPath(item, "/comments"), e.member.token, nil))
	if len(list) != 3 || list[0].ID != mine.ID || list[1].ID != other.ID {
		t.Errorf("unexpected list: %+v", list)
	}
	if b := decode[library.Item](t, e.json("GET", itemPath(item, ""), e.member.token, nil)); b.CommentCount != 3 {
		t.Errorf("the item should count its comments: %d", b.CommentCount)
	}

	del := func(p person, c library.Comment) *httptest.ResponseRecorder {
		return e.json("DELETE", itemPath(item, fmt.Sprintf("/comments/%d", c.ID)), p.token, nil)
	}
	errorCode(t, del(e.member, other), 403, "not_yours")
	errorCode(t, del(e.material, mine), 403, "not_yours") // material does not moderate the library
	status(t, del(e.member, mine), http.StatusNoContent)
	status(t, del(e.biblioteca, other), http.StatusNoContent)
	errorCode(t, del(e.member, mine), 404, "not_found")
}

// Files: everybody attaches them; a cover is for admin, board and biblioteca only and has to be an
// image; the files are shown in the page only if they are PDFs or images.
func TestResourcesAndCover(t *testing.T) {
	e := newEnv(t)
	item := e.newItem("Con ficheros")

	rec := e.upload("POST", itemPath(item, "/resources"), e.member.token, "topo.png", pngBytes, map[string]string{"title": "  La topo  "})
	status(t, rec, http.StatusCreated)
	topo := decode[library.Resource](t, rec)
	if topo.IsCover || topo.ContentType != "image/png" || *topo.Title != "La topo" || topo.UploadedBy != e.member.user.Login {
		t.Errorf("unexpected resource: %+v", topo)
	}
	rec = e.upload("POST", itemPath(item, "/resources"), e.biblioteca.token, "notas.txt", []byte("hola"), nil)
	status(t, rec, http.StatusCreated)
	notes := decode[library.Resource](t, rec)
	errorCode(t, e.upload("POST", itemPath(item, "/resources"), e.member.token, "", nil, nil), 400, "file_required")
	errorCode(t, e.upload("POST", itemPath(item, "/resources"), e.member.token, "vacio.txt", nil, nil), 400, "file_required")
	errorCode(t, e.upload("POST", itemPath(item, "/resources"), e.member.token, "x.txt", []byte("x"),
		map[string]string{"title": strings.Repeat("t", library.MaxResourceTitleLength+1)}), 400, "file_title_too_long")
	errorCode(t, e.upload("POST", "/api/v1/library/items/987654/resources", e.member.token, "x.txt", []byte("x"), nil), 404, "not_found")
	big := bytes.Repeat([]byte("a"), library.MaxFileSize+1)
	errorCode(t, e.upload("POST", itemPath(item, "/resources"), e.member.token, "grande.bin", big, nil), 413, "file_too_large")

	// An image is shown in the page; anything else is downloaded, and sandboxed.
	file := func(r library.Resource, query string) *httptest.ResponseRecorder {
		return e.json("GET", itemPath(item, fmt.Sprintf("/resources/%d/file%s", r.ID, query)), e.member.token, nil)
	}
	rec = file(topo, "")
	status(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), pngBytes) || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "inline") {
		t.Errorf("an image should be shown in the page: %v", rec.Header())
	}
	if !strings.HasPrefix(file(topo, "?download=1").Header().Get("Content-Disposition"), "attachment") {
		t.Error("?download=1 should force a download")
	}
	rec = file(notes, "")
	if !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("anything but an image or a PDF should only be downloaded, sandboxed: %v", rec.Header())
	}
	// The file can be read with the session cookie too, for an <img> or a tab.
	req := httptest.NewRequest("GET", itemPath(item, fmt.Sprintf("/resources/%d/file", topo.ID)), nil)
	req.AddCookie(&http.Cookie{Name: "katiuskas_session", Value: e.member.token})
	status(t, e.do(req, ""), http.StatusOK)

	list := decode[[]library.Resource](t, e.json("GET", itemPath(item, "/resources"), e.member.token, nil))
	if len(list) != 2 || list[0].ID != topo.ID {
		t.Errorf("unexpected list: %+v", list)
	}

	// The cover.
	errorCode(t, e.json("GET", itemPath(item, "/cover"), e.member.token, nil), 404, "not_found")
	errorCode(t, e.upload("PUT", itemPath(item, "/cover"), e.member.token, "portada.png", pngBytes, nil), 403, "forbidden")
	errorCode(t, e.upload("PUT", itemPath(item, "/cover"), e.biblioteca.token, "portada.txt", []byte("no soy una imagen"), nil), 400, "cover_not_image")
	errorCode(t, e.upload("PUT", itemPath(item, "/cover"), e.biblioteca.token, "", nil, nil), 400, "file_required")
	rec = e.upload("PUT", itemPath(item, "/cover"), e.biblioteca.token, "portada.png", pngBytes, nil)
	status(t, rec, http.StatusOK)
	cover := decode[library.Resource](t, rec)
	if !cover.IsCover {
		t.Errorf("not a cover: %+v", cover)
	}
	rec = e.json("GET", itemPath(item, "/cover"), e.member.token, nil)
	status(t, rec, http.StatusOK)
	if !bytes.Equal(rec.Body.Bytes(), pngBytes) || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("unexpected cover: %v", rec.Header())
	}
	b := decode[library.Item](t, e.json("GET", itemPath(item, ""), e.member.token, nil))
	if !b.HasCover || b.ResourceCount != 2 {
		t.Errorf("the cover is not a resource of the item: %+v", b)
	}
	if list := decode[[]library.Resource](t, e.json("GET", itemPath(item, "/resources"), e.member.token, nil)); len(list) != 2 {
		t.Errorf("the cover is not in the list of files: %+v", list)
	}
	// It cannot be removed as if it were an ordinary file.
	errorCode(t, e.json("DELETE", itemPath(item, fmt.Sprintf("/resources/%d", cover.ID)), e.board.token, nil), 404, "not_found")

	// A new cover takes the place of the old one: one row, one file.
	rec = e.upload("PUT", itemPath(item, "/cover"), e.board.token, "otra.png", pngBytes, nil)
	status(t, rec, http.StatusOK)
	newCover := decode[library.Resource](t, rec)
	if newCover.ID == cover.ID {
		t.Error("a new cover must be a new file: a file is never rewritten")
	}
	if _, err := os.Stat(filepath.Join(e.root, fmt.Sprint(item.ID), fmt.Sprint(cover.ID))); !os.IsNotExist(err) {
		t.Errorf("the bytes of the old cover are still there (%v)", err)
	}
	var covers int
	if err := e.tx.QueryRow(context.Background(), `SELECT count(*) FROM library_resources WHERE item_id = $1 AND is_cover`, item.ID).Scan(&covers); err != nil || covers != 1 {
		t.Errorf("an item has one cover: %d (%v)", covers, err)
	}
	errorCode(t, e.json("DELETE", itemPath(item, "/cover"), e.member.token, nil), 403, "forbidden")
	status(t, e.json("DELETE", itemPath(item, "/cover"), e.biblioteca.token, nil), http.StatusNoContent)
	errorCode(t, e.json("DELETE", itemPath(item, "/cover"), e.biblioteca.token, nil), 404, "not_found")

	// Removing a file: whoever attached it, and admin, board and biblioteca.
	del := func(p person, r library.Resource) *httptest.ResponseRecorder {
		return e.json("DELETE", itemPath(item, fmt.Sprintf("/resources/%d", r.ID)), p.token, nil)
	}
	errorCode(t, del(e.member, notes), 403, "not_yours")
	errorCode(t, del(e.material, topo), 403, "not_yours")
	status(t, del(e.member, topo), http.StatusNoContent)
	status(t, del(e.admin, notes), http.StatusNoContent)
	if _, err := os.Stat(filepath.Join(e.root, fmt.Sprint(item.ID), fmt.Sprint(topo.ID))); !os.IsNotExist(err) {
		t.Errorf("the bytes of a removed file are still there (%v)", err)
	}
	errorCode(t, del(e.admin, notes), 404, "not_found")
}

// Deleting an item takes its comments and its files, bytes included.
func TestDeletingAItemTakesItsComplements(t *testing.T) {
	e := newEnv(t)
	item := e.newItem("Efímero")
	status(t, e.json("POST", itemPath(item, "/comments"), e.member.token, map[string]any{"body": "hola"}), http.StatusCreated)
	rec := e.upload("POST", itemPath(item, "/resources"), e.member.token, "a.txt", []byte("a"), nil)
	status(t, rec, http.StatusCreated)
	file := decode[library.Resource](t, rec)
	status(t, e.upload("PUT", itemPath(item, "/cover"), e.biblioteca.token, "c.png", pngBytes, nil), http.StatusOK)

	status(t, e.json("DELETE", itemPath(item, ""), e.biblioteca.token, nil), http.StatusNoContent)
	for _, table := range []string{"library_comments", "library_resources"} {
		var left int
		if err := e.tx.QueryRow(context.Background(), `SELECT count(*) FROM `+table+` WHERE item_id = $1`, item.ID).Scan(&left); err != nil || left != 0 {
			t.Errorf("%s still has rows of the item: %d (%v)", table, left, err)
		}
	}
	if _, err := os.Stat(filepath.Join(e.root, fmt.Sprint(item.ID), fmt.Sprint(file.ID))); !os.IsNotExist(err) {
		t.Errorf("the bytes of the item's file are still there (%v)", err)
	}
}

// Whoever added a comment, a file or a loan cannot be deleted from the intranet until those are gone.
func TestAPersonWithItemsActivityCannotBeDeletedBlindly(t *testing.T) {
	e := newEnv(t)
	item := e.newItem("Rastro")
	status(t, e.json("POST", itemPath(item, "/comments"), e.member.token, map[string]any{"body": "estuve aquí"}), http.StatusCreated)
	repo := users.NewRepository(e.tx)
	if err := repo.Delete(context.Background(), e.member.user.ID); err == nil {
		t.Error("expected a person with a comment not to be deleted without more")
	}
}

// The types: seeded, listed with how many items have each, added, renamed and deleted by whoever
// manages the library; an item needs one that exists; names are unique ignoring case.
func TestTypes(t *testing.T) {
	e := newEnv(t)
	types := decode[[]library.Type](t, e.json("GET", "/api/v1/library/types", e.member.token, nil))
	if len(types) < 5 || types[0].Name != "Libro" || types[0].ID != e.libro {
		t.Fatalf("expected the seeded types, Libro first: %+v", types)
	}

	rec := e.json("POST", "/api/v1/library/types", e.biblioteca.token, map[string]any{"name": "  Folleto  "})
	status(t, rec, http.StatusCreated)
	folleto := decode[library.Type](t, rec)
	if folleto.Name != "Folleto" || folleto.ItemCount != 0 {
		t.Errorf("unexpected type: %+v", folleto)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha añadido el tipo «Folleto»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	errorCode(t, e.json("POST", "/api/v1/library/types", e.board.token, map[string]any{"name": "folleto"}), 409, "duplicate_library_type")
	errorCode(t, e.json("POST", "/api/v1/library/types", e.board.token, map[string]any{"name": "  "}), 400, "type_name_required")
	errorCode(t, e.json("POST", "/api/v1/library/types", e.board.token, map[string]any{"name": strings.Repeat("a", library.MaxTypeNameLength+1)}), 400, "type_name_too_long")

	path := fmt.Sprintf("/api/v1/library/types/%d", folleto.ID)
	status(t, e.json("PUT", path, e.board.token, map[string]any{"name": "FOLLETO"}), http.StatusOK) // only the case changes
	errorCode(t, e.json("PUT", path, e.board.token, map[string]any{"name": "mapa"}), 409, "duplicate_library_type")
	errorCode(t, e.json("PUT", "/api/v1/library/types/987654", e.board.token, map[string]any{"name": "X"}), 404, "not_found")

	// An item takes one that exists.
	create := func(body map[string]any) *httptest.ResponseRecorder {
		return e.json("POST", "/api/v1/library/items", e.biblioteca.token, body)
	}
	errorCode(t, create(map[string]any{"title": "Sin tipo"}), 400, "type_required")
	errorCode(t, create(map[string]any{"title": "Raro", "type_id": 987654, "acquired_on": "2020-01-01"}), 400, "unknown_library_type")
	rec = create(map[string]any{"title": "Guía", "type_id": folleto.ID, "acquired_on": "2020-01-01"})
	status(t, rec, http.StatusCreated)
	item := decode[library.Item](t, rec)
	if item.TypeID != folleto.ID || item.Type != "FOLLETO" {
		t.Errorf("the item should carry its type: %+v", item)
	}
	errorCode(t, e.json("PUT", itemPath(item, ""), e.board.token, map[string]any{"title": "Guía"}), 400, "type_required")
	rec = e.json("PUT", itemPath(item, ""), e.board.token, map[string]any{"title": "Guía", "type_id": e.libro, "acquired_on": "2020-01-01"})
	status(t, rec, http.StatusOK)
	if got := decode[library.Item](t, rec); got.Type != "Libro" {
		t.Errorf("expected the type to change: %+v", got)
	}

	// A type in use cannot be deleted; one that is free can.
	for _, ty := range decode[[]library.Type](t, e.json("GET", "/api/v1/library/types", e.member.token, nil)) {
		if ty.ID == e.libro && ty.ItemCount != 1 {
			t.Errorf("expected Libro to have 1 item, got %d", ty.ItemCount)
		}
	}
	errorCode(t, e.json("DELETE", fmt.Sprintf("/api/v1/library/types/%d", e.libro), e.board.token, nil), 409, "library_type_in_use")
	status(t, e.json("DELETE", path, e.board.token, nil), http.StatusNoContent)
	errorCode(t, e.json("DELETE", path, e.board.token, nil), 404, "not_found")
}

// The dates of an item: when it was acquired (required) and, together, when and why it left the
// library for good (lost, withdrawn or sold). An item that has left is not available and cannot be
// lent; one that is out cannot leave; the dates agree with the loans.
func TestItemDates(t *testing.T) {
	e := newEnv(t)
	body := func(extra map[string]any) map[string]any {
		m := map[string]any{"type_id": e.libro, "title": "Topo", "acquired_on": "2020-01-01"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	create := func(m map[string]any) *httptest.ResponseRecorder {
		return e.json("POST", "/api/v1/library/items", e.biblioteca.token, m)
	}
	tomorrow := time.Now().AddDate(0, 0, 2).Format("2006-01-02")

	// What is refused.
	errorCode(t, create(map[string]any{"type_id": e.libro, "title": "Topo"}), 400, "acquired_on_required")
	errorCode(t, create(body(map[string]any{"acquired_on": "ayer"})), 400, "invalid_date")
	errorCode(t, create(body(map[string]any{"acquired_on": tomorrow})), 400, "invalid_item_dates")
	errorCode(t, create(body(map[string]any{"ended_on": "2021-01-01"})), 400, "end_incomplete")
	errorCode(t, create(body(map[string]any{"end_reason": "sold"})), 400, "end_incomplete")
	errorCode(t, create(body(map[string]any{"ended_on": "2021-01-01", "end_reason": "burnt"})), 400, "invalid_end_reason")
	errorCode(t, create(body(map[string]any{"ended_on": "2019-12-31", "end_reason": "lost"})), 400, "invalid_item_dates")
	errorCode(t, create(body(map[string]any{"ended_on": tomorrow, "end_reason": "lost"})), 400, "invalid_item_dates")

	// An item that has left is created or edited into being unavailable, with its date and reason.
	rec := create(body(nil))
	status(t, rec, http.StatusCreated)
	item := decode[library.Item](t, rec)
	if item.AcquiredOn != "2020-01-01" || item.EndedOn != nil || item.EndReason != nil || !item.Available {
		t.Errorf("unexpected item: %+v", item)
	}
	rec = e.json("PUT", itemPath(item, ""), e.board.token, body(map[string]any{"ended_on": " 2021-05-01 ", "end_reason": "sold"}))
	status(t, rec, http.StatusOK)
	item = decode[library.Item](t, rec)
	if item.Available || item.EndedOn == nil || *item.EndedOn != "2021-05-01" || *item.EndReason != "sold" {
		t.Errorf("an item that has left should say so and not be available: %+v", item)
	}
	e.notifier.wait(t) // the creation
	if msg := e.notifier.wait(t); !strings.Contains(msg, "EndReason") && !strings.Contains(msg, "sold") {
		t.Errorf("the edit notice should say it has left: %q", msg)
	}

	// It cannot be lent while it is gone; taking the end date away brings it back.
	borrower := e.person("member")
	lend := func(extra map[string]any) *httptest.ResponseRecorder {
		m := map[string]any{"login": borrower.user.Login}
		for k, v := range extra {
			m[k] = v
		}
		return e.json("POST", itemPath(item, "/loans"), e.material.token, m)
	}
	errorCode(t, lend(nil), 409, "item_ended")
	rec = e.json("PUT", itemPath(item, ""), e.board.token, body(nil))
	status(t, rec, http.StatusOK)
	if got := decode[library.Item](t, rec); !got.Available || got.EndedOn != nil || got.EndReason != nil {
		t.Errorf("without the end date it should be available again: %+v", got)
	}

	// A loan cannot start before the item was acquired.
	errorCode(t, lend(map[string]any{"lent_at": "2019-12-31"}), 400, "invalid_dates")

	// While it is out it cannot leave; the dates agree with the loans.
	rec = lend(map[string]any{"lent_at": "2021-01-10"})
	status(t, rec, http.StatusCreated)
	loan := decode[library.Loan](t, rec)
	leave := body(map[string]any{"ended_on": "2021-06-01", "end_reason": "lost"})
	errorCode(t, e.json("PUT", itemPath(item, ""), e.board.token, leave), 409, "item_on_loan")
	errorCode(t, e.json("PUT", itemPath(item, ""), e.board.token, body(map[string]any{"acquired_on": "2021-02-01"})), 400, "invalid_item_dates")
	status(t, e.json("POST", itemPath(item, fmt.Sprintf("/loans/%d/return", loan.ID)), e.material.token, map[string]any{"returned_at": "2021-01-20"}), http.StatusOK)
	errorCode(t, e.json("PUT", itemPath(item, ""), e.board.token, body(map[string]any{"ended_on": "2021-01-15", "end_reason": "lost"})), 400, "invalid_item_dates")
	rec = e.json("PUT", itemPath(item, ""), e.board.token, leave)
	status(t, rec, http.StatusOK)
	if got := decode[library.Item](t, rec); got.Available || *got.EndReason != "lost" {
		t.Errorf("expected it to have left: %+v", got)
	}
	// Its loan history stays, and it is not in the list of what is out.
	if loans := decode[[]library.Loan](t, e.json("GET", itemPath(item, "/loans"), e.material.token, nil)); len(loans) != 1 {
		t.Errorf("the history of loans should stay: %+v", loans)
	}
	if open := decode[[]library.Loan](t, e.json("GET", "/api/v1/library/loans", e.material.token, nil)); len(open) != 0 {
		t.Errorf("nothing should be out: %+v", open)
	}
}
