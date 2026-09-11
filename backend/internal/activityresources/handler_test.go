package activityresources_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activities"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activityresources"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

// fakeNotifier stands in for httpapi.MutationNotifier so tests can
// check what a mutation notification actually said, without a real
// Telegram call.
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

// waitForMessage polls briefly for NotifyAsync's own goroutine to have
// delivered something, rather than asserting immediately after the
// request returns (the notification is fire-and-forget, so it can
// still be in flight by then). It pops the oldest undelivered message
// rather than just peeking at it, so a test that triggers more than
// one notification can call this once per notification, in order.
func (f *fakeNotifier) waitForMessage(t *testing.T) string {
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
	t.Fatal("timed out waiting for a mutation notification")
	return ""
}

// stubActivities is the minimal activityresources.ActivityLookup a
// handler test needs — a single fixed activity (id, leader) and a fixed
// participant set, no Postgres involved.
type stubActivities struct {
	activity     activities.Activity
	notFound     bool
	participants map[int64]bool
}

func (s stubActivities) Get(_ context.Context, id int64) (activities.Activity, error) {
	if s.notFound || id != s.activity.ID {
		return activities.Activity{}, activities.ErrNotFound
	}
	return s.activity, nil
}

func (s stubActivities) IsParticipant(_ context.Context, _ int64, userID int64) (bool, error) {
	return s.participants[userID], nil
}

// stubUsers is the minimal activityresources.UserLookup a handler test
// needs.
type stubUsers struct {
	loginsByID map[int64]string
}

func (s stubUsers) LoginForUserID(_ context.Context, id int64) (string, error) {
	login, ok := s.loginsByID[id]
	if !ok {
		return "", errors.New("not found")
	}
	return login, nil
}

// testPeople holds the real (rolled-back-on-cleanup) user rows a
// handler test needs — author_id has a foreign key against users, so
// these can't be fabricated ids the way stubActivities/stubUsers'
// *behavior* can be.
type testPeople struct {
	leader, participant, stranger, admin testutil.TestUser
}

func newTestServer(t *testing.T) (*http.ServeMux, testutil.FakeResolver, int64, testPeople) {
	t.Helper()
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	storage, err := activityresources.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	people := testPeople{
		leader:      testutil.InsertUser(t, tx, ""),
		participant: testutil.InsertUser(t, tx, ""),
		stranger:    testutil.InsertUser(t, tx, ""),
		admin:       testutil.InsertUser(t, tx, ""),
	}
	activityID := testutil.InsertActivity(t, tx)
	service := activityresources.NewService(repo, storage)
	authenticator, resolver := testutil.NewTestAuthenticator()

	stubAct := stubActivities{
		activity:     activities.Activity{ID: activityID, LeaderID: people.leader.ID},
		participants: map[int64]bool{people.participant.ID: true},
	}
	stubU := stubUsers{loginsByID: map[int64]string{
		people.leader.ID: people.leader.Login, people.participant.ID: people.participant.Login,
		people.stranger.ID: people.stranger.Login, people.admin.ID: people.admin.Login,
	}}

	mux := http.NewServeMux()
	activityresources.NewHandler(service, authenticator, stubAct, stubU, nil).RegisterRoutes(mux)
	return mux, resolver, activityID, people
}

// newTestServerWithNotifier is newTestServer, but also wires a
// fakeNotifier so tests can assert on mutation-notification content,
// and gives the stub activity a real Title (newTestServer leaves it
// blank, since nothing but the notifications actually reads it).
func newTestServerWithNotifier(t *testing.T) (*http.ServeMux, testutil.FakeResolver, int64, testPeople, *fakeNotifier) {
	t.Helper()
	tx := testutil.NewTx(t)
	repo := activityresources.NewRepository(tx)
	storage, err := activityresources.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	people := testPeople{
		leader:      testutil.InsertUser(t, tx, ""),
		participant: testutil.InsertUser(t, tx, ""),
		stranger:    testutil.InsertUser(t, tx, ""),
		admin:       testutil.InsertUser(t, tx, ""),
	}
	activityID := testutil.InsertActivity(t, tx)
	service := activityresources.NewService(repo, storage)
	authenticator, resolver := testutil.NewTestAuthenticator()

	stubAct := stubActivities{
		activity:     activities.Activity{ID: activityID, Title: "Sima del Viento", LeaderID: people.leader.ID},
		participants: map[int64]bool{people.participant.ID: true},
	}
	stubU := stubUsers{loginsByID: map[int64]string{
		people.leader.ID: people.leader.Login, people.participant.ID: people.participant.Login,
		people.stranger.ID: people.stranger.Login, people.admin.ID: people.admin.Login,
	}}
	notifier := &fakeNotifier{}

	mux := http.NewServeMux()
	activityresources.NewHandler(service, authenticator, stubAct, stubU, notifier).RegisterRoutes(mux)
	return mux, resolver, activityID, people, notifier
}

func doRequest(mux *http.ServeMux, method, path, token string, body []byte, contentType string) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func doJSON(mux *http.ServeMux, method, path, token string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	return doRequest(mux, method, path, token, b, "application/json")
}

// multipartBody builds a multipart/form-data body from plain fields
// plus, optionally, one file part.
func multipartBody(t *testing.T, fields map[string]string, fileField, fileName string, fileContent []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if fileField != "" {
		part, err := writer.CreateFormFile(fileField, fileName)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(fileContent); err != nil {
			t.Fatalf("write file content: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return body, writer.FormDataContentType()
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func createTextResource(t *testing.T, mux *http.ServeMux, activityID int64, token, title, description string) activityresources.Resource {
	t.Helper()
	body, contentType := multipartBody(t, map[string]string{"type": "text", "title": title, "description": description}, "", "", nil)
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/activities/%d/resources", activityID), token, body.Bytes(), contentType)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create text resource: %d: %s", rec.Code, rec.Body.String())
	}
	var res activityresources.Resource
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode resource: %v", err)
	}
	return res
}

func TestHandlerListRequiresAuthentication(t *testing.T) {
	mux, _, activityID, _ := newTestServer(t)
	rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/activities/%d/resources", activityID), "", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandlerListOpenToAnyBaselineRole(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	for _, role := range []string{"admin", "board", "member"} {
		token := testutil.TokenFor(authn, people.stranger.ID, role)
		rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/activities/%d/resources", activityID), token, nil, "")
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 for role %q, got %d: %s", role, rec.Code, rec.Body.String())
		}
	}
}

func TestHandlerCreateTextResourceAsParticipant(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	res := createTextResource(t, mux, activityID, token, "Crónica", "Un gran día bajo tierra.")
	if res.AuthorLogin != people.participant.Login {
		t.Errorf("expected author_login %q, got %q", people.participant.Login, res.AuthorLogin)
	}
	if res.Position != 1 {
		t.Errorf("expected position 1, got %d", res.Position)
	}
}

func TestHandlerCreateAllowedForOrganizerEvenWithoutAParticipantRow(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	// people.leader isn't in the stub's participants map — only the
	// fact that Activity.LeaderID matches should let them through.
	token := testutil.TokenFor(authn, people.leader.ID, "member")
	createTextResource(t, mux, activityID, token, "Crónica", "texto")
}

func TestHandlerCreateAllowedForAdminBoard(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	for _, role := range []string{"admin", "board"} {
		token := testutil.TokenFor(authn, people.admin.ID, role)
		createTextResource(t, mux, activityID, token, "Crónica "+role, "texto")
	}
}

func TestHandlerCreateRejectsNonParticipant(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.stranger.ID, "member")
	body, contentType := multipartBody(t, map[string]string{"type": "text", "title": "T", "description": "d"}, "", "", nil)
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/activities/%d/resources", activityID), token, body.Bytes(), contentType)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCreateUnknownActivityIsNotFound(t *testing.T) {
	mux, authn, _, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.admin.ID, "admin")
	body, contentType := multipartBody(t, map[string]string{"type": "text", "title": "T", "description": "d"}, "", "", nil)
	rec := doRequest(mux, http.MethodPost, "/api/v1/activities/999999/resources", token, body.Bytes(), contentType)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCreateTextRequiresDescription(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	body, contentType := multipartBody(t, map[string]string{"type": "text", "title": "T"}, "", "", nil)
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/activities/%d/resources", activityID), token, body.Bytes(), contentType)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "description_required" {
		t.Errorf("expected description_required, got %q", code)
	}
}

func TestHandlerCreateFileResourceAndDownloadIt(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")

	body, contentType := multipartBody(t, map[string]string{"type": "file", "title": "Foto de la sima"}, "file", "foto.jpg", []byte("fake jpeg bytes"))
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/activities/%d/resources", activityID), token, body.Bytes(), contentType)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create file resource: %d: %s", rec.Code, rec.Body.String())
	}
	var res activityresources.Resource
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.FileName == nil || *res.FileName != "foto.jpg" {
		t.Errorf("expected file_name foto.jpg, got %+v", res.FileName)
	}

	filePath := fmt.Sprintf("/api/v1/activities/%d/resources/%d/file", activityID, res.ID)
	fileRec := doRequest(mux, http.MethodGet, filePath, token, nil, "")
	if fileRec.Code != http.StatusOK {
		t.Fatalf("get file: %d: %s", fileRec.Code, fileRec.Body.String())
	}
	if fileRec.Body.String() != "fake jpeg bytes" {
		t.Errorf("expected the raw file content, got %q", fileRec.Body.String())
	}
	if got := fileRec.Header().Get("Content-Disposition"); got != `inline; filename="foto.jpg"` {
		t.Errorf("expected inline disposition, got %q", got)
	}

	downloadRec := doRequest(mux, http.MethodGet, filePath+"?download=1", token, nil, "")
	if got := downloadRec.Header().Get("Content-Disposition"); got != `attachment; filename="foto.jpg"` {
		t.Errorf("expected attachment disposition, got %q", got)
	}
}

// TestHandlerCreateFileResourceCleansUpMultipartTempFile covers the fix
// for a previously leaked temp file: net/http's own ParseMultipartForm
// spills anything past its 4 MiB in-memory threshold to a temp file
// (mime/multipart's own "multipart-" prefix, in os.TempDir()) that
// nothing removed before create started deferring
// r.MultipartForm.RemoveAll().
func TestHandlerCreateFileResourceCleansUpMultipartTempFile(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")

	before, err := filepath.Glob(filepath.Join(os.TempDir(), "multipart-*"))
	if err != nil {
		t.Fatalf("glob before: %v", err)
	}

	// Past ParseMultipartForm's own 4 MiB in-memory threshold, well
	// under this route's 25 MiB cap (maxUploadSize).
	content := bytes.Repeat([]byte("x"), 5<<20)
	body, contentType := multipartBody(t, map[string]string{"type": "file", "title": "Vídeo"}, "file", "video.mp4", content)
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/activities/%d/resources", activityID), token, body.Bytes(), contentType)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create file resource: %d: %s", rec.Code, rec.Body.String())
	}

	after, err := filepath.Glob(filepath.Join(os.TempDir(), "multipart-*"))
	if err != nil {
		t.Fatalf("glob after: %v", err)
	}
	if len(after) > len(before) {
		t.Errorf("expected no leftover multipart temp file, had %d before the upload and %d after", len(before), len(after))
	}
}

func TestHandlerFileRejectsTextResource(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	res := createTextResource(t, mux, activityID, token, "Crónica", "texto")

	rec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/activities/%d/resources/%d/file", activityID, res.ID), token, nil, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	if code := errorCode(t, rec); code != "not_a_file" {
		t.Errorf("expected not_a_file, got %q", code)
	}
}

func TestHandlerUpdateAllowedForAuthor(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	res := createTextResource(t, mux, activityID, token, "Original", "original")

	newTitle := "Updated"
	rec := doJSON(mux, http.MethodPatch, fmt.Sprintf("/api/v1/activities/%d/resources/%d", activityID, res.ID), token,
		map[string]any{"title": newTitle})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d: %s", rec.Code, rec.Body.String())
	}
	var updated activityresources.Resource
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Title != newTitle {
		t.Errorf("expected title %q, got %q", newTitle, updated.Title)
	}
}

func TestHandlerUpdateRejectsNonAuthor(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	authorToken := testutil.TokenFor(authn, people.participant.ID, "member")
	res := createTextResource(t, mux, activityID, authorToken, "Original", "original")

	// The organizer took part too, but didn't author this one.
	otherToken := testutil.TokenFor(authn, people.leader.ID, "member")
	rec := doJSON(mux, http.MethodPatch, fmt.Sprintf("/api/v1/activities/%d/resources/%d", activityID, res.ID), otherToken,
		map[string]any{"title": "hijacked"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerUpdateAllowedForAdminBoardEvenNotAuthor(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	authorToken := testutil.TokenFor(authn, people.participant.ID, "member")
	res := createTextResource(t, mux, activityID, authorToken, "Original", "original")

	adminToken := testutil.TokenFor(authn, people.admin.ID, "admin")
	rec := doJSON(mux, http.MethodPatch, fmt.Sprintf("/api/v1/activities/%d/resources/%d", activityID, res.ID), adminToken,
		map[string]any{"title": "corrected by admin"})
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerUpdateRejectsDescriptionOnFileResource(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	body, contentType := multipartBody(t, map[string]string{"type": "file", "title": "F"}, "file", "f.pdf", []byte("x"))
	rec := doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/activities/%d/resources", activityID), token, body.Bytes(), contentType)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create file: %d: %s", rec.Code, rec.Body.String())
	}
	var res activityresources.Resource
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}

	updateRec := doJSON(mux, http.MethodPatch, fmt.Sprintf("/api/v1/activities/%d/resources/%d", activityID, res.ID), token,
		map[string]any{"description": "not allowed"})
	if updateRec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", updateRec.Code, updateRec.Body.String())
	}
	if code := errorCode(t, updateRec); code != "description_not_allowed" {
		t.Errorf("expected description_not_allowed, got %q", code)
	}
}

func TestHandlerDeleteAllowedForAuthorOnly(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	authorToken := testutil.TokenFor(authn, people.participant.ID, "member")
	res := createTextResource(t, mux, activityID, authorToken, "T", "d")

	otherToken := testutil.TokenFor(authn, people.leader.ID, "member")
	if rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/activities/%d/resources/%d", activityID, res.ID), otherToken, nil, ""); rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for a non-author, got %d", rec.Code)
	}
	if rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/activities/%d/resources/%d", activityID, res.ID), authorToken, nil, ""); rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 for the author, got %d: %s", rec.Code, rec.Body.String())
	}

	getRec := doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/activities/%d/resources/%d", activityID, res.ID), authorToken, nil, "")
	if getRec.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", getRec.Code)
	}
}

func TestHandlerReorder(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	first := createTextResource(t, mux, activityID, token, "First", "d")
	second := createTextResource(t, mux, activityID, token, "Second", "d")

	rec := doJSON(mux, http.MethodPut, fmt.Sprintf("/api/v1/activities/%d/resources/order", activityID), token,
		map[string]any{"resource_ids": []int64{second.ID, first.ID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder: %d: %s", rec.Code, rec.Body.String())
	}
	var list []activityresources.Resource
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("expected reversed order, got %+v", list)
	}
}

func TestHandlerReorderRejectsMismatch(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	first := createTextResource(t, mux, activityID, token, "First", "d")

	rec := doJSON(mux, http.MethodPut, fmt.Sprintf("/api/v1/activities/%d/resources/order", activityID), token,
		map[string]any{"resource_ids": []int64{first.ID, 999999}})
	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "reorder_mismatch" {
		t.Errorf("expected reorder_mismatch, got %q", code)
	}
}

func TestHandlerReorderRejectsNonParticipant(t *testing.T) {
	mux, authn, activityID, people := newTestServer(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	first := createTextResource(t, mux, activityID, token, "First", "d")

	strangerToken := testutil.TokenFor(authn, people.stranger.ID, "member")
	rec := doJSON(mux, http.MethodPut, fmt.Sprintf("/api/v1/activities/%d/resources/order", activityID), strangerToken,
		map[string]any{"resource_ids": []int64{first.ID}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerCreateNotifiesTelegramWithActorActivityAndTitle(t *testing.T) {
	mux, authn, activityID, people, notifier := newTestServerWithNotifier(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")

	createTextResource(t, mux, activityID, token, "Fotos de la sima", "d")

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, people.participant.Login) {
		t.Errorf("expected the notification to name the actor, got %q", msg)
	}
	if !strings.Contains(msg, "Sima del Viento") || !strings.Contains(msg, "Fotos de la sima") {
		t.Errorf("expected the notification to name the activity and the resource, got %q", msg)
	}
}

// TestHandlerUpdateNotifiesTelegramWithFieldDiff is the core of the
// feature this whole notifier exists for: seeing exactly what changed,
// not just that something did.
func TestHandlerUpdateNotifiesTelegramWithFieldDiff(t *testing.T) {
	mux, authn, activityID, people, notifier := newTestServerWithNotifier(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	res := createTextResource(t, mux, activityID, token, "Original", "d")
	notifier.waitForMessage(t) // drain the create notification first

	rec := doJSON(mux, http.MethodPatch, fmt.Sprintf("/api/v1/activities/%d/resources/%d", activityID, res.ID), token,
		map[string]any{"title": "Actualizado"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, people.participant.Login) {
		t.Errorf("expected the notification to name the actor, got %q", msg)
	}
	if !strings.Contains(msg, "Title: Original → Actualizado") {
		t.Errorf("expected the notification to show the title change, got %q", msg)
	}
}

// TestHandlerUpdateWithNoActualChangeSendsNoNotification makes sure a
// PATCH that doesn't actually change anything doesn't produce a
// misleading "edited" message with an empty diff.
func TestHandlerUpdateWithNoActualChangeSendsNoNotification(t *testing.T) {
	mux, authn, activityID, people, notifier := newTestServerWithNotifier(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	res := createTextResource(t, mux, activityID, token, "Original", "d")
	notifier.waitForMessage(t) // drain the create notification first

	rec := doJSON(mux, http.MethodPatch, fmt.Sprintf("/api/v1/activities/%d/resources/%d", activityID, res.ID), token,
		map[string]any{"title": "Original"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d: %s", rec.Code, rec.Body.String())
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if len(notifier.sent) != 0 {
		t.Errorf("expected no notification for a no-op update, got %v", notifier.sent)
	}
}

func TestHandlerDeleteNotifiesTelegramWithActorActivityAndTitle(t *testing.T) {
	mux, authn, activityID, people, notifier := newTestServerWithNotifier(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	res := createTextResource(t, mux, activityID, token, "Para borrar", "d")
	notifier.waitForMessage(t) // drain the create notification first

	rec := doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/activities/%d/resources/%d", activityID, res.ID), token, nil, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, people.participant.Login) {
		t.Errorf("expected the notification to name the actor, got %q", msg)
	}
	if !strings.Contains(msg, "Sima del Viento") || !strings.Contains(msg, "Para borrar") {
		t.Errorf("expected the notification to name the activity and the resource, got %q", msg)
	}
}

func TestHandlerReorderNotifiesTelegram(t *testing.T) {
	mux, authn, activityID, people, notifier := newTestServerWithNotifier(t)
	token := testutil.TokenFor(authn, people.participant.ID, "member")
	first := createTextResource(t, mux, activityID, token, "First", "d")
	notifier.waitForMessage(t) // drain the first create notification
	second := createTextResource(t, mux, activityID, token, "Second", "d")
	notifier.waitForMessage(t) // drain the second create notification

	rec := doJSON(mux, http.MethodPut, fmt.Sprintf("/api/v1/activities/%d/resources/order", activityID), token,
		map[string]any{"resource_ids": []int64{second.ID, first.ID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder: %d: %s", rec.Code, rec.Body.String())
	}

	msg := notifier.waitForMessage(t)
	if !strings.Contains(msg, people.participant.Login) || !strings.Contains(msg, "Sima del Viento") {
		t.Errorf("expected the notification to name the actor and activity, got %q", msg)
	}
}
