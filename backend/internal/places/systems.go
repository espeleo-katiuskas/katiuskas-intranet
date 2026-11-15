package places

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// ----- repository -----

// lockSystem locks the row of a system for the rest of the transaction, or says it is not there.
func lockSystem(ctx context.Context, tx pgx.Tx, id int64) error {
	var locked int64
	err := tx.QueryRow(ctx, `SELECT id FROM place_systems WHERE id = $1 FOR UPDATE`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSystemNotFound
	}
	return err
}

// touchSystem records who changed a system last, and when.
func touchSystem(ctx context.Context, tx pgx.Tx, id, userID int64) error {
	_, err := tx.Exec(ctx, `UPDATE place_systems SET updated_by = $2, updated_at = now() WHERE id = $1`, id, userID)
	return err
}

// SystemExists says whether a system has that id.
func (r *Repository) SystemExists(ctx context.Context, id int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM place_systems WHERE id = $1)`, id).Scan(&exists)
	return exists, err
}

// SystemNamed is the id of the system with that name, ignoring case, or 0.
func (r *Repository) SystemNamed(ctx context.Context, name string) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `SELECT id FROM place_systems WHERE lower(name) = lower($1)`, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// ListSystems is every system by name, each with its cavities by name.
func (r *Repository) ListSystems(ctx context.Context) ([]SystemListItem, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name FROM place_systems ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	list := []SystemListItem{}
	byID := map[int64]int{}
	for rows.Next() {
		var s SystemListItem
		if err := rows.Scan(&s.ID, &s.Name); err != nil {
			rows.Close()
			return nil, err
		}
		s.Places = []SystemPlaceRef{}
		byID[s.ID] = len(list)
		list = append(list, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	refs, err := r.pool.Query(ctx, `
		SELECT p.system_id, p.id, p.name, COALESCE(st.name, '')
		FROM places p LEFT JOIN place_subtypes st ON st.id = p.subtype_id
		WHERE p.system_id IS NOT NULL
		ORDER BY lower(p.name), p.id`)
	if err != nil {
		return nil, err
	}
	defer refs.Close()
	for refs.Next() {
		var systemID int64
		var ref SystemPlaceRef
		if err := refs.Scan(&systemID, &ref.ID, &ref.Name, &ref.Subtype); err != nil {
			return nil, err
		}
		if i, ok := byID[systemID]; ok {
			list[i].Places = append(list[i].Places, ref)
		}
	}
	return list, refs.Err()
}

// GetSystem is a system with its cavities, each with everything about it.
func (r *Repository) GetSystem(ctx context.Context, id int64) (System, error) {
	var s System
	err := r.pool.QueryRow(ctx, `
		SELECT s.id, s.name, s.description, cu.login, s.created_at, uu.login, s.updated_at,
			(SELECT count(*) FROM place_files f WHERE f.system_id = s.id)
		FROM place_systems s
		JOIN users cu ON cu.id = s.created_by
		JOIN users uu ON uu.id = s.updated_by
		WHERE s.id = $1`, id).Scan(&s.ID, &s.Name, &s.Description, &s.CreatedBy, &s.CreatedAt, &s.UpdatedBy,
		&s.UpdatedAt, &s.FileCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return System{}, ErrSystemNotFound
	}
	if err != nil {
		return System{}, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT`+listColumns+`, p.access, p.description, p.technical_sheet`+listFrom+`
		WHERE p.system_id = $1
		ORDER BY lower(p.name), p.id`, id)
	if err != nil {
		return System{}, err
	}
	defer rows.Close()
	s.Places = []SystemPlace{}
	for rows.Next() {
		var p SystemPlace
		targets := append(p.ListItem.scanTargets(), &p.Access, &p.Description, &p.TechnicalSheet)
		if err := rows.Scan(targets...); err != nil {
			return System{}, err
		}
		if p.AltNames == nil {
			p.AltNames = []string{}
		}
		s.Places = append(s.Places, p)
	}
	return s, rows.Err()
}

func (r *Repository) CreateSystem(ctx context.Context, userID int64, name string, description *string) (System, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO place_systems (name, description, created_by, updated_by) VALUES ($1, $2, $3, $3) RETURNING id
	`, name, description, userID).Scan(&id)
	if isUniqueViolation(err) {
		return System{}, ErrDuplicateSystem
	}
	if err != nil {
		return System{}, err
	}
	return r.GetSystem(ctx, id)
}

// UpdateSystem replaces the name and the description of a system, and who changed it and when.
func (r *Repository) UpdateSystem(ctx context.Context, id, userID int64, name string, description *string) (System, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE place_systems SET name = $2, description = $3, updated_by = $4, updated_at = now() WHERE id = $1
	`, id, name, description, userID)
	if isUniqueViolation(err) {
		return System{}, ErrDuplicateSystem
	}
	if err != nil {
		return System{}, err
	}
	if tag.RowsAffected() == 0 {
		return System{}, ErrSystemNotFound
	}
	return r.GetSystem(ctx, id)
}

// DeleteSystem removes a system with its files, in one transaction: its cavities stay, in no system. It
// answers with the ids of the files, so that the caller can remove their bytes.
func (r *Repository) DeleteSystem(ctx context.Context, id int64) ([]int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := lockSystem(ctx, tx, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE places SET system_id = NULL WHERE system_id = $1`, id); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `DELETE FROM place_files WHERE system_id = $1 RETURNING id`, id)
	if err != nil {
		return nil, err
	}
	var files []int64
	for rows.Next() {
		var fileID int64
		if err := rows.Scan(&fileID); err != nil {
			rows.Close()
			return nil, err
		}
		files = append(files, fileID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM place_systems WHERE id = $1`, id); err != nil {
		return nil, err
	}
	return files, tx.Commit(ctx)
}

// systemPlaceRefs are the cavities of a system, by name.
func systemPlaceRefs(ctx context.Context, q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, systemID int64) ([]SystemPlaceRef, error) {
	rows, err := q.Query(ctx, `
		SELECT p.id, p.name, COALESCE(st.name, '')
		FROM places p LEFT JOIN place_subtypes st ON st.id = p.subtype_id
		WHERE p.system_id = $1 ORDER BY lower(p.name), p.id`, systemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []SystemPlaceRef{}
	for rows.Next() {
		var ref SystemPlaceRef
		if err := rows.Scan(&ref.ID, &ref.Name, &ref.Subtype); err != nil {
			return nil, err
		}
		list = append(list, ref)
	}
	return list, rows.Err()
}

// SetSystemPlaces makes the cavities of a system exactly the given ones, in one transaction: the ones
// that are no longer there are left in no system, and the new ones leave the system they were in (a
// cavity is in one). It answers with the cavities as they were and as they are, to tell what changed.
// A place that is not there is ErrUnknownPlace and one that is not a cavity, ErrNotACavity.
func (r *Repository) SetSystemPlaces(ctx context.Context, id, userID int64, placeIDs []int64) (before, after []SystemPlaceRef, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	if err := lockSystem(ctx, tx, id); err != nil {
		return nil, nil, err
	}
	var found, caves int
	if err := tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE kind = 'cave') FROM places WHERE id = ANY($1)
	`, placeIDs).Scan(&found, &caves); err != nil {
		return nil, nil, err
	}
	if found != len(placeIDs) {
		return nil, nil, ErrUnknownPlace
	}
	if caves != len(placeIDs) {
		return nil, nil, ErrNotACavity
	}
	if before, err = systemPlaceRefs(ctx, tx, id); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE places SET system_id = NULL WHERE system_id = $1 AND NOT (id = ANY($2))`, id, placeIDs); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE places SET system_id = $1 WHERE id = ANY($2) AND system_id IS DISTINCT FROM $1`, id, placeIDs); err != nil {
		return nil, nil, err
	}
	if err := touchSystem(ctx, tx, id, userID); err != nil {
		return nil, nil, err
	}
	if after, err = systemPlaceRefs(ctx, tx, id); err != nil {
		return nil, nil, err
	}
	return before, after, tx.Commit(ctx)
}

// ----- service -----

func cleanSystemName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrBlankSystemName
	}
	if utf8.RuneCountInString(name) > MaxSystemNameLength {
		return "", ErrSystemNameLength
	}
	return name, nil
}

// freeSystemName says that no system other than `except` has the name (ignoring case): checked before
// the statement, which would otherwise fail and abort the transaction the tests run in.
func (s *Service) freeSystemName(ctx context.Context, name string, except int64) error {
	id, err := s.repo.SystemNamed(ctx, name)
	if err != nil {
		return err
	}
	if id != 0 && id != except {
		return ErrDuplicateSystem
	}
	return nil
}

func (s *Service) ListSystems(ctx context.Context) ([]SystemListItem, error) {
	return s.repo.ListSystems(ctx)
}

func (s *Service) GetSystem(ctx context.Context, id int64) (System, error) {
	return s.repo.GetSystem(ctx, id)
}

func (s *Service) CreateSystem(ctx context.Context, userID int64, name string, description *string) (System, error) {
	name, err := cleanSystemName(name)
	if err != nil {
		return System{}, err
	}
	if err := s.freeSystemName(ctx, name, 0); err != nil {
		return System{}, err
	}
	return s.repo.CreateSystem(ctx, userID, name, blankToNil(description))
}

func (s *Service) UpdateSystem(ctx context.Context, id, userID int64, name string, description *string) (System, error) {
	name, err := cleanSystemName(name)
	if err != nil {
		return System{}, err
	}
	if exists, err := s.repo.SystemExists(ctx, id); err != nil {
		return System{}, err
	} else if !exists {
		return System{}, ErrSystemNotFound
	}
	if err := s.freeSystemName(ctx, name, id); err != nil {
		return System{}, err
	}
	return s.repo.UpdateSystem(ctx, id, userID, name, blankToNil(description))
}

// DeleteSystem removes a system with its files; its cavities stay.
func (s *Service) DeleteSystem(ctx context.Context, id int64) error {
	files, err := s.repo.DeleteSystem(ctx, id)
	if err != nil {
		return err
	}
	for _, fileID := range files {
		s.removeBytes(id, fileID)
	}
	return nil
}

// SetSystemPlaces makes the cavities of a system exactly the given ones (repeated ids count once).
func (s *Service) SetSystemPlaces(ctx context.Context, id, userID int64, placeIDs []int64) (before, after []SystemPlaceRef, err error) {
	seen := map[int64]bool{}
	clean := make([]int64, 0, len(placeIDs))
	for _, placeID := range placeIDs {
		if !seen[placeID] {
			seen[placeID] = true
			clean = append(clean, placeID)
		}
	}
	return s.repo.SetSystemPlaces(ctx, id, userID, clean)
}
