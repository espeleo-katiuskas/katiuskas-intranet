package material

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/auth"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/httpapi"
)

// UserLookup translates between a person's login and their numeric id.
// This package's own routes are nested under /users/{login}/movements,
// but material_movements is keyed by numeric user id — users.Service
// satisfies this structurally (see main.go's wiring), without material
// having to import that package directly. licenses/memberships/roles
// genuinely can't (users.Service imports all three); material keeps
// the same small local-interface shape anyway for consistency across
// every package that's keyed by user id.
type UserLookup interface {
	UserIDForLogin(ctx context.Context, login string) (int64, error)
	LoginForUserID(ctx context.Context, id int64) (string, error)
}

// recipientRequiredTypes are the movement types that always exchange
// the item with a specific person and so must name one — see
// MovementType. Loan, sale and return's recipients are optional
// (neither required nor forbidden — see recipientForbiddenTypes below
// for the types that forbid one outright).
var recipientRequiredTypes = map[MovementType]bool{
	MovementRental: true,
}

// recipientForbiddenTypes are the movement types that never have a
// recipient at all: acquisition, loss, abandonment and retirement are
// stock-level changes, not exchanges with a person.
var recipientForbiddenTypes = map[MovementType]bool{
	MovementAcquisition: true,
	MovementLoss:        true,
	MovementAbandonment: true,
	MovementRetirement:  true,
}

var validMovementTypes = map[MovementType]bool{
	MovementLoan:        true,
	MovementRental:      true,
	MovementSale:        true,
	MovementAcquisition: true,
	MovementLoss:        true,
	MovementAbandonment: true,
	MovementRetirement:  true,
	MovementReturn:      true,
}

type Handler struct {
	service       *Service
	authenticator *auth.Authenticator
	users         UserLookup
	notifier      httpapi.MutationNotifier
}

func NewHandler(service *Service, authenticator *auth.Authenticator, users UserLookup, notifier httpapi.MutationNotifier) *Handler {
	return &Handler{service: service, authenticator: authenticator, users: users, notifier: notifier}
}

// actorLogin resolves the calling session's own login, for the "who
// did this" half of a mutation notification — every write endpoint in
// this handler needs it, so it's pulled out rather than repeated at
// each call site. Falls back to a placeholder rather than failing the
// whole request if the lookup itself errors, since a notification is
// never worth failing the actual write over.
func (h *Handler) actorLogin(ctx context.Context) string {
	current, _ := auth.UserFromContext(ctx)
	login, err := h.users.LoginForUserID(ctx, current.ID)
	if err != nil {
		return fmt.Sprintf("user#%d", current.ID)
	}
	return login
}

// itemLabel names an item for a mutation notification: its display name (type, make and model,
// attributes, description).
func itemLabel(item Item) string {
	if item.DisplayName != "" {
		return item.DisplayName
	}
	return fmt.Sprintf("item#%d", item.ID)
}

// attributesLine says what changed in an item's attributes, for the notification of an edit, or ""
// if nothing did.
func attributesLine(before, after Item) string {
	render := func(it Item) string {
		parts := []string{}
		for _, v := range it.Attributes {
			parts = append(parts, v.Name+" = "+v.Value)
		}
		if len(parts) == 0 {
			return "(vacío)"
		}
		return strings.Join(parts, ", ")
	}
	b, a := render(before), render(after)
	if b == a {
		return ""
	}
	return fmt.Sprintf("Attributes: %s → %s", b, a)
}

// movementVerb names what a movement type does, in Spanish, for the
// mutation notification below — see MovementType's own doc comment for
// what each one actually means to an item's computed quantities.
func movementVerb(t MovementType) string {
	switch t {
	case MovementLoan:
		return "préstamo"
	case MovementRental:
		return "alquiler"
	case MovementSale:
		return "venta"
	case MovementAcquisition:
		return "adquisición"
	case MovementLoss:
		return "pérdida"
	case MovementAbandonment:
		return "abandono"
	case MovementRetirement:
		return "retirada"
	case MovementReturn:
		return "devolución"
	default:
		return string(t)
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/material/items", h.authenticator.Protect(h.listItems))
	mux.Handle("POST /api/v1/material/items", h.authenticator.Protect(h.createItem, "admin", "board", "material"))
	mux.Handle("PUT /api/v1/material/items/{id}", h.authenticator.Protect(h.updateItem, "admin", "board", "material"))
	mux.Handle("DELETE /api/v1/material/items/{id}", h.authenticator.Protect(h.deleteItem, "admin"))
	mux.Handle("GET /api/v1/material/items/{id}/movements", h.authenticator.Protect(h.listItemMovements))

	// Whoever can read the items can read their types (every item names one). Changing the list is
	// admin only (/admin), and never Cuerda or Saca.
	mux.Handle("GET /api/v1/material/types", h.authenticator.Protect(h.listMaterialTypes))
	mux.Handle("POST /api/v1/material/types", h.authenticator.Protect(h.createType, "admin"))
	mux.Handle("PUT /api/v1/material/types/order", h.authenticator.Protect(h.reorderTypes, "admin"))
	mux.Handle("PATCH /api/v1/material/types/{id}", h.authenticator.Protect(h.updateType, "admin"))
	mux.Handle("DELETE /api/v1/material/types/{id}", h.authenticator.Protect(h.deleteType, "admin"))

	// The attributes of a type and the options of an enum attribute: admin only, like the types.
	mux.Handle("POST /api/v1/material/types/{id}/attributes", h.authenticator.Protect(h.createAttribute, "admin"))
	mux.Handle("PUT /api/v1/material/types/{id}/attributes/order", h.authenticator.Protect(h.reorderAttributes, "admin"))
	mux.Handle("PATCH /api/v1/material/attributes/{id}", h.authenticator.Protect(h.updateAttribute, "admin"))
	mux.Handle("DELETE /api/v1/material/attributes/{id}", h.authenticator.Protect(h.deleteAttribute, "admin"))
	mux.Handle("POST /api/v1/material/attributes/{id}/options", h.authenticator.Protect(h.createOption, "admin"))
	mux.Handle("PATCH /api/v1/material/options/{id}", h.authenticator.Protect(h.renameOption, "admin"))
	mux.Handle("DELETE /api/v1/material/options/{id}", h.authenticator.Protect(h.deleteOption, "admin"))

	mux.Handle("GET /api/v1/material/movements", h.authenticator.Protect(h.listMovements, "admin", "board", "material"))
	mux.Handle("POST /api/v1/material/movements", h.authenticator.Protect(h.createMovement, "admin", "board", "material"))
	mux.Handle("POST /api/v1/material/movements/{id}/return", h.authenticator.Protect(h.returnMovement, "admin", "board", "material"))
	mux.Handle("DELETE /api/v1/material/movements/{id}", h.authenticator.Protect(h.deleteMovement, "admin"))

	mux.Handle("GET /api/v1/users/{login}/material/movements", h.authenticator.Protect(h.listMovementsByUser))
}

// resolveUserLogin looks up the numeric id behind a {login} path
// segment. Any failure — malformed, or simply nobody by that login — is
// reported the same way (404): this package only needs to know whether
// the lookup succeeded, not why it didn't.
func (h *Handler) resolveUserLogin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := h.users.UserIDForLogin(r.Context(), r.PathValue("login"))
	if err != nil {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
		return 0, false
	}
	return id, true
}

func (h *Handler) listItems(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListItems(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list items")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) createItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TypeID             int64            `json:"type_id"`
		Brand              *string          `json:"brand"`
		Model              *string          `json:"model"`
		Code               *string          `json:"code"`
		Description        *string          `json:"description"`
		Attributes         []AttributeValue `json:"attributes"`
		AcquiredAt         *string          `json:"acquired_at"`
		TotalQuantity      int32            `json:"total_quantity"`
		Loanable           *bool            `json:"loanable"`
		RentalPricePerWeek *float64         `json:"rental_price_per_week"`
		SalePrice          *float64         `json:"sale_price"`
		RopeType           *RopeType        `json:"rope_type"`
		DiameterMM         *float64         `json:"diameter_mm"`
		ManufactureYear    *int32           `json:"manufacture_year"`
		LengthM            *int32           `json:"length_m"`
		BagType            *BagType         `json:"bag_type"`
		CapacityLiters     *float64         `json:"capacity_liters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	var acquiredAt *time.Time
	if body.AcquiredAt != nil && *body.AcquiredAt != "" {
		d, err := time.Parse("2006-01-02", *body.AcquiredAt)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "acquired_at must be YYYY-MM-DD")
			return
		}
		acquiredAt = &d
	}
	loanable := true
	if body.Loanable != nil {
		loanable = *body.Loanable
	}
	item, err := h.service.CreateItem(r.Context(), CreateItemInput{
		TypeID: body.TypeID, Brand: body.Brand, Model: body.Model, Code: body.Code,
		Description: body.Description, Attributes: body.Attributes, AcquiredAt: acquiredAt, InitialQuantity: body.TotalQuantity,
		Loanable: loanable, RentalPricePerWeek: body.RentalPricePerWeek, SalePrice: body.SalePrice,
		RopeType: body.RopeType, DiameterMM: body.DiameterMM, ManufactureYear: body.ManufactureYear,
		LengthM: body.LengthM, BagType: body.BagType, CapacityLiters: body.CapacityLiters,
	})
	if err != nil {
		writeItemError(w, err, "create")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🆕 %s ha añadido el material «%s» (x%d).",
		h.actorLogin(r.Context()), itemLabel(item), item.TotalQuantity))
	httpapi.WriteJSON(w, http.StatusCreated, item)
}

// itemAuditFields are the Item fields an edit's notification diffs — those UpdateItemInput can change
// (the type by its name, not by the numeric id).
var itemAuditFields = []string{
	"Brand", "Model", "Code", "Description", "Loanable", "RentalPricePerWeek", "SalePrice",
	"RopeType", "DiameterMM", "ManufactureYear", "LengthM", "BagType", "CapacityLiters", "TypeName",
}

// updateItem replaces every editable field of an item with the body's: the type and the quantities
// are not among them (a different type is a different item, and the stock is what its movements
// say).
func (h *Handler) updateItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		Brand              *string          `json:"brand"`
		Model              *string          `json:"model"`
		Code               *string          `json:"code"`
		Description        *string          `json:"description"`
		Attributes         []AttributeValue `json:"attributes"`
		Loanable           *bool            `json:"loanable"`
		RentalPricePerWeek *float64         `json:"rental_price_per_week"`
		SalePrice          *float64         `json:"sale_price"`
		RopeType           *RopeType        `json:"rope_type"`
		DiameterMM         *float64         `json:"diameter_mm"`
		ManufactureYear    *int32           `json:"manufacture_year"`
		LengthM            *int32           `json:"length_m"`
		BagType            *BagType         `json:"bag_type"`
		CapacityLiters     *float64         `json:"capacity_liters"`
		TypeID             *int64           `json:"type_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	loanable := true
	if body.Loanable != nil {
		loanable = *body.Loanable
	}
	before, beforeErr := h.service.GetItem(r.Context(), id)
	item, err := h.service.UpdateItem(r.Context(), id, UpdateItemInput{
		TypeID: body.TypeID, Brand: body.Brand, Model: body.Model, Code: body.Code, Description: body.Description,
		Attributes: body.Attributes, Loanable: loanable, RentalPricePerWeek: body.RentalPricePerWeek, SalePrice: body.SalePrice,
		RopeType: body.RopeType, DiameterMM: body.DiameterMM, ManufactureYear: body.ManufactureYear,
		LengthM: body.LengthM, BagType: body.BagType, CapacityLiters: body.CapacityLiters,
	})
	if err != nil {
		writeItemError(w, err, "update")
		return
	}
	if beforeErr == nil {
		diff := httpapi.DiffFields(before, item, itemAuditFields...)
		if line := attributesLine(before, item); line != "" {
			diff = append(diff, line)
		}
		if len(diff) > 0 {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("✏️ %s ha editado el material «%s»:\n%s",
				h.actorLogin(r.Context()), itemLabel(before), strings.Join(diff, "\n")))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, item)
}

func (h *Handler) listMaterialTypes(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListMaterialTypes(r.Context())
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list material types")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// deleteItem removes an item with no history (see Repository.DeleteItem).
func (h *Handler) deleteItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	// Fetched before deleting, so the notification can still name the item.
	item, itemErr := h.service.GetItem(r.Context(), id)
	err = h.service.DeleteItem(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", err.Error())
		return
	case errors.Is(err, ErrItemHasMovements):
		httpapi.WriteError(w, http.StatusConflict, "item_has_movements", err.Error())
		return
	case err != nil:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete item")
		return
	}
	if itemErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado el material «%s».",
			h.actorLogin(r.Context()), itemLabel(item)))
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeItemError answers with the status and code of an error of the routes that create or edit an
// item.
func writeItemError(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrTypeNotFound):
		httpapi.WriteError(w, http.StatusBadRequest, "unknown_type", err.Error())
	case errors.Is(err, ErrSingleUnitItem):
		httpapi.WriteError(w, http.StatusBadRequest, "single_unit_item", err.Error())
	case errors.Is(err, ErrKindChange):
		httpapi.WriteError(w, http.StatusBadRequest, "kind_change", err.Error())
	case errors.Is(err, ErrInvalidItemFields):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_item_fields", err.Error())
	case errors.Is(err, ErrModelWithoutBrand):
		httpapi.WriteError(w, http.StatusBadRequest, "model_without_brand", err.Error())
	case errors.Is(err, ErrInvalidItemCode):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_item_code", err.Error())
	case errors.Is(err, ErrDescriptionRequired):
		httpapi.WriteError(w, http.StatusBadRequest, "item_description_required", err.Error())
	case errors.Is(err, ErrMissingAttribute):
		httpapi.WriteError(w, http.StatusBadRequest, "missing_attribute", err.Error())
	case errors.Is(err, ErrInvalidAttribute):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_attribute", err.Error())
	case errors.Is(err, ErrDuplicateItemCode):
		httpapi.WriteError(w, http.StatusConflict, "duplicate_item_code", err.Error())
	case errors.Is(err, ErrDuplicateItem):
		httpapi.WriteError(w, http.StatusConflict, "duplicate_material", err.Error())
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not "+what+" item")
	}
}

// writeTypeError answers with the status and code of an error of the type routes.
func writeTypeError(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, ErrBlankTypeName):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
	case errors.Is(err, ErrTypeNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrFixedType):
		httpapi.WriteError(w, http.StatusConflict, "fixed_type", err.Error())
	case errors.Is(err, ErrDuplicateType):
		httpapi.WriteError(w, http.StatusConflict, "duplicate_type", err.Error())
	case errors.Is(err, ErrTypeInUse):
		httpapi.WriteError(w, http.StatusConflict, "type_in_use", err.Error())
	case errors.Is(err, ErrAttributeNotFound), errors.Is(err, ErrOptionNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrBlankName), errors.Is(err, ErrInvalidDataType), errors.Is(err, ErrNotEnum), errors.Is(err, ErrInvalidOrder):
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", err.Error())
	case errors.Is(err, ErrDuplicateAttribute):
		httpapi.WriteError(w, http.StatusConflict, "duplicate_attribute", err.Error())
	case errors.Is(err, ErrDuplicateOption):
		httpapi.WriteError(w, http.StatusConflict, "duplicate_option", err.Error())
	case errors.Is(err, ErrOptionInUse):
		httpapi.WriteError(w, http.StatusConflict, "option_in_use", err.Error())
	case errors.Is(err, ErrWouldDuplicateItems):
		httpapi.WriteError(w, http.StatusConflict, "would_duplicate_items", err.Error())
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not "+what+" the type")
	}
}

func (h *Handler) createType(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name                string `json:"name"`
		DescriptionRequired *bool  `json:"description_required"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	descriptionRequired := true
	if body.DescriptionRequired != nil {
		descriptionRequired = *body.DescriptionRequired
	}
	t, err := h.service.CreateType(r.Context(), body.Name, descriptionRequired)
	if err != nil {
		writeTypeError(w, err, "create")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗂️ %s ha añadido el tipo de material «%s».",
		h.actorLogin(r.Context()), t.Name))
	httpapi.WriteJSON(w, http.StatusCreated, t)
}

// updateType renames a type and/or changes whether its items must have a description.
func (h *Handler) updateType(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	var body struct {
		Name                *string `json:"name"`
		DescriptionRequired *bool   `json:"description_required"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Name == nil && body.DescriptionRequired == nil) {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "give a name and/or description_required")
		return
	}
	before, beforeErr := h.service.GetMaterialType(r.Context(), id)
	t, err := h.service.UpdateType(r.Context(), id, body.Name, body.DescriptionRequired)
	if err != nil {
		writeTypeError(w, err, "update")
		return
	}
	if beforeErr == nil {
		actor := h.actorLogin(r.Context())
		if before.Name != t.Name {
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗂️ %s ha cambiado el nombre del tipo de material «%s» por «%s».",
				actor, before.Name, t.Name))
		}
		if before.DescriptionRequired != t.DescriptionRequired {
			state := "ya no es obligatoria"
			if t.DescriptionRequired {
				state = "pasa a ser obligatoria"
			}
			httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗂️ %s ha cambiado el tipo de material «%s»: la descripción %s.",
				actor, t.Name, state))
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, t)
}

func (h *Handler) deleteType(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	t, err := h.service.DeleteType(r.Context(), id)
	if err != nil {
		writeTypeError(w, err, "delete")
		return
	}
	httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗂️ %s ha borrado el tipo de material «%s».",
		h.actorLogin(r.Context()), t.Name))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listItemMovements(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	list, err := h.service.ListMovementsByItem(r.Context(), id)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list movements")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// listMovements lists movements by status (default 'active' — in
// practice only ever rentals, since loan/return are always
// 'completed'), or by activity when ?activity_id= is given instead —
// the activity page's own material listing, spanning every loan and
// return tied to it so it can compute what's still outstanding item by
// item.
func (h *Handler) listMovements(w http.ResponseWriter, r *http.Request) {
	if activityIDParam := r.URL.Query().Get("activity_id"); activityIDParam != "" {
		activityID, err := strconv.ParseInt(activityIDParam, 10, 64)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_activity_id", "activity_id must be numeric")
			return
		}
		list, err := h.service.ListMovementsByActivity(r.Context(), activityID)
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list movements")
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, list)
		return
	}

	status := MovementStatus(r.URL.Query().Get("status"))
	if status == "" {
		status = MovementActive
	}
	list, err := h.service.ListMovementsByStatus(r.Context(), status)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list movements")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

// listMovementsByUser lets a person see their own material history, or
// admin/board/material see anyone's — the same self-or-those-roles check
// memberships.listByUser uses, since a movement can carry a price and is
// more sensitive than, say, activity participation.
func (h *Handler) listMovementsByUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.resolveUserLogin(w, r)
	if !ok {
		return
	}
	current, _ := auth.UserFromContext(r.Context())
	if !current.HasAnyRole("admin", "board", "material") && current.ID != userID {
		httpapi.WriteError(w, http.StatusForbidden, "forbidden", "members can only view their own material history")
		return
	}
	list, err := h.service.ListMovementsByUser(r.Context(), userID)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not list movements")
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) createMovement(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemID       int64        `json:"item_id"`
		Login        string       `json:"login"`
		Type         MovementType `json:"type"`
		Quantity     int32        `json:"quantity"`
		TransactedAt *string      `json:"transacted_at"`
		ActivityID   *int64       `json:"activity_id"`
		Notes        *string      `json:"notes"`
		AccountID    *int64       `json:"account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_body", "malformed JSON body")
		return
	}
	if !validMovementTypes[body.Type] {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_type", "unrecognized movement type")
		return
	}
	if recipientRequiredTypes[body.Type] && body.Login == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "login_required", "rental movements require a login")
		return
	}
	if recipientForbiddenTypes[body.Type] && body.Login != "" {
		httpapi.WriteError(w, http.StatusBadRequest, "recipient_not_allowed", "acquisition, loss, abandonment and retirement movements have no recipient")
		return
	}
	var userID *int64
	if body.Login != "" {
		id, err := h.users.UserIDForLogin(r.Context(), body.Login)
		if err != nil {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		userID = &id
	}
	transactedAt := time.Now()
	if body.TransactedAt != nil && *body.TransactedAt != "" {
		d, err := time.Parse("2006-01-02", *body.TransactedAt)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, "invalid_date", "transacted_at must be YYYY-MM-DD")
			return
		}
		transactedAt = d
	}
	current, _ := auth.UserFromContext(r.Context())
	recordedBy := current.ID
	m, err := h.service.CreateMovement(r.Context(), CreateMovementInput{
		ItemID: body.ItemID, UserID: userID, Type: body.Type, Quantity: body.Quantity,
		TransactedAt: transactedAt, RecordedBy: &recordedBy, ActivityID: body.ActivityID, Notes: body.Notes,
		AccountID: body.AccountID,
	})
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "item not found")
		return
	}
	if errors.Is(err, ErrInsufficientStock) {
		httpapi.WriteError(w, http.StatusConflict, "insufficient_stock", err.Error())
		return
	}
	if errors.Is(err, ErrActivityRequired) {
		httpapi.WriteError(w, http.StatusBadRequest, "activity_required", err.Error())
		return
	}
	if errors.Is(err, ErrNotLoanable) {
		httpapi.WriteError(w, http.StatusConflict, "not_loanable", err.Error())
		return
	}
	if errors.Is(err, ErrNotRentable) {
		httpapi.WriteError(w, http.StatusConflict, "not_rentable", err.Error())
		return
	}
	if errors.Is(err, ErrNotSellable) {
		httpapi.WriteError(w, http.StatusConflict, "not_sellable", err.Error())
		return
	}
	if errors.Is(err, ErrSingleUnitItem) {
		httpapi.WriteError(w, http.StatusConflict, "single_unit_item", err.Error())
		return
	}
	if errors.Is(err, ErrReturnExceedsLoaned) {
		httpapi.WriteError(w, http.StatusConflict, "return_exceeds_loaned", err.Error())
		return
	}
	if errors.Is(err, ErrRecipientNotMember) {
		httpapi.WriteError(w, http.StatusConflict, "recipient_not_member", err.Error())
		return
	}
	if errors.Is(err, ErrRecipientNotMemberOrFormer) {
		httpapi.WriteError(w, http.StatusConflict, "recipient_not_member_or_former", err.Error())
		return
	}
	if writeAccountError(w, err) {
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not create movement")
		return
	}
	if body.Login != "" {
		m.UserLogin = &body.Login
	}
	if recordedByLogin, err := h.users.LoginForUserID(r.Context(), recordedBy); err == nil {
		m.RecordedByLogin = &recordedByLogin
	}
	if item, itemErr := h.service.GetItem(r.Context(), m.ItemID); itemErr == nil {
		text := fmt.Sprintf("🆕 %s ha registrado un %s de «%s» (x%d)",
			h.actorLogin(r.Context()), movementVerb(m.Type), itemLabel(item), m.Quantity)
		if m.UserLogin != nil {
			text += fmt.Sprintf(" a %s", *m.UserLogin)
		}
		httpapi.NotifyAsync(h.notifier, text+".")
	}
	httpapi.WriteJSON(w, http.StatusCreated, m)
}

// writeAccountError answers with the status and code of an error about the accounts of a sale or a
// rental, and says whether it was one.
func writeAccountError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrAccountRequired):
		httpapi.WriteError(w, http.StatusBadRequest, "account_required", err.Error())
	case errors.Is(err, ErrUnknownAccount):
		httpapi.WriteError(w, http.StatusBadRequest, "unknown_account", err.Error())
	case errors.Is(err, ErrAccountNotAsset):
		httpapi.WriteError(w, http.StatusBadRequest, "account_not_asset", err.Error())
	case errors.Is(err, ErrAccountHasChildren):
		httpapi.WriteError(w, http.StatusConflict, "account_has_children", err.Error())
	case errors.Is(err, ErrNoMaterialAccount):
		httpapi.WriteError(w, http.StatusConflict, "no_material_account", err.Error())
	default:
		return false
	}
	return true
}

// returnMovement closes an active rental — the only type that still
// reaches status 'active'. A loan's return is a separate 'return'
// movement created via POST /movements instead (see createMovement).
// deleteMovement removes a movement recorded by mistake (see Repository.DeleteMovement).
func (h *Handler) deleteMovement(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	m, err := h.service.DeleteMovement(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "movement not found")
		return
	case errors.Is(err, ErrMovementNeeded):
		httpapi.WriteError(w, http.StatusConflict, "movement_needed", err.Error())
		return
	case errors.Is(err, ErrMovementTransactionHasFiles):
		httpapi.WriteError(w, http.StatusConflict, "transaction_has_files", err.Error())
		return
	case err != nil:
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not delete movement")
		return
	}
	if item, itemErr := h.service.GetItem(r.Context(), m.ItemID); itemErr == nil {
		httpapi.NotifyAsync(h.notifier, fmt.Sprintf("🗑️ %s ha borrado un movimiento de «%s»: %s, x%d.",
			h.actorLogin(r.Context()), itemLabel(item), movementVerb(m.Type), m.Quantity))
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) returnMovement(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_id", "id must be numeric")
		return
	}
	m, err := h.service.ReturnMovement(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", "active movement not found")
		return
	}
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "could not return movement")
		return
	}
	if m.UserID != nil {
		if login, err := h.users.LoginForUserID(r.Context(), *m.UserID); err == nil {
			m.UserLogin = &login
		}
	}
	if m.RecordedBy != nil {
		if login, err := h.users.LoginForUserID(r.Context(), *m.RecordedBy); err == nil {
			m.RecordedByLogin = &login
		}
	}
	if item, itemErr := h.service.GetItem(r.Context(), m.ItemID); itemErr == nil {
		text := fmt.Sprintf("✏️ %s ha registrado la devolución del alquiler de «%s»",
			h.actorLogin(r.Context()), itemLabel(item))
		if m.UserLogin != nil {
			text += fmt.Sprintf(" (alquilado a %s)", *m.UserLogin)
		}
		httpapi.NotifyAsync(h.notifier, text+".")
	}
	httpapi.WriteJSON(w, http.StatusOK, m)
}
