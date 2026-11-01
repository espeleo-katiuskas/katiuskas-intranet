package transactionfiles_test

import (
	"bytes"
	"context"
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

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounting"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/licenses"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/memberships"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/roles"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/transactionfiles"
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

type env struct {
	root                string
	tx                  pgx.Tx
	mux                 *http.ServeMux
	notifier            *fakeNotifier
	accounting          *accounting.Repository
	admin, money, board string
	member              string
	transactionID       int64
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
	mux := http.NewServeMux()
	root := t.TempDir()
	storage, err := transactionfiles.NewStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	transactionfiles.NewHandler(transactionfiles.NewService(transactionfiles.NewRepository(tx), storage), authenticator, usersService, notifier).RegisterRoutes(mux)
	admin := testutil.InsertUser(t, tx, "")
	money := testutil.InsertUser(t, tx, "")
	board := testutil.InsertUser(t, tx, "")
	member := testutil.InsertUser(t, tx, "")
	return &env{
		root: root, tx: tx, mux: mux, notifier: notifier, accounting: accounting.NewRepository(tx),
		admin:         testutil.TokenFor(resolver, admin.ID, "admin"),
		money:         testutil.TokenFor(resolver, money.ID, "member", "money"),
		board:         testutil.TokenFor(resolver, board.ID, "board"),
		member:        testutil.TokenFor(resolver, member.ID, "member"),
		transactionID: testutil.InsertTransaction(t, tx),
	}
}

func (e *env) do(req *http.Request, token string) *httptest.ResponseRecorder {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *env) get(path, token string) *httptest.ResponseRecorder {
	return e.do(httptest.NewRequest("GET", path, nil), token)
}

func (e *env) upload(transactionID int64, name string, content []byte, token string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", name)
	part.Write(content)
	w.Close()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/transactions/%d/files", transactionID), &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return e.do(req, token)
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("expected %d, got %d: %s", status, rec.Code, rec.Body.String())
	}
	if code != "" && !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("expected error code %q: %s", code, rec.Body.String())
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

var pdf = []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")

func TestAttachListOpenDelete(t *testing.T) {
	e := newEnv(t)
	base := fmt.Sprintf("/api/v1/transactions/%d/files", e.transactionID)

	rec := e.upload(e.transactionID, "../factura 1.pdf", pdf, e.money)
	expect(t, rec, http.StatusCreated, "")
	f := decode[transactionfiles.File](t, rec)
	if f.FileName != "factura 1.pdf" || f.ContentType != "application/pdf" || f.FileSize != int64(len(pdf)) || f.UploadedBy == nil {
		t.Fatalf("file: %+v", f)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "factura 1.pdf") {
		t.Fatalf("notice: %s", msg)
	}

	for _, token := range []string{e.admin, e.board, e.money} {
		files := decode[[]transactionfiles.File](t, e.get(base, token))
		if len(files) != 1 || files[0].ID != f.ID {
			t.Fatalf("list: %+v", files)
		}
	}

	open := e.get(fmt.Sprintf("%s/%d/file", base, f.ID), e.board)
	expect(t, open, http.StatusOK, "")
	if !bytes.Equal(open.Body.Bytes(), pdf) || open.Header().Get("Content-Type") != "application/pdf" ||
		!strings.HasPrefix(open.Header().Get("Content-Disposition"), "inline") ||
		open.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("open: %v %q", open.Header(), open.Body.String())
	}
	dl := e.get(fmt.Sprintf("%s/%d/file?download=1", base, f.ID), e.admin)
	if !strings.HasPrefix(dl.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %v", dl.Header())
	}

	// The file is of the transaction in the path only.
	other := testutil.InsertTransactionAt(t, e.tx, time.Now().Add(-time.Hour))
	expect(t, e.get(fmt.Sprintf("/api/v1/transactions/%d/files/%d/file", other, f.ID), e.admin), http.StatusNotFound, "not_found")

	expect(t, e.do(httptest.NewRequest("DELETE", fmt.Sprintf("%s/%d", base, f.ID), nil), e.board), http.StatusForbidden, "")
	expect(t, e.do(httptest.NewRequest("DELETE", fmt.Sprintf("%s/%d", base, f.ID), nil), e.money), http.StatusNoContent, "")
	if msg := e.notifier.wait(t); !strings.Contains(msg, "quitado") {
		t.Fatalf("notice: %s", msg)
	}
	expect(t, e.do(httptest.NewRequest("DELETE", fmt.Sprintf("%s/%d", base, f.ID), nil), e.money), http.StatusNotFound, "not_found")
}

func TestPermissionsAndInputs(t *testing.T) {
	e := newEnv(t)
	base := fmt.Sprintf("/api/v1/transactions/%d/files", e.transactionID)
	expect(t, e.get(base, ""), http.StatusUnauthorized, "")
	expect(t, e.get(base, e.member), http.StatusForbidden, "")
	expect(t, e.get("/api/v1/transaction-files", e.member), http.StatusForbidden, "")
	expect(t, e.upload(e.transactionID, "a.pdf", pdf, e.board), http.StatusForbidden, "")
	expect(t, e.upload(e.transactionID, "a.pdf", pdf, e.member), http.StatusForbidden, "")
	expect(t, e.upload(e.transactionID, "empty.pdf", nil, e.admin), http.StatusBadRequest, "file_required")
	expect(t, e.upload(e.transactionID+1000, "a.pdf", pdf, e.admin), http.StatusNotFound, "transaction_not_found")
	expect(t, e.get("/api/v1/transactions/abc/files", e.admin), http.StatusBadRequest, "invalid_id")
	big := make([]byte, transactionfiles.MaxFileSize+1)
	expect(t, e.upload(e.transactionID, "big.pdf", big, e.admin), http.StatusRequestEntityTooLarge, "file_too_large")
}

func TestHtmlIsNeverShownInline(t *testing.T) {
	e := newEnv(t)
	html := []byte("<html><script>alert(1)</script></html>")
	// Even claiming to be a PDF by its name, what is HTML is not shown as a page.
	f := decode[transactionfiles.File](t, e.upload(e.transactionID, "evil.pdf", html, e.admin))
	if transactionfiles.Viewable(f.ContentType) {
		t.Fatalf("type %q should not be viewable", f.ContentType)
	}
	rec := e.get(fmt.Sprintf("/api/v1/transactions/%d/files/%d/file", e.transactionID, f.ID), e.admin)
	if !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("headers: %v", rec.Header())
	}
}

func TestOverviewWithTransactionDetails(t *testing.T) {
	e := newEnv(t)
	second := testutil.InsertTransactionAt(t, e.tx, time.Date(2020, 5, 5, 12, 0, 0, 0, time.UTC))
	expect(t, e.upload(e.transactionID, "new.pdf", pdf, e.admin), http.StatusCreated, "")
	expect(t, e.upload(second, "old1.pdf", pdf, e.admin), http.StatusCreated, "")
	expect(t, e.upload(second, "old2.pdf", pdf, e.admin), http.StatusCreated, "")

	list := decode[[]transactionfiles.Listing](t, e.get("/api/v1/transaction-files", e.board))
	if len(list) != 3 || list[0].FileName != "new.pdf" || list[1].FileName != "old1.pdf" || list[2].FileName != "old2.pdf" {
		t.Fatalf("list: %+v", list)
	}
	tr := list[1].Transaction
	if tr.ID != second || tr.Description != "test transaction" || len(tr.Entries) != 2 || tr.Amount <= 0 ||
		tr.Entries[0].AccountName == "" {
		t.Fatalf("transaction details: %+v", tr)
	}

	byYear := decode[[]transactionfiles.Listing](t, e.get("/api/v1/transaction-files?year=2020", e.admin))
	if len(byYear) != 2 || byYear[0].TransactionID != second {
		t.Fatalf("year filter: %+v", byYear)
	}
	page := decode[[]transactionfiles.Listing](t, e.get("/api/v1/transaction-files?limit=1&offset=2", e.admin))
	if len(page) != 1 || page[0].FileName != "old2.pdf" {
		t.Fatalf("page: %+v", page)
	}
	expect(t, e.get("/api/v1/transaction-files?year=1800", e.admin), http.StatusBadRequest, "invalid_year")
	expect(t, e.get("/api/v1/transaction-files?limit=0", e.admin), http.StatusBadRequest, "invalid_limit")
	expect(t, e.get("/api/v1/transaction-files?offset=-1", e.admin), http.StatusBadRequest, "invalid_offset")
}

func TestAccountingKnowsAboutFiles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tr, err := e.accounting.GetTransaction(ctx, e.transactionID)
	if err != nil || tr.FileCount != 0 {
		t.Fatalf("before: %+v %v", tr, err)
	}
	expect(t, e.upload(e.transactionID, "a.pdf", pdf, e.admin), http.StatusCreated, "")
	expect(t, e.upload(e.transactionID, "b.pdf", pdf, e.admin), http.StatusCreated, "")
	tr, _ = e.accounting.GetTransaction(ctx, e.transactionID)
	if tr.FileCount != 2 {
		t.Fatalf("file_count: %d", tr.FileCount)
	}
	list, err := e.accounting.ListTransactions(ctx, accounting.ListFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range list {
		if x.ID == e.transactionID {
			found = x.FileCount == 2
		}
	}
	if !found {
		t.Fatal("ListTransactions should carry file_count")
	}
	entries, err := e.accounting.ListAccountEntries(ctx, tr.Entries[0].AccountID, accounting.ListFilter{Limit: 10})
	if err != nil || len(entries) == 0 || entries[0].TransactionID != e.transactionID || entries[0].FileCount != 2 {
		t.Fatalf("ledger: %+v %v", entries, err)
	}
	// A transaction with files can not be deleted until they are removed.
	if err := e.accounting.DeleteTransaction(ctx, e.transactionID); err != accounting.ErrTransactionHasFiles {
		t.Fatalf("delete: %v", err)
	}
}

func TestBytesAreOnDiskAndGoWithTheRow(t *testing.T) {
	e := newEnv(t)
	f := decode[transactionfiles.File](t, e.upload(e.transactionID, "a.pdf", pdf, e.admin))
	path := filepath.Join(e.root, fmt.Sprint(e.transactionID), fmt.Sprint(f.ID))
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, pdf) {
		t.Fatalf("on disk: %v %q", err, got)
	}
	url := fmt.Sprintf("/api/v1/transactions/%d/files/%d", e.transactionID, f.ID)
	// A row whose bytes are missing answers 404 instead of failing.
	os.Remove(path)
	expect(t, e.get(url+"/file", e.admin), http.StatusNotFound, "not_found")
	// Deleting still works, and removes the bytes of one that is there.
	g := decode[transactionfiles.File](t, e.upload(e.transactionID, "b.pdf", pdf, e.admin))
	gPath := filepath.Join(e.root, fmt.Sprint(e.transactionID), fmt.Sprint(g.ID))
	expect(t, e.do(httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/transactions/%d/files/%d", e.transactionID, g.ID), nil), e.admin), http.StatusNoContent, "")
	if _, err := os.Stat(gPath); !os.IsNotExist(err) {
		t.Fatalf("the bytes should be gone: %v", err)
	}
	expect(t, e.do(httptest.NewRequest("DELETE", url, nil), e.admin), http.StatusNoContent, "")
}
