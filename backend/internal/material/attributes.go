package material

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrInvalidAttribute means the values given for an item's attributes do not fit its type: an
	// attribute the type does not have, one given twice, an option of another attribute, or a value
	// of the wrong kind. The message says which.
	ErrInvalidAttribute = errors.New("invalid attribute value")
	// ErrMissingAttribute means a required attribute of the type has no value.
	ErrMissingAttribute = errors.New("a required attribute has no value")
	// ErrDescriptionRequired means the item's type requires a description and there is none.
	ErrDescriptionRequired = errors.New("the description is required for this type")
	// ErrDuplicateItem means another item is exactly the same: same type, make, model, code,
	// description and attributes.
	ErrDuplicateItem = errors.New("another item is exactly the same")
	// ErrWouldDuplicateItems means a change to an attribute (deleting it) would leave two items
	// of the type exactly the same.
	ErrWouldDuplicateItems = errors.New("the change would leave two items exactly the same")
	// ErrAttributeNotFound and ErrOptionNotFound mean there is no attribute or option with that id.
	ErrAttributeNotFound = errors.New("attribute not found")
	ErrOptionNotFound    = errors.New("option not found")
	// ErrDuplicateAttribute means the type already has an attribute with that name, and
	// ErrDuplicateOption that the attribute already has an option with that label.
	ErrDuplicateAttribute = errors.New("the type already has an attribute with this name")
	ErrDuplicateOption    = errors.New("the attribute already has an option with this label")
	// ErrBlankName means an attribute was given no name or an option no label.
	ErrBlankName = errors.New("the name is required")
	// ErrInvalidDataType means a data type that is neither text nor enum.
	ErrInvalidDataType = errors.New("the data type must be text or enum")
	// ErrNotEnum means options were given to an attribute that is not an enum one.
	ErrNotEnum = errors.New("only an enum attribute has options")
	// ErrOptionInUse means an option cannot be deleted because some item has it.
	ErrOptionInUse = errors.New("some item has this option")
	// ErrInvalidOrder means a list of attributes to reorder is not exactly those of the type.
	ErrInvalidOrder = errors.New("the list must have each attribute of the type exactly once")
)

// itemIdentityIndex is the unique index that keeps two items from being the same.
const itemIdentityIndex = "material_items_identity_key"

// querier is what both the pool and a transaction offer for reading.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// loadTypes reads the catalog of types with their attributes and options: every type, or the one
// with the given id. The types come in their order (position), the attributes in theirs, the
// options by label.
func loadTypes(ctx context.Context, q querier, typeID *int64) ([]MaterialType, error) {
	rows, err := q.Query(ctx, `
		SELECT id, name, kind, description_required, position FROM material_types
		WHERE $1::bigint IS NULL OR id = $1
		ORDER BY position, id`, typeID)
	if err != nil {
		return nil, err
	}
	types := []MaterialType{}
	for rows.Next() {
		var t MaterialType
		if err := rows.Scan(&t.ID, &t.Name, &t.Kind, &t.DescriptionRequired, &t.Position); err != nil {
			rows.Close()
			return nil, err
		}
		t.Attributes = []Attribute{}
		types = append(types, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	attrs, err := loadAttributes(ctx, q, `WHERE $1::bigint IS NULL OR a.type_id = $1`, typeID)
	if err != nil {
		return nil, err
	}
	for i := range types {
		for _, a := range attrs {
			if a.TypeID == types[i].ID {
				types[i].Attributes = append(types[i].Attributes, a)
			}
		}
	}
	return types, nil
}

// loadAttributes reads attributes with their options; where is a WHERE clause over the table
// aliased "a", with one parameter.
func loadAttributes(ctx context.Context, q querier, where string, arg any) ([]Attribute, error) {
	rows, err := q.Query(ctx, `
		SELECT a.id, a.type_id, a.name, a.data_type, a.required, a.position
		FROM material_type_attributes a `+where+` ORDER BY a.type_id, a.position, a.id`, arg)
	if err != nil {
		return nil, err
	}
	attrs := []Attribute{}
	for rows.Next() {
		var a Attribute
		if err := rows.Scan(&a.ID, &a.TypeID, &a.Name, &a.DataType, &a.Required, &a.Position); err != nil {
			rows.Close()
			return nil, err
		}
		if a.DataType == AttributeEnum {
			a.Options = []AttributeOption{}
		}
		attrs = append(attrs, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	orows, err := q.Query(ctx, `
		SELECT o.id, o.attribute_id, o.label
		FROM material_attribute_options o JOIN material_type_attributes a ON a.id = o.attribute_id
		`+where+` ORDER BY lower(o.label), o.id`, arg)
	if err != nil {
		return nil, err
	}
	defer orows.Close()
	for orows.Next() {
		var o AttributeOption
		var attributeID int64
		if err := orows.Scan(&o.ID, &attributeID, &o.Label); err != nil {
			return nil, err
		}
		for i := range attrs {
			if attrs[i].ID == attributeID {
				attrs[i].Options = append(attrs[i].Options, o)
			}
		}
	}
	return attrs, orows.Err()
}

// buildAttributes checks the values given for an item's attributes against its type's and turns
// them into what is stored: a map by attribute id, with a string for a text attribute and the
// option id for an enum one. A blank value is no value. Every required attribute must have one.
func buildAttributes(t MaterialType, values []AttributeValue) (map[string]any, error) {
	byID := make(map[int64]Attribute, len(t.Attributes))
	for _, a := range t.Attributes {
		byID[a.ID] = a
	}
	out := map[string]any{}
	seen := map[int64]bool{}
	for _, v := range values {
		a, ok := byID[v.AttributeID]
		if !ok {
			return nil, fmt.Errorf("attribute %d is not an attribute of the type %q: %w",
				v.AttributeID, t.Name, ErrInvalidAttribute)
		}
		if seen[a.ID] {
			return nil, fmt.Errorf("attribute %q is given twice: %w", a.Name, ErrInvalidAttribute)
		}
		seen[a.ID] = true
		key := strconv.FormatInt(a.ID, 10)
		text := strings.TrimSpace(v.Value)
		switch a.DataType {
		case AttributeText:
			if v.OptionID != nil {
				return nil, fmt.Errorf("attribute %q is text and takes no option_id: %w", a.Name, ErrInvalidAttribute)
			}
			if text != "" {
				out[key] = text
			}
		case AttributeEnum:
			if v.OptionID == nil {
				if text != "" {
					return nil, fmt.Errorf("attribute %q takes an option_id, not a value: %w", a.Name, ErrInvalidAttribute)
				}
				continue
			}
			if !slices.ContainsFunc(a.Options, func(o AttributeOption) bool { return o.ID == *v.OptionID }) {
				return nil, fmt.Errorf("option %d is not an option of attribute %q: %w", *v.OptionID, a.Name, ErrInvalidAttribute)
			}
			out[key] = *v.OptionID
		}
	}
	for _, a := range t.Attributes {
		if _, ok := out[strconv.FormatInt(a.ID, 10)]; a.Required && !ok {
			return nil, fmt.Errorf("attribute %q: %w", a.Name, ErrMissingAttribute)
		}
	}
	return out, nil
}

// checkItem validates what an item of the type may have — the description (required only of general
// material whose type says so) and the attributes — and returns the attributes as stored.
func checkItem(t MaterialType, description *string, values []AttributeValue) ([]byte, error) {
	if t.Kind == ItemKindGeneral && t.DescriptionRequired && description == nil {
		return nil, ErrDescriptionRequired
	}
	attrs, err := buildAttributes(t, values)
	if err != nil {
		return nil, err
	}
	return json.Marshal(attrs)
}

// decorate fills the derived fields of an item — its attribute values, name and whether it is
// incomplete — from its type's definition.
func decorate(it *Item, t MaterialType) {
	it.Attributes = []AttributeValue{}
	parts := []string{t.Name}
	if brandModel := strings.TrimSpace(strings.Join(nonNil(it.Brand, it.Model), " ")); brandModel != "" {
		parts = append(parts, brandModel)
	}
	missing := false
	for _, a := range t.Attributes {
		raw, ok := it.rawAttributes[strconv.FormatInt(a.ID, 10)]
		if !ok {
			missing = missing || a.Required
			continue
		}
		v := AttributeValue{AttributeID: a.ID, Name: a.Name}
		switch a.DataType {
		case AttributeText:
			if err := json.Unmarshal(raw, &v.Value); err != nil || v.Value == "" {
				continue
			}
		case AttributeEnum:
			var id int64
			if err := json.Unmarshal(raw, &id); err != nil {
				continue
			}
			i := slices.IndexFunc(a.Options, func(o AttributeOption) bool { return o.ID == id })
			if i < 0 {
				continue
			}
			v.OptionID, v.Value = &id, a.Options[i].Label
		}
		it.Attributes = append(it.Attributes, v)
		parts = append(parts, v.Value)
	}
	if it.Kind == ItemKindGeneral {
		if it.Description != nil {
			parts = append(parts, *it.Description)
		}
		it.Incomplete = missing || (t.DescriptionRequired && it.Description == nil)
	}
	it.DisplayName = strings.Join(parts, " · ")
}

func nonNil(ss ...*string) []string {
	out := []string{}
	for _, s := range ss {
		if s != nil {
			out = append(out, *s)
		}
	}
	return out
}

// decorateItems decorates a list of items with the catalog read once.
func (r *Repository) decorateItems(ctx context.Context, items []Item) error {
	types, err := loadTypes(ctx, r.pool, nil)
	if err != nil {
		return err
	}
	byID := make(map[int64]MaterialType, len(types))
	for _, t := range types {
		byID[t.ID] = t
	}
	for i := range items {
		decorate(&items[i], byID[items[i].TypeID])
	}
	return nil
}

// inTx runs f in its own (sub)transaction, so that a rejected write is rolled back on its own: under
// test, where every call shares one outer transaction, a violation would otherwise abort all of it.
func (r *Repository) inTx(ctx context.Context, f func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetMaterialType reads one type with its attributes.
func (r *Repository) GetMaterialType(ctx context.Context, id int64) (MaterialType, error) {
	types, err := loadTypes(ctx, r.pool, &id)
	if err != nil {
		return MaterialType{}, err
	}
	if len(types) == 0 {
		return MaterialType{}, ErrTypeNotFound
	}
	return types[0], nil
}

// GetAttribute reads one attribute with its options.
func (r *Repository) GetAttribute(ctx context.Context, id int64) (Attribute, error) {
	attrs, err := loadAttributes(ctx, r.pool, `WHERE a.id = $1`, id)
	if err != nil {
		return Attribute{}, err
	}
	if len(attrs) == 0 {
		return Attribute{}, ErrAttributeNotFound
	}
	return attrs[0], nil
}

// attributeError maps the database errors of the attribute and option writes to this package's.
func attributeError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == uniqueViolationCode && pgErr.ConstraintName == itemIdentityIndex:
			return ErrWouldDuplicateItems
		case pgErr.Code == uniqueViolationCode && strings.Contains(pgErr.ConstraintName, "attribute_options"):
			return ErrDuplicateOption
		case pgErr.Code == uniqueViolationCode:
			return ErrDuplicateAttribute
		}
	}
	return err
}

// CreateAttribute adds an attribute to the end of a general type's list, with its options if it is
// an enum one.
func (r *Repository) CreateAttribute(ctx context.Context, typeID int64, name string, dataType AttributeDataType,
	required bool, options []string) (Attribute, error) {
	if dataType != AttributeText && dataType != AttributeEnum {
		return Attribute{}, ErrInvalidDataType
	}
	if dataType == AttributeText && len(options) > 0 {
		return Attribute{}, ErrNotEnum
	}
	var id int64
	err := r.inTx(ctx, func(tx pgx.Tx) error {
		var kind ItemKind
		err := tx.QueryRow(ctx, `SELECT kind FROM material_types WHERE id = $1`, typeID).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTypeNotFound
		}
		if err != nil {
			return err
		}
		if kind != ItemKindGeneral {
			return ErrFixedType
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO material_type_attributes (type_id, name, data_type, required, position)
			VALUES ($1, $2, $3, $4,
				COALESCE((SELECT max(position) + 1 FROM material_type_attributes WHERE type_id = $1), 0))
			RETURNING id`, typeID, name, dataType, required).Scan(&id); err != nil {
			return attributeError(err)
		}
		for _, label := range options {
			if _, err := tx.Exec(ctx, `
				INSERT INTO material_attribute_options (attribute_id, label) VALUES ($1, $2)`, id, label); err != nil {
				return attributeError(err)
			}
		}
		return nil
	})
	if err != nil {
		return Attribute{}, err
	}
	return r.GetAttribute(ctx, id)
}

// UpdateAttribute renames an attribute and/or changes whether it is required; nil leaves a field
// as it is. Its data type cannot change.
func (r *Repository) UpdateAttribute(ctx context.Context, id int64, name *string, required *bool) (Attribute, error) {
	err := r.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE material_type_attributes SET name = COALESCE($2, name), required = COALESCE($3, required)
			WHERE id = $1`, id, name, required)
		if err != nil {
			return attributeError(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrAttributeNotFound
		}
		return nil
	})
	if err != nil {
		return Attribute{}, err
	}
	return r.GetAttribute(ctx, id)
}

// DeleteAttribute removes an attribute and the value every item of its type has for it. If that
// would leave two items exactly the same, nothing changes and ErrWouldDuplicateItems says so.
func (r *Repository) DeleteAttribute(ctx context.Context, id int64) (Attribute, error) {
	attr, err := r.GetAttribute(ctx, id)
	if err != nil {
		return Attribute{}, err
	}
	err = r.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE material_items SET attributes = attributes - $2::text
			WHERE type_id = $1 AND attributes ? $2::text`, attr.TypeID, strconv.FormatInt(id, 10)); err != nil {
			return attributeError(err)
		}
		// Nothing cascades: the options go first.
		if _, err := tx.Exec(ctx, `DELETE FROM material_attribute_options WHERE attribute_id = $1`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM material_type_attributes WHERE id = $1`, id)
		return err
	})
	return attr, err
}

// ReorderAttributes sets the order of a type's attributes: ids must be all of them, once each.
func (r *Repository) ReorderAttributes(ctx context.Context, typeID int64, ids []int64) error {
	return r.inTx(ctx, func(tx pgx.Tx) error {
		t, err := loadTypes(ctx, tx, &typeID)
		if err != nil {
			return err
		}
		if len(t) == 0 {
			return ErrTypeNotFound
		}
		have := []int64{}
		for _, a := range t[0].Attributes {
			have = append(have, a.ID)
		}
		want := slices.Clone(ids)
		slices.Sort(have)
		slices.Sort(want)
		if !slices.Equal(have, want) {
			return ErrInvalidOrder
		}
		for position, id := range ids {
			if _, err := tx.Exec(ctx, `UPDATE material_type_attributes SET position = $2 WHERE id = $1`, id, position); err != nil {
				return err
			}
		}
		return nil
	})
}

// CreateOption adds an option to an enum attribute.
func (r *Repository) CreateOption(ctx context.Context, attributeID int64, label string) (AttributeOption, error) {
	var o AttributeOption
	err := r.inTx(ctx, func(tx pgx.Tx) error {
		var dataType AttributeDataType
		err := tx.QueryRow(ctx, `SELECT data_type FROM material_type_attributes WHERE id = $1`, attributeID).Scan(&dataType)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAttributeNotFound
		}
		if err != nil {
			return err
		}
		if dataType != AttributeEnum {
			return ErrNotEnum
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO material_attribute_options (attribute_id, label) VALUES ($1, $2)
			RETURNING id, label`, attributeID, label).Scan(&o.ID, &o.Label); err != nil {
			return attributeError(err)
		}
		return nil
	})
	return o, err
}

// GetOption reads an option with the attribute it belongs to.
func (r *Repository) GetOption(ctx context.Context, id int64) (AttributeOption, Attribute, error) {
	var o AttributeOption
	var attributeID int64
	err := r.pool.QueryRow(ctx, `SELECT id, label, attribute_id FROM material_attribute_options WHERE id = $1`, id).
		Scan(&o.ID, &o.Label, &attributeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, Attribute{}, ErrOptionNotFound
	}
	if err != nil {
		return o, Attribute{}, err
	}
	a, err := r.GetAttribute(ctx, attributeID)
	return o, a, err
}

// RenameOption changes an option's label; the items that have it follow, as they point at its id.
func (r *Repository) RenameOption(ctx context.Context, id int64, label string) (AttributeOption, error) {
	var o AttributeOption
	err := r.inTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `UPDATE material_attribute_options SET label = $2 WHERE id = $1 RETURNING id, label`, id, label).
			Scan(&o.ID, &o.Label)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOptionNotFound
		}
		return attributeError(err)
	})
	return o, err
}

// DeleteOption removes an option that no item has.
func (r *Repository) DeleteOption(ctx context.Context, id int64) error {
	return r.inTx(ctx, func(tx pgx.Tx) error {
		var attributeID, typeID int64
		err := tx.QueryRow(ctx, `
			SELECT a.id, a.type_id FROM material_attribute_options o
			JOIN material_type_attributes a ON a.id = o.attribute_id WHERE o.id = $1`, id).Scan(&attributeID, &typeID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOptionNotFound
		}
		if err != nil {
			return err
		}
		var inUse bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM material_items
				WHERE type_id = $1 AND attributes ->> $2::text = $3::text)`,
			typeID, strconv.FormatInt(attributeID, 10), strconv.FormatInt(id, 10)).Scan(&inUse); err != nil {
			return err
		}
		if inUse {
			return ErrOptionInUse
		}
		_, err = tx.Exec(ctx, `DELETE FROM material_attribute_options WHERE id = $1`, id)
		return err
	})
}
