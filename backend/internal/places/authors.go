package places

import "context"

// UserLookup is what the places need of the people of the club, and nothing else: the API names a
// person by login only, so a login becomes an id on the way in and an id becomes a login on the way
// out. The places do not read the table of users: the repository keeps the ids of the authors
// (created_by, updated_by, uploaded_by) as plain references, and the Service names them through this
// interface, so the catalogue does not depend on how the club stores its people.
type UserLookup interface {
	LoginForUserID(ctx context.Context, id int64) (string, error)
	UserIDForLogin(ctx context.Context, login string) (int64, error)
	// LoginsForUserIDs gives the login of each id in one go; an id nobody has is left out.
	LoginsForUserIDs(ctx context.Context, ids []int64) (map[int64]string, error)
}

// HasAuthored says whether a person made anything in the catalogue of places (users.AuthorGuard).
func (s *Service) HasAuthored(ctx context.Context, userID int64) (bool, error) {
	return s.repo.HasAuthored(ctx, userID)
}

// loginsOf is the login of each of the people, one query for all. Zero ids are skipped.
func (s *Service) loginsOf(ctx context.Context, ids ...int64) (map[int64]string, error) {
	seen := make(map[int64]bool, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id != 0 && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return map[int64]string{}, nil
	}
	return s.users.LoginsForUserIDs(ctx, unique)
}

// namePlace fills in the logins of who created and last changed a place. It takes the error of the
// call that produced the place, so that a repository call can be returned through it directly.
func (s *Service) namePlace(ctx context.Context, p Place, err error) (Place, error) {
	if err != nil {
		return Place{}, err
	}
	logins, err := s.loginsOf(ctx, p.CreatedByID, p.UpdatedByID)
	if err != nil {
		return Place{}, err
	}
	p.CreatedBy, p.UpdatedBy = logins[p.CreatedByID], logins[p.UpdatedByID]
	if err := s.countActivities(ctx, &p.ListItem); err != nil {
		return Place{}, err
	}
	return p, nil
}

// nameSystem fills in the logins of the system and of the files of its cavities.
func (s *Service) nameSystem(ctx context.Context, sys System, err error) (System, error) {
	if err != nil {
		return System{}, err
	}
	ids := []int64{sys.CreatedByID, sys.UpdatedByID}
	for _, p := range sys.Places {
		for _, f := range p.Files {
			ids = append(ids, f.UploadedByID)
		}
	}
	logins, err := s.loginsOf(ctx, ids...)
	if err != nil {
		return System{}, err
	}
	sys.CreatedBy, sys.UpdatedBy = logins[sys.CreatedByID], logins[sys.UpdatedByID]
	cavities := make([]*ListItem, len(sys.Places))
	for i := range sys.Places {
		cavities[i] = &sys.Places[i].ListItem
	}
	if err := s.countActivities(ctx, cavities...); err != nil {
		return System{}, err
	}
	for i := range sys.Places {
		for j := range sys.Places[i].Files {
			f := &sys.Places[i].Files[j]
			f.UploadedBy = logins[f.UploadedByID]
		}
	}
	return sys, nil
}

// nameFiles fills in the login of whoever attached each file.
func (s *Service) nameFiles(ctx context.Context, files []File, err error) ([]File, error) {
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(files))
	for i, f := range files {
		ids[i] = f.UploadedByID
	}
	logins, err := s.loginsOf(ctx, ids...)
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].UploadedBy = logins[files[i].UploadedByID]
	}
	return files, nil
}

// nameFile is nameFiles for one.
func (s *Service) nameFile(ctx context.Context, f File, err error) (File, error) {
	if err != nil {
		return File{}, err
	}
	named, err := s.nameFiles(ctx, []File{f}, nil)
	if err != nil {
		return File{}, err
	}
	return named[0], nil
}
