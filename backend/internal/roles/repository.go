package roles

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/db"
)

var (
	ErrNotFound      = errors.New("role not found")
	ErrDuplicateName = errors.New("a role with this name already exists")
	ErrSystemRole    = errors.New("this role is built into the system and can't be deleted")
	// ErrAutomaticRole means the role is computed (member, former_member)
	// rather than stored, so it can't be granted or revoked by hand.
	ErrAutomaticRole = errors.New("this role is automatic and can't be granted or revoked by hand")
)

const (
	uniqueViolationCode     = "23505"
	foreignKeyViolationCode = "23503"
)

type Repository struct {
	pool db.PGX
}

func NewRepository(pool db.PGX) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) List(ctx context.Context) ([]Role, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, description, is_system, is_automatic FROM roles ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []Role{}
	for rows.Next() {
		var role Role
		if err := rows.Scan(&role.ID, &role.Name, &role.Description, &role.IsSystem, &role.IsAutomatic); err != nil {
			return nil, err
		}
		list = append(list, role)
	}
	return list, rows.Err()
}

func (r *Repository) GetByID(ctx context.Context, id int64) (Role, error) {
	var role Role
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, description, is_system, is_automatic FROM roles WHERE id = $1
	`, id).Scan(&role.ID, &role.Name, &role.Description, &role.IsSystem, &role.IsAutomatic)
	if errors.Is(err, pgx.ErrNoRows) {
		return role, ErrNotFound
	}
	return role, err
}

func (r *Repository) GetByName(ctx context.Context, name string) (Role, error) {
	var role Role
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, description, is_system, is_automatic FROM roles WHERE name = $1
	`, name).Scan(&role.ID, &role.Name, &role.Description, &role.IsSystem, &role.IsAutomatic)
	if errors.Is(err, pgx.ErrNoRows) {
		return role, ErrNotFound
	}
	return role, err
}

func (r *Repository) Create(ctx context.Context, in CreateInput) (Role, error) {
	var role Role
	err := r.pool.QueryRow(ctx, `
		INSERT INTO roles (name, description) VALUES ($1, $2)
		RETURNING id, name, description, is_system, is_automatic
	`, in.Name, in.Description).Scan(&role.ID, &role.Name, &role.Description, &role.IsSystem, &role.IsAutomatic)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return role, ErrDuplicateName
	}
	return role, err
}

// Delete refuses to remove a system role (admin/board/member); anything
// else is a plain delete that cascades out of every user_roles row that
// referenced it.
func (r *Repository) Delete(ctx context.Context, id int64) error {
	role, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if role.IsSystem {
		return ErrSystemRole
	}
	_, err = r.pool.Exec(ctx, `DELETE FROM roles WHERE id = $1`, id)
	return err
}

// ListNamesForUser is the single source of truth for "what roles does
// this person hold": everything stored in user_roles, plus "member" and
// "former_member" computed live from their membership history, and
// "board" computed live from their board_members history (see the
// package doc comment). It backs both the JWT roles claim issued at
// login and the roles shown on their profile, so the two can never
// disagree.
func (r *Repository) ListNamesForUser(ctx context.Context, userID int64) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT r.name FROM roles r
		JOIN user_roles ur ON ur.role_id = r.id
		WHERE ur.user_id = $1

		UNION

		SELECT 'member' WHERE EXISTS (
			SELECT 1 FROM memberships m WHERE m.user_id = $1 AND m.period @> CURRENT_DATE
		)

		UNION

		SELECT 'former_member' WHERE EXISTS (
			SELECT 1 FROM memberships m WHERE m.user_id = $1
		) AND NOT EXISTS (
			SELECT 1 FROM memberships m WHERE m.user_id = $1 AND m.period @> CURRENT_DATE
		)

		UNION

		SELECT 'board' WHERE EXISTS (
			SELECT 1 FROM board_members bm WHERE bm.user_id = $1 AND bm.period @> CURRENT_DATE
		)

		ORDER BY 1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (r *Repository) Assign(ctx context.Context, userID, roleID int64) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, userID, roleID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolationCode {
		return ErrNotFound
	}
	return err
}

func (r *Repository) Remove(ctx context.Context, userID, roleID int64) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM user_roles WHERE user_id = $1 AND role_id = $2
	`, userID, roleID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) CountUsersWithRoleName(ctx context.Context, name string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE r.name = $1
	`, name).Scan(&count)
	return count, err
}

func (r *Repository) UserHasRoleName(ctx context.Context, userID int64, name string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id
			WHERE ur.user_id = $1 AND r.name = $2
		)
	`, userID, name).Scan(&exists)
	return exists, err
}
