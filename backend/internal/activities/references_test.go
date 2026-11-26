package activities_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/activities"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

// recordedReferences is a catalogue that only remembers what it is told.
type recordedReferences struct {
	set  map[string]bool
	fail bool
}

func (r *recordedReferences) Refer(_ context.Context, placeID int64, key string) error {
	if r.fail {
		return errors.New("down")
	}
	r.set[keyOf(placeID, key)] = true
	return nil
}

func (r *recordedReferences) Unrefer(_ context.Context, placeID int64, key string) error {
	if r.fail {
		return errors.New("down")
	}
	delete(r.set, keyOf(placeID, key))
	return nil
}

func keyOf(placeID int64, key string) string { return key + "@" + itoa(placeID) }

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func (r *recordedReferences) sorted() []string {
	out := []string{}
	for k := range r.set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The activities tell the catalogue which places they go to, as they are made, changed and deleted, so that
// a catalogue that is a project of its own does not delete one that the club uses; and they ask it whether a
// place exists, since no key does.
func TestActivitiesTellTheCatalogueWhichPlacesTheyUse(t *testing.T) {
	tx := testutil.NewTx(t)
	ctx := context.Background()
	service, _ := bothServices(t, tx)
	refs := &recordedReferences{set: map[string]bool{}}
	service.WithPlaceReferences(refs)
	member := testutil.InsertMember(t, tx)
	leader := member.ID
	by := testutil.CatalogueAccount(t, tx, member)
	a, b, c := insertPlace(t, tx, by, "Cueva A"), insertPlace(t, tx, by, "Cueva B"), insertPlace(t, tx, by, "Cueva C")

	day := time.Now().AddDate(0, 0, 3)
	activity, err := service.Create(ctx, activities.CreateInput{
		Title: "Salida", Type: activities.TypeCaving, StartDate: day, EndDate: day, LeaderID: leader,
		Visibility: activities.VisibilityPublic, CreatedBy: leader, PlaceIDs: []int64{a, b},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := activities.ReferenceKey(activity.ID)
	if got, want := refs.sorted(), []string{keyOf(a, key), keyOf(b, key)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after creating: %v, want %v", got, want)
	}

	if _, _, err := service.SetPlaces(ctx, activity.ID, []int64{b, c}); err != nil {
		t.Fatal(err)
	}
	if got, want := refs.sorted(), []string{keyOf(b, key), keyOf(c, key)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after changing: %v, want %v", got, want)
	}

	// A place nobody knows is refused, and nothing is told of it.
	if _, _, err := service.SetPlaces(ctx, activity.ID, []int64{b, c, 999999999}); !errors.Is(err, activities.ErrUnknownPlace) {
		t.Fatalf("an unknown place: %v", err)
	}
	if len(refs.set) != 2 {
		t.Fatalf("references changed by a refused change: %v", refs.sorted())
	}

	// A catalogue that does not answer does not stop the club: the change is made, and the sync puts it right.
	refs.fail = true
	if _, _, err := service.SetPlaces(ctx, activity.ID, []int64{a, b, c}); err != nil {
		t.Fatalf("with the catalogue down: %v", err)
	}
	refs.fail = false
	n, err := service.SyncReferences(ctx)
	if err != nil || n < 3 {
		t.Fatalf("sync: %d %v", n, err)
	}
	if got, want := refs.sorted(), []string{keyOf(a, key), keyOf(b, key), keyOf(c, key)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after the sync: %v, want %v", got, want)
	}

	if err := service.Delete(ctx, activity.ID); err != nil {
		t.Fatal(err)
	}
	if len(refs.set) != 0 {
		t.Fatalf("after deleting: %v", refs.sorted())
	}
}
