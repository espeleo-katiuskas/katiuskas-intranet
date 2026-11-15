package places_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/places"
)

func systemPath(s places.System, rest string) string {
	return fmt.Sprintf("/api/v1/systems/%d%s", s.ID, rest)
}

// newSystem creates a system as the member and returns it.
func (e *env) newSystem(name string, description any) places.System {
	e.t.Helper()
	rec := e.json("POST", "/api/v1/systems", e.member.token, map[string]any{"name": name, "description": description})
	status(e.t, rec, http.StatusCreated)
	e.notifier.wait(e.t)
	return decode[places.System](e.t, rec)
}

func names(list []places.SystemPlace) string {
	var out []string
	for _, p := range list {
		out = append(out, p.Name)
	}
	return strings.Join(out, "|")
}

// A system is created and replaced by any member, with its name and description, says who and when, is
// listed with its cavities, and is deleted by admin or board, which leaves its cavities where they are.
func TestSystemCRUD(t *testing.T) {
	e := newEnv(t)
	rec := e.json("POST", "/api/v1/systems", e.member.token, map[string]any{"name": "  Sistema del Cabrito ", "description": "Tres bocas, una galería."})
	status(t, rec, http.StatusCreated)
	s := decode[places.System](t, rec)
	if s.Name != "Sistema del Cabrito" || *s.Description != "Tres bocas, una galería." || s.CreatedBy != e.member.user.Login ||
		s.UpdatedBy != e.member.user.Login || s.FileCount != 0 || s.Places == nil || len(s.Places) != 0 {
		t.Errorf("unexpected system: %+v", s)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha creado el sistema «Sistema del Cabrito»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	errorCode(t, e.json("POST", "/api/v1/systems", e.member.token, map[string]any{"name": "sistema DEL cabrito"}), 409, "duplicate_system")
	errorCode(t, e.json("POST", "/api/v1/systems", e.member.token, map[string]any{"name": "  "}), 400, "system_name_required")
	errorCode(t, e.json("POST", "/api/v1/systems", e.member.token, map[string]any{"name": strings.Repeat("a", places.MaxSystemNameLength+1)}), 400, "system_name_too_long")
	if blank := e.newSystem("Sin descripción", "   "); blank.Description != nil {
		t.Errorf("a blank description is none: %+v", blank)
	}

	// Somebody else edits it; an edit replaces both fields and the notice says what changed.
	rec = e.json("PUT", systemPath(s, ""), e.other.token, map[string]any{"name": "Sistema del Cabrito II"})
	status(t, rec, http.StatusOK)
	edited := decode[places.System](t, rec)
	if edited.Name != "Sistema del Cabrito II" || edited.Description != nil || edited.UpdatedBy != e.other.user.Login || edited.CreatedBy != e.member.user.Login {
		t.Errorf("an edit should replace the fields and say who: %+v", edited)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha editado el sistema «Sistema del Cabrito»") || !strings.Contains(msg, "Name:") {
		t.Errorf("unexpected notice: %q", msg)
	}
	status(t, e.json("PUT", systemPath(s, ""), e.other.token, map[string]any{"name": "SISTEMA DEL CABRITO II"}), http.StatusOK) // only the case changes
	e.notifier.wait(t)
	errorCode(t, e.json("PUT", systemPath(s, ""), e.other.token, map[string]any{"name": "sin descripción"}), 409, "duplicate_system")
	errorCode(t, e.json("PUT", systemPath(s, ""), e.other.token, map[string]any{"name": ""}), 400, "system_name_required")
	errorCode(t, e.json("PUT", "/api/v1/systems/987654", e.other.token, map[string]any{"name": "X"}), 404, "not_found")
	errorCode(t, e.json("GET", "/api/v1/systems/987654", e.other.token, nil), 404, "not_found")

	// The listing: by name, ignoring case, with the cavities of each.
	a := e.newPlace(e.cavidad, "Boca Alta", nil)
	b := e.newPlace(e.cavidad, "Boca Baja", map[string]any{"subtype_id": e.subSima})
	e.notifier.wait(t)
	e.notifier.wait(t)
	status(t, e.json("PUT", systemPath(s, "/places"), e.member.token, map[string]any{"place_ids": []int64{b.ID, a.ID}}), http.StatusOK)
	e.notifier.wait(t)
	type listed struct {
		ID     int64
		Name   string
		Places []places.SystemPlaceRef
	}
	list := decode[[]listed](t, e.json("GET", "/api/v1/systems", e.other.token, nil))
	if len(list) != 2 || list[0].Name != "Sin descripción" || list[1].ID != s.ID || len(list[0].Places) != 0 ||
		len(list[1].Places) != 2 || list[1].Places[0].Name != "Boca Alta" || list[1].Places[1].Subtype != "Sima" {
		t.Errorf("unexpected list: %+v", list)
	}

	// Deleting it is for admin and board, and its cavities stay, in no system.
	errorCode(t, e.json("DELETE", systemPath(s, ""), e.member.token, nil), 403, "forbidden")
	status(t, e.json("DELETE", systemPath(s, ""), e.board.token, nil), http.StatusNoContent)
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha borrado el sistema «SISTEMA DEL CABRITO II»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	errorCode(t, e.json("DELETE", systemPath(s, ""), e.admin.token, nil), 404, "not_found")
	got := decode[places.Place](t, e.json("GET", placePath(a, ""), e.member.token, nil))
	if got.SystemID != nil || got.System != nil {
		t.Errorf("a cavity must stay, in no system: %+v", got)
	}
	// Everything needs a session.
	if rec := e.json("GET", "/api/v1/systems", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a session is needed, got %d", rec.Code)
	}
}

// The cavities of a system: set from the system or from the place, only cavities, one system each; the
// system page has everything about each, and a place says which system it is in.
func TestSystemCavities(t *testing.T) {
	e := newEnv(t)
	s := e.newSystem("Sistema A", "Un sistema.")
	other := e.newSystem("Sistema B", nil)
	boca := e.newPlace(e.cavidad, "Boca 1", map[string]any{"alt_names": []string{"La Uno"}, "description": "Estrecha.", "depth_m": 40, "access": "Por la pista."})
	sima := e.newPlace(e.cavidad, "Boca 2", map[string]any{"subtype_id": e.subSima})
	ferrata := e.newPlace(e.ferrata, "Una ferrata", nil)
	for i := 0; i < 3; i++ {
		e.notifier.wait(t)
	}

	// From the system: the cavities given are exactly its cavities.
	rec := e.json("PUT", systemPath(s, "/places"), e.other.token, map[string]any{"place_ids": []int64{boca.ID, sima.ID, boca.ID}})
	status(t, rec, http.StatusOK)
	if refs := decode[[]places.SystemPlaceRef](t, rec); len(refs) != 2 || refs[0].Name != "Boca 1" || refs[1].Subtype != "Sima" {
		t.Errorf("unexpected cavities: %+v", refs)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha cambiado las cavidades del sistema «Sistema A»: «Boca 1», «Boca 2»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	full := decode[places.System](t, e.json("GET", systemPath(s, ""), e.member.token, nil))
	if names(full.Places) != "Boca 1|Boca 2" || full.UpdatedBy != e.other.user.Login {
		t.Errorf("unexpected system: %+v", full)
	}
	first := full.Places[0]
	if first.System == nil || *first.System != "Sistema A" || *first.Subtype != "Cueva" || *first.DepthM != 40 ||
		strings.Join(first.AltNames, "") != "La Uno" || *first.Description != "Estrecha." || *first.Access != "Por la pista." ||
		first.Where == "" || first.Lat == 0 {
		t.Errorf("the system should carry everything about each cavity: %+v", first)
	}
	// The place says which system it is in, in the listing too.
	if got := decode[places.Place](t, e.json("GET", placePath(boca, ""), e.member.token, nil)); got.SystemID == nil || *got.SystemID != s.ID || *got.System != "Sistema A" {
		t.Errorf("the place should say its system: %+v", got)
	}
	for _, it := range decode[[]places.ListItem](t, e.json("GET", "/api/v1/places", e.member.token, nil)) {
		if it.ID == sima.ID && (it.SystemID == nil || *it.System != "Sistema A") {
			t.Errorf("the listing should say it too: %+v", it)
		}
	}

	// The same cavities say nothing; fewer unlink the rest; a cavity in another system moves.
	status(t, e.json("PUT", systemPath(s, "/places"), e.other.token, map[string]any{"place_ids": []int64{boca.ID, sima.ID}}), http.StatusOK)
	status(t, e.json("PUT", systemPath(other, "/places"), e.other.token, map[string]any{"place_ids": []int64{sima.ID}}), http.StatusOK)
	if msg := e.notifier.wait(t); !strings.Contains(msg, "del sistema «Sistema B»: «Boca 2»") {
		t.Errorf("expected the notice of the move (none for the unchanged), got %q", msg)
	}
	if now := decode[places.System](t, e.json("GET", systemPath(s, ""), e.member.token, nil)); names(now.Places) != "Boca 1" {
		t.Errorf("a cavity is in one system, it left the first: %q", names(now.Places))
	}

	// Only cavities, which exist.
	errorCode(t, e.json("PUT", systemPath(s, "/places"), e.other.token, map[string]any{"place_ids": []int64{boca.ID, ferrata.ID}}), 400, "not_a_cavity")
	errorCode(t, e.json("PUT", systemPath(s, "/places"), e.other.token, map[string]any{"place_ids": []int64{987654}}), 400, "unknown_place")
	errorCode(t, e.json("PUT", "/api/v1/systems/987654/places", e.other.token, map[string]any{"place_ids": []int64{}}), 404, "not_found")
	if now := decode[places.System](t, e.json("GET", systemPath(s, ""), e.member.token, nil)); names(now.Places) != "Boca 1" {
		t.Errorf("a refused change must change nothing: %q", names(now.Places))
	}
	status(t, e.json("PUT", systemPath(s, "/places"), e.other.token, map[string]any{"place_ids": []int64{}}), http.StatusOK)
	e.notifier.wait(t)

	// From the place: the system is one more field, only for a cavity, and which exists.
	rec = e.json("PUT", placePath(boca, ""), e.member.token, body(e.cavidad, "Boca 1", map[string]any{"system_id": s.ID}))
	status(t, rec, http.StatusOK)
	if got := decode[places.Place](t, rec); got.SystemID == nil || *got.SystemID != s.ID {
		t.Errorf("the place should be in the system: %+v", got)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "System: (vacío) → Sistema A") {
		t.Errorf("the notice should say the system changed: %q", msg)
	}
	errorCode(t, e.json("PUT", placePath(boca, ""), e.member.token, body(e.cavidad, "Boca 1", map[string]any{"system_id": 987654})), 400, "unknown_system")
	errorCode(t, e.json("PUT", placePath(ferrata, ""), e.member.token, body(e.ferrata, "Una ferrata", map[string]any{"system_id": s.ID})), 400, "invalid_place_fields")
	// Changing the type to one that is not a cavity refuses the system it was in; leaving it out clears it.
	errorCode(t, e.json("PUT", placePath(boca, ""), e.member.token, body(e.canyon, "Boca 1", map[string]any{"system_id": s.ID})), 400, "invalid_place_fields")
	status(t, e.json("PUT", placePath(boca, ""), e.member.token, body(e.cavidad, "Boca 1", nil)), http.StatusOK)
	if now := decode[places.System](t, e.json("GET", systemPath(s, ""), e.member.token, nil)); len(now.Places) != 0 {
		t.Errorf("leaving the system out of the edit takes the cavity out of it: %q", names(now.Places))
	}
}

// The files of a system: the same as the files of a place, but its own, in the same table and
// directory without ever mixing with a place's.
func TestSystemFiles(t *testing.T) {
	e := newEnv(t)
	s := e.newSystem("Sistema con fotos", nil)
	p := e.newPlace(e.cavidad, "Boca con foto", nil)
	e.notifier.wait(t)
	path := systemPath(s, "/files")

	rec := e.uploadTo(path, e.other.token, "topo.png", " Topografía ", png)
	status(t, rec, http.StatusCreated)
	topo := decode[places.File](t, rec)
	if topo.SystemID == nil || *topo.SystemID != s.ID || topo.PlaceID != nil || *topo.Title != "Topografía" || topo.ContentType != "image/png" {
		t.Errorf("unexpected file: %+v", topo)
	}
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha adjuntado «topo.png» al sistema «Sistema con fotos»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	// A file of a place with the same directory number is another thing.
	rec = e.uploadTo(placePath(p, "/files"), e.member.token, "foto.png", "", png)
	status(t, rec, http.StatusCreated)
	photo := decode[places.File](t, rec)
	e.notifier.wait(t)
	if photo.ID == topo.ID {
		t.Fatalf("a row id is unique among places and systems")
	}

	// Each lists its own; the count is on the system; the bytes are served; the others' are not reachable.
	if list := decode[[]places.File](t, e.json("GET", path, e.member.token, nil)); len(list) != 1 || list[0].ID != topo.ID {
		t.Errorf("a system lists its own files: %+v", list)
	}
	if list := decode[[]places.File](t, e.json("GET", placePath(p, "/files"), e.member.token, nil)); len(list) != 1 || list[0].ID != photo.ID {
		t.Errorf("a place lists its own files: %+v", list)
	}
	if got := decode[places.System](t, e.json("GET", systemPath(s, ""), e.member.token, nil)); got.FileCount != 1 {
		t.Errorf("expected 1 file, got %d", got.FileCount)
	}
	if rec := e.json("GET", systemPath(s, fmt.Sprintf("/files/%d/file", topo.ID)), e.member.token, nil); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Errorf("the bytes of a file of a system: %d %v", rec.Code, rec.Header())
	}
	errorCode(t, e.json("GET", systemPath(s, fmt.Sprintf("/files/%d/file", photo.ID)), e.member.token, nil), 404, "not_found")
	errorCode(t, e.json("GET", placePath(p, fmt.Sprintf("/files/%d/file", topo.ID)), e.member.token, nil), 404, "not_found")
	errorCode(t, e.json("GET", "/api/v1/systems/987654/files", e.member.token, nil), 404, "not_found")
	errorCode(t, e.uploadTo("/api/v1/systems/987654/files", e.member.token, "a.png", "", png), 404, "not_found")
	errorCode(t, e.uploadTo(path, e.member.token, "vacio.txt", "", nil), 400, "file_required")

	// Who removes: whoever attached it, admin and board.
	errorCode(t, e.json("DELETE", systemPath(s, fmt.Sprintf("/files/%d", topo.ID)), e.member.token, nil), 403, "place_file_not_yours")
	status(t, e.json("DELETE", systemPath(s, fmt.Sprintf("/files/%d", topo.ID)), e.other.token, nil), http.StatusNoContent)
	if msg := e.notifier.wait(t); !strings.Contains(msg, "ha quitado «topo.png» del sistema «Sistema con fotos»") {
		t.Errorf("unexpected notice: %q", msg)
	}
	if _, err := e.storage.Open(s.ID, topo.ID); err == nil {
		t.Errorf("the bytes must be gone")
	}

	// Deleting a system takes its files, rows and bytes, with it; the place's stay.
	rec = e.uploadTo(path, e.member.token, "otro.png", "", png)
	status(t, rec, http.StatusCreated)
	kept := decode[places.File](t, rec)
	e.notifier.wait(t)
	status(t, e.json("DELETE", systemPath(s, ""), e.admin.token, nil), http.StatusNoContent)
	if _, err := e.storage.Open(s.ID, kept.ID); err == nil {
		t.Errorf("the bytes of a deleted system must be gone")
	}
	if _, err := e.storage.Open(p.ID, photo.ID); err != nil {
		t.Errorf("the files of a place must not go with a system: %v", err)
	}
}
