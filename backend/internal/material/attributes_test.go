package material_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/material"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

// decodeInto decodes a recorded response body, failing the test if it is not the wanted JSON.
func decodeInto[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status || (code != "" && errorCode(t, rec) != code) {
		t.Fatalf("expected %d %q, got %d %s", status, code, rec.Code, rec.Body.String())
	}
}

// A type's attributes: an admin defines them (text or enum, required or not), and an item of the
// type carries a value for each; its name is the type, make and model, the values in the order of
// the attributes, and the description.
func TestAttributesOfATypeAndTheItemsThatCarryThem(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	admin := tokenFor(t, authn, 1, "admin")
	board := tokenFor(t, authn, 2, "board")
	typeID := testutil.MaterialTypeID(t, tx, "Mosquetones")
	base := fmt.Sprintf("/api/v1/material/types/%d/attributes", typeID)

	// Only an admin edits the definitions.
	body := map[string]any{"name": "Material", "data_type": "text"}
	expectStatus(t, doRequest(mux, http.MethodPost, base, board, body), http.StatusForbidden, "")

	material1 := decodeInto[material.Attribute](t, doRequest(mux, http.MethodPost, base, admin, body))
	if material1.Position != 0 || material1.DataType != material.AttributeText || material1.Required {
		t.Fatalf("text attribute: %+v", material1)
	}
	shape := decodeInto[material.Attribute](t, doRequest(mux, http.MethodPost, base, admin, map[string]any{
		"name": "Forma", "data_type": "enum", "required": true, "options": []string{"simétrico", "asimétrico", "HMS"},
	}))
	if shape.Position != 1 || len(shape.Options) != 3 || !shape.Required {
		t.Fatalf("enum attribute: %+v", shape)
	}
	expectStatus(t, doRequest(mux, http.MethodPost, base, admin, body), http.StatusConflict, "duplicate_attribute")
	expectStatus(t, doRequest(mux, http.MethodPost, base, admin, map[string]any{"name": "X", "data_type": "number"}),
		http.StatusBadRequest, "invalid_body")
	expectStatus(t, doRequest(mux, http.MethodPost, base, admin, map[string]any{"name": "X", "data_type": "text", "options": []string{"a"}}),
		http.StatusBadRequest, "invalid_body")
	// Cuerda and Saca have their own columns, not attributes.
	expectStatus(t, doRequest(mux, http.MethodPost,
		fmt.Sprintf("/api/v1/material/types/%d/attributes", testutil.MaterialTypeID(t, tx, "Cuerda")), admin, body),
		http.StatusConflict, "fixed_type")

	optionID := func(label string) int64 {
		for _, o := range shape.Options {
			if o.Label == label {
				return o.ID
			}
		}
		t.Fatalf("no option %q", label)
		return 0
	}
	item := func(extra map[string]any) map[string]any {
		b := generalItemBody(t, tx, 1)
		b["description"] = nil
		for k, v := range extra {
			b[k] = v
		}
		return b
	}

	// The type still requires a description: nothing yet changes about that.
	expectStatus(t, doRequest(mux, http.MethodPost, "/api/v1/material/items", board, item(nil)), http.StatusBadRequest, "item_description_required")
	// An admin makes it optional for this type.
	rec := doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/material/types/%d", typeID), admin, map[string]any{"description_required": false})
	expectStatus(t, rec, http.StatusOK, "")
	if got := decodeInto[material.MaterialType](t, rec); got.DescriptionRequired || len(got.Attributes) != 2 {
		t.Fatalf("type after the patch: %+v", got)
	}

	// The required attribute must have a value.
	expectStatus(t, doRequest(mux, http.MethodPost, "/api/v1/material/items", board, item(nil)), http.StatusBadRequest, "missing_attribute")
	// Values that do not fit.
	for name, attrs := range map[string][]map[string]any{
		"an unknown attribute":     {{"attribute_id": 999999, "value": "x"}},
		"an option of nowhere":     {{"attribute_id": shape.ID, "option_id": 999999}},
		"a value for an enum":      {{"attribute_id": shape.ID, "value": "HMS"}},
		"an option for a text":     {{"attribute_id": material1.ID, "option_id": optionID("HMS")}},
		"an attribute given twice": {{"attribute_id": shape.ID, "option_id": optionID("HMS")}, {"attribute_id": shape.ID, "option_id": optionID("HMS")}},
	} {
		expectStatus(t, doRequest(mux, http.MethodPost, "/api/v1/material/items", board, item(map[string]any{"attributes": attrs})),
			http.StatusBadRequest, "invalid_attribute")
		_ = name
	}

	create := item(map[string]any{
		"brand": "Petzl", "model": "Attache",
		"attributes": []map[string]any{
			{"attribute_id": shape.ID, "option_id": optionID("asimétrico")},
			{"attribute_id": material1.ID, "value": "  aluminio "},
		},
	})
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/items", board, create)
	expectStatus(t, rec, http.StatusCreated, "")
	created := decodeInto[material.Item](t, rec)
	// The values come in the order of the attributes, not the order they were sent in.
	if created.DisplayName != "Mosquetones · Petzl Attache · aluminio · asimétrico" || len(created.Attributes) != 2 || created.Incomplete {
		t.Errorf("created: %q %+v", created.DisplayName, created.Attributes)
	}
	// The same item again is refused, however the values are written.
	expectStatus(t, doRequest(mux, http.MethodPost, "/api/v1/material/items", board, create), http.StatusConflict, "duplicate_material")

	// Another value makes another item; with a description, it is at the end of the name.
	other := item(map[string]any{
		"brand": "Petzl", "model": "Attache", "description": "con muesca",
		"attributes": []map[string]any{{"attribute_id": shape.ID, "option_id": optionID("HMS")}},
	})
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/items", board, other)
	expectStatus(t, rec, http.StatusCreated, "")
	if got := decodeInto[material.Item](t, rec); got.DisplayName != "Mosquetones · Petzl Attache · HMS · con muesca" {
		t.Errorf("name: %q", got.DisplayName)
	}

	// The list carries the same names, and the types list their attributes and options.
	rec = doRequest(mux, http.MethodGet, "/api/v1/material/items", board, nil)
	found := false
	for _, it := range decodeInto[[]material.Item](t, rec) {
		found = found || it.DisplayName == created.DisplayName
	}
	if !found {
		t.Errorf("the list lacks %q: %s", created.DisplayName, rec.Body.String())
	}

	// Renaming an option changes the name of the items that have it; one in use cannot be deleted.
	expectStatus(t, doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/material/options/%d", optionID("asimétrico")), admin,
		map[string]any{"label": "asimétrico (pera)"}), http.StatusOK, "")
	rec = doRequest(mux, http.MethodGet, fmt.Sprintf("/api/v1/material/items/%d/movements", created.ID), board, nil)
	expectStatus(t, rec, http.StatusOK, "")
	expectStatus(t, doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/material/options/%d", optionID("asimétrico")), admin, nil),
		http.StatusConflict, "option_in_use")
	expectStatus(t, doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/material/options/%d", optionID("simétrico")), admin, nil),
		http.StatusNoContent, "")

	// Editing: the attributes are resent, and the type's required ones still count.
	path := fmt.Sprintf("/api/v1/material/items/%d", created.ID)
	edited := item(map[string]any{"brand": "Petzl", "model": "Attache",
		"attributes": []map[string]any{{"attribute_id": shape.ID, "option_id": optionID("HMS")}}})
	expectStatus(t, doRequest(mux, http.MethodPut, path, board, edited), http.StatusOK, "")
	// ...and it is now the same as the other one but for the description.
	edited["description"] = "con muesca"
	expectStatus(t, doRequest(mux, http.MethodPut, path, board, edited), http.StatusConflict, "duplicate_material")

	// Deleting the attribute "Material" is refused if it would leave two items the same, and goes
	// through, taking the values with it, if not.
	other2 := item(map[string]any{"brand": "Petzl", "model": "Attache",
		"attributes": []map[string]any{
			{"attribute_id": shape.ID, "option_id": optionID("HMS")}, {"attribute_id": material1.ID, "value": "acero"}}})
	expectStatus(t, doRequest(mux, http.MethodPost, "/api/v1/material/items", board, other2), http.StatusCreated, "")
	expectStatus(t, doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/material/attributes/%d", material1.ID), admin, nil),
		http.StatusConflict, "would_duplicate_items")

	// Reordering: all the attributes, once each.
	expectStatus(t, doRequest(mux, http.MethodPut, base+"/order", admin, map[string]any{"attribute_ids": []int64{shape.ID}}),
		http.StatusBadRequest, "invalid_body")
	rec = doRequest(mux, http.MethodPut, base+"/order", admin, map[string]any{"attribute_ids": []int64{shape.ID, material1.ID}})
	expectStatus(t, rec, http.StatusOK, "")
	if got := decodeInto[material.MaterialType](t, rec); got.Attributes[0].ID != shape.ID {
		t.Errorf("order after reordering: %+v", got.Attributes)
	}
}

// An item saved before its type asked for something is listed as incomplete until it is edited, and
// a rope or a bag takes no attributes at all.
func TestIncompleteItemsAndFixedTypes(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	admin := tokenFor(t, authn, 1, "admin")
	board := tokenFor(t, authn, 2, "board")
	typeID := testutil.MaterialTypeID(t, tx, "Mosquetones")

	rec := doRequest(mux, http.MethodPost, "/api/v1/material/items", board, generalItemBody(t, tx, 1))
	expectStatus(t, rec, http.StatusCreated, "")
	item := decodeInto[material.Item](t, rec)
	if item.Incomplete {
		t.Fatalf("a new item is complete: %+v", item)
	}

	expectStatus(t, doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/material/types/%d/attributes", typeID), admin,
		map[string]any{"name": "Color", "data_type": "text", "required": true}), http.StatusCreated, "")
	rec = doRequest(mux, http.MethodGet, "/api/v1/material/items", board, nil)
	for _, it := range decodeInto[[]material.Item](t, rec) {
		if it.ID == item.ID && !it.Incomplete {
			t.Errorf("the item lacks a required attribute and is not incomplete: %+v", it)
		}
	}

	ropeType := testutil.MaterialTypeID(t, tx, "Cuerda")
	expectStatus(t, doRequest(mux, http.MethodPatch, fmt.Sprintf("/api/v1/material/types/%d", ropeType), admin,
		map[string]any{"description_required": false}), http.StatusConflict, "fixed_type")
	rope := map[string]any{
		"type_id": ropeType, "brand": "Beal", "model": "Spelenium", "code": "C-1", "rope_type": "caving",
		"diameter_mm": 9.5, "manufacture_year": 2024, "length_m": 50,
		"attributes": []map[string]any{{"attribute_id": 1, "value": "x"}},
	}
	expectStatus(t, doRequest(mux, http.MethodPost, "/api/v1/material/items", board, rope), http.StatusBadRequest, "invalid_attribute")
	delete(rope, "attributes")
	rec = doRequest(mux, http.MethodPost, "/api/v1/material/items", board, rope)
	expectStatus(t, rec, http.StatusCreated, "")
	if got := decodeInto[material.Item](t, rec); got.Incomplete || got.DisplayName != "Cuerda · Beal Spelenium" {
		t.Errorf("rope: %q incomplete=%v", got.DisplayName, got.Incomplete)
	}
}

// A type goes with its attributes and their options (nothing cascades in the database), unless it
// has items.
func TestDeleteTypeWithAttributes(t *testing.T) {
	mux, authn, tx := newTestServer(t)
	admin := tokenFor(t, authn, 1, "admin")
	board := tokenFor(t, authn, 2, "board")

	create := func(name string) material.MaterialType {
		rec := doRequest(mux, http.MethodPost, "/api/v1/material/types", admin, map[string]any{"name": name, "description_required": false})
		expectStatus(t, rec, http.StatusCreated, "")
		ty := decodeInto[material.MaterialType](t, rec)
		if ty.DescriptionRequired {
			t.Fatalf("description_required was asked false: %+v", ty)
		}
		rec = doRequest(mux, http.MethodPost, fmt.Sprintf("/api/v1/material/types/%d/attributes", ty.ID), admin,
			map[string]any{"name": "Color", "data_type": "enum", "options": []string{"rojo", "azul"}})
		expectStatus(t, rec, http.StatusCreated, "")
		return ty
	}

	empty := create("Cintas")
	expectStatus(t, doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/material/types/%d", empty.ID), admin, nil), http.StatusNoContent, "")

	used := create("Anclajes")
	item := map[string]any{"type_id": used.ID, "total_quantity": 1}
	expectStatus(t, doRequest(mux, http.MethodPost, "/api/v1/material/items", board, item), http.StatusCreated, "")
	expectStatus(t, doRequest(mux, http.MethodDelete, fmt.Sprintf("/api/v1/material/types/%d", used.ID), admin, nil), http.StatusConflict, "type_in_use")
	// The refused delete left its attributes alone.
	rec := doRequest(mux, http.MethodGet, "/api/v1/material/types", admin, nil)
	for _, ty := range decodeInto[[]material.MaterialType](t, rec) {
		if ty.ID == used.ID && (len(ty.Attributes) != 1 || len(ty.Attributes[0].Options) != 2) {
			t.Errorf("attributes after a refused delete: %+v", ty.Attributes)
		}
	}
	_ = tx
}
