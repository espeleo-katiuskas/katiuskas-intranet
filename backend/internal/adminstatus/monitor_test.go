package adminstatus

import (
	"context"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

type recorder struct {
	mu   sync.Mutex
	sent []string
}

func (r *recorder) Send(_ context.Context, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, text)
	return nil
}

func newMonitor(t *testing.T, info Info) (*Monitor, *recorder, *time.Time) {
	t.Helper()
	authenticator, _ := testutil.NewTestAuthenticator()
	handler := NewHandler(testutil.NewTx(t), authenticator, info)
	rec := &recorder{}
	monitor := NewMonitor(handler, rec)
	clock := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	monitor.now = func() time.Time { return clock }
	return monitor, rec, &clock
}

func TestMonitorSaysNothingWhileEverythingIsFine(t *testing.T) {
	dir := t.TempDir()
	monitor, rec, _ := newMonitor(t, Info{ArchiveRoot: dir, UserFilesRoot: dir, ActivityResourcesRoot: dir, TransactionFilesRoot: dir, LibraryFilesRoot: dir})
	monitor.Check(context.Background())
	monitor.Check(context.Background())
	if len(rec.sent) != 0 {
		t.Errorf("expected no notice, got %q", rec.sent)
	}
}

func TestMonitorReportsAProblemOnceAndWhenItIsGone(t *testing.T) {
	dir := t.TempDir()
	missing := dir + "/missing"
	monitor, rec, clock := newMonitor(t, Info{
		PublicBaseURL: "https://intranet.example.test",
		ArchiveRoot:   dir, UserFilesRoot: missing, ActivityResourcesRoot: dir, TransactionFilesRoot: dir, LibraryFilesRoot: dir,
	})
	ctx := context.Background()

	monitor.Check(ctx)
	monitor.Check(ctx)
	*clock = clock.Add(time.Hour)
	monitor.Check(ctx)
	if len(rec.sent) != 1 {
		t.Fatalf("expected one notice for a problem that stays, got %q", rec.sent)
	}
	if !strings.Contains(rec.sent[0], "intranet.example.test") || !strings.Contains(rec.sent[0], missing) ||
		!strings.Contains(rec.sent[0], "ficheros de las personas") {
		t.Errorf("the notice should say where and what, got %q", rec.sent[0])
	}

	if err := os.Mkdir(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	monitor.Check(ctx)
	if len(rec.sent) != 2 || !strings.Contains(rec.sent[1], "ya no hay ningún problema") {
		t.Errorf("expected a notice that the problem is gone, got %q", rec.sent)
	}
	monitor.Check(ctx)
	if len(rec.sent) != 2 {
		t.Errorf("expected nothing more once it is solved, got %q", rec.sent)
	}
}

func TestMonitorRemindsOnceADayAndReportsNewProblems(t *testing.T) {
	dir := t.TempDir()
	monitor, rec, clock := newMonitor(t, Info{
		ArchiveRoot: dir + "/a", UserFilesRoot: dir, ActivityResourcesRoot: dir, TransactionFilesRoot: dir, LibraryFilesRoot: dir,
	})
	ctx := context.Background()
	monitor.Check(ctx)

	*clock = clock.Add(monitorReminder - time.Minute)
	monitor.Check(ctx)
	if len(rec.sent) != 1 {
		t.Fatalf("expected no reminder yet, got %q", rec.sent)
	}
	*clock = clock.Add(2 * time.Minute)
	monitor.Check(ctx)
	if len(rec.sent) != 2 {
		t.Fatalf("expected a reminder after a day, got %q", rec.sent)
	}

	monitor.handler.info.ActivityResourcesRoot = dir + "/b"
	monitor.Check(ctx)
	if len(rec.sent) != 3 || !strings.Contains(rec.sent[2], "memorias") || !strings.Contains(rec.sent[2], "archivo") {
		t.Errorf("expected a new notice when another problem appears, listing both, got %q", rec.sent)
	}
}

func TestProblemsOfDatabaseAndDisk(t *testing.T) {
	s := statusResponse{
		Database: databaseStatus{Error: "connection refused"},
		Disks: []diskStatus{
			{Paths: []string{"/data/archive"}, FreeBytes: 3 << 30, UsedPercent: 91},
			{Paths: []string{"/other"}, UsedPercent: 40},
		},
	}
	got := problemsOf(s, 90)
	if len(got) != 2 || got[0].Key != "database" || got[1].Key != "disk:/data/archive" {
		t.Fatalf("unexpected problems: %+v", got)
	}
	if !strings.Contains(got[0].Text, "connection refused") || !strings.Contains(got[1].Text, "91 %") ||
		!strings.Contains(got[1].Text, "3.0 GiB") {
		t.Errorf("unexpected texts: %+v", got)
	}
	if n := len(problemsOf(s, 0)); n != 1 {
		t.Errorf("expected only the database problem with the disk check off, got %d", n)
	}
	if n := len(problemsOf(s, 92)); n != 1 {
		t.Errorf("expected the disk below the threshold not to count, got %d", n)
	}
}

func TestDiskOfComputesWhatDfShows(t *testing.T) {
	// 100 blocks of 4 KiB, 10 free, of which 5 are available to a normal user.
	d := diskOf("/x", &syscall.Statfs_t{Bsize: 4096, Blocks: 100, Bfree: 10, Bavail: 5})
	if d.UsedPercent != 95 || d.FreeBytes != 5*4096 || d.TotalBytes != 100*4096 {
		t.Errorf("unexpected disk: %+v", d)
	}
}

func TestDisksOfGroupsDirectoriesOnTheSameFilesystem(t *testing.T) {
	dir := t.TempDir()
	disks := disksOf([]storageStatus{
		{Name: "archive", Path: dir, Exists: true},
		{Name: "user_files", Path: dir, Exists: true},
		{Name: "missing", Path: dir + "/nope", Exists: false},
	})
	if len(disks) != 1 || len(disks[0].Paths) != 2 {
		t.Errorf("expected one filesystem with both directories, got %+v", disks)
	}
}
