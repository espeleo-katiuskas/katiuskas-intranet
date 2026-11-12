package library

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/platform/clubtime"
)

// RoleLookup says whether a person holds a role, the automatic ones included — roles.Service
// satisfies it structurally (see main.go's wiring).
type RoleLookup interface {
	HasAnyRole(ctx context.Context, userID int64, names ...string) (bool, error)
}

type Service struct {
	repo    *Repository
	storage *Storage
	roles   RoleLookup
}

func NewService(repo *Repository, storage *Storage, roles RoleLookup) *Service {
	return &Service{repo: repo, storage: storage, roles: roles}
}

// blankToNil trims a text and gives nil for a blank one, so that "" is never stored as if it were a value.
func blankToNil(p *string) *string {
	if p == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*p)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func clean(in Input) (Input, error) {
	if in.TypeID <= 0 {
		return Input{}, ErrTypeRequired
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return Input{}, ErrBlankTitle
	}
	in.Author, in.Publisher, in.ISBN = blankToNil(in.Author), blankToNil(in.Publisher), blankToNil(in.ISBN)
	in.Location, in.Description = blankToNil(in.Location), blankToNil(in.Description)
	if in.Year != nil && (*in.Year < 1000 || *in.Year > 2200) {
		return Input{}, ErrInvalidYear
	}
	return in, nil
}

func (s *Service) List(ctx context.Context) ([]Item, error) { return s.repo.List(ctx) }

func (s *Service) Get(ctx context.Context, id int64) (Item, error) { return s.repo.Get(ctx, id) }

func (s *Service) Create(ctx context.Context, in Input) (Item, error) {
	in, err := clean(in)
	if err != nil {
		return Item{}, err
	}
	if err := s.checkType(ctx, in.TypeID); err != nil {
		return Item{}, err
	}
	return s.repo.Create(ctx, in)
}

func (s *Service) Update(ctx context.Context, id int64, in Input) (Item, error) {
	in, err := clean(in)
	if err != nil {
		return Item{}, err
	}
	if err := s.checkType(ctx, in.TypeID); err != nil {
		return Item{}, err
	}
	return s.repo.Update(ctx, id, in)
}

// checkType refuses a type that does not exist (the foreign key would too, but only by failing the
// statement).
func (s *Service) checkType(ctx context.Context, id int64) error {
	if _, err := s.repo.GetType(ctx, id); errors.Is(err, ErrTypeNotFound) {
		return ErrUnknownType
	} else if err != nil {
		return err
	}
	return nil
}

// ----- types -----

func (s *Service) ListTypes(ctx context.Context) ([]Type, error) { return s.repo.ListTypes(ctx) }

func (s *Service) GetType(ctx context.Context, id int64) (Type, error) {
	return s.repo.GetType(ctx, id)
}

// cleanTypeName trims the name and checks it is neither blank nor too long.
func cleanTypeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrBlankTypeName
	}
	if utf8.RuneCountInString(name) > MaxTypeNameLength {
		return "", ErrTypeNameTooLong
	}
	return name, nil
}

// freeTypeName says the name is not taken (ignoring case) by a type other than `except`.
func (s *Service) freeTypeName(ctx context.Context, name string, except int64) error {
	t, err := s.repo.TypeNamed(ctx, name)
	if errors.Is(err, ErrTypeNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if t.ID != except {
		return ErrDuplicateType
	}
	return nil
}

func (s *Service) CreateType(ctx context.Context, name string) (Type, error) {
	name, err := cleanTypeName(name)
	if err != nil {
		return Type{}, err
	}
	if err := s.freeTypeName(ctx, name, 0); err != nil {
		return Type{}, err
	}
	return s.repo.CreateType(ctx, name)
}

func (s *Service) RenameType(ctx context.Context, id int64, name string) (Type, error) {
	name, err := cleanTypeName(name)
	if err != nil {
		return Type{}, err
	}
	if _, err := s.repo.GetType(ctx, id); err != nil {
		return Type{}, err
	}
	if err := s.freeTypeName(ctx, name, id); err != nil {
		return Type{}, err
	}
	return s.repo.RenameType(ctx, id, name)
}

// DeleteType removes a type no item has.
func (s *Service) DeleteType(ctx context.Context, id int64) error {
	t, err := s.repo.GetType(ctx, id)
	if err != nil {
		return err
	}
	if t.ItemCount > 0 {
		return ErrTypeInUse
	}
	return s.repo.DeleteType(ctx, id)
}

// Delete removes an item that has never been lent, its comments and its files.
func (s *Service) Delete(ctx context.Context, id int64) error {
	files, err := s.repo.Delete(ctx, id)
	if err != nil {
		return err
	}
	for _, fileID := range files {
		s.removeBytes(id, fileID)
	}
	return nil
}

// removeBytes deletes a file's bytes once its row is gone: the row was the only thing that made the
// file exist for anybody, so failing to remove the bytes only leaves an unreferenced file behind and
// must not undo what was asked.
func (s *Service) removeBytes(itemID, resourceID int64) {
	if err := s.storage.Delete(itemID, resourceID); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Error("remove an item file from disk", "error", err, "item_id", itemID, "resource_id", resourceID)
	}
}

// ----- comments -----

func (s *Service) ListComments(ctx context.Context, itemID int64) ([]Comment, error) {
	if _, err := s.repo.Get(ctx, itemID); err != nil {
		return nil, err
	}
	return s.repo.ListComments(ctx, itemID)
}

func (s *Service) AddComment(ctx context.Context, itemID, userID int64, body string) (Comment, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Comment{}, ErrBlankComment
	}
	if utf8.RuneCountInString(body) > MaxCommentLength {
		return Comment{}, ErrCommentTooLong
	}
	if _, err := s.repo.Get(ctx, itemID); err != nil {
		return Comment{}, err
	}
	return s.repo.AddComment(ctx, itemID, userID, body)
}

// DeleteComment removes a comment: its author may, and so may whoever manages the library
// (privileged). It answers with the comment, for the notice.
func (s *Service) DeleteComment(ctx context.Context, itemID, id, requesterID int64, privileged bool) (Comment, error) {
	c, authorID, err := s.repo.GetComment(ctx, itemID, id)
	if err != nil {
		return Comment{}, err
	}
	if !privileged && authorID != requesterID {
		return Comment{}, ErrNotYours
	}
	if err := s.repo.DeleteComment(ctx, itemID, id); err != nil {
		return Comment{}, err
	}
	return c, nil
}

// ----- files -----

func cleanName(name string) string {
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	if name == "" || name == "." || name == "/" {
		return "file"
	}
	return name
}

func (s *Service) ListResources(ctx context.Context, itemID int64) ([]Resource, error) {
	if _, err := s.repo.Get(ctx, itemID); err != nil {
		return nil, err
	}
	return s.repo.ListResources(ctx, itemID)
}

// add stores a file: the row first, since the path of the bytes is keyed by its id, and the row is
// removed again if the save fails, so that a file whose bytes are not there never stays listed.
func (s *Service) add(ctx context.Context, itemID, uploadedBy int64, isCover bool, title *string, name string, content []byte) (Resource, error) {
	f, replaced, err := s.repo.CreateResource(ctx, itemID, uploadedBy, isCover, title, name, detectType(name, content), len(content))
	if err != nil {
		return Resource{}, err
	}
	if err := s.storage.Save(itemID, f.ID, bytes.NewReader(content)); err != nil {
		_ = s.repo.DeleteResource(ctx, itemID, f.ID)
		return Resource{}, err
	}
	if replaced != 0 {
		s.removeBytes(itemID, replaced)
	}
	return f, nil
}

// AddResource attaches a file (anything) to an item, with an optional caption.
func (s *Service) AddResource(ctx context.Context, itemID, uploadedBy int64, title *string, name string, content []byte) (Resource, error) {
	if len(content) == 0 {
		return Resource{}, ErrEmptyFile
	}
	title = blankToNil(title)
	if title != nil && utf8.RuneCountInString(*title) > MaxResourceTitleLength {
		return Resource{}, ErrTitleTooLong
	}
	if _, err := s.repo.Get(ctx, itemID); err != nil {
		return Resource{}, err
	}
	return s.add(ctx, itemID, uploadedBy, false, title, cleanName(name), content)
}

// SetCover gives the item its cover, in place of the one it had. It has to be an image, recognised by
// its bytes.
func (s *Service) SetCover(ctx context.Context, itemID, uploadedBy int64, name string, content []byte) (Resource, error) {
	if len(content) == 0 {
		return Resource{}, ErrEmptyFile
	}
	name = cleanName(name)
	if !isImage(detectType(name, content)) {
		return Resource{}, ErrNotAnImage
	}
	if _, err := s.repo.Get(ctx, itemID); err != nil {
		return Resource{}, err
	}
	return s.add(ctx, itemID, uploadedBy, true, nil, name, content)
}

// RemoveCover takes the cover off an item.
func (s *Service) RemoveCover(ctx context.Context, itemID int64) error {
	cover, err := s.repo.GetCover(ctx, itemID)
	if err != nil {
		return err
	}
	if err := s.repo.DeleteResource(ctx, itemID, cover.ID); err != nil {
		return err
	}
	s.removeBytes(itemID, cover.ID)
	return nil
}

// RemoveResource deletes a file: whoever attached it may, and so may whoever manages the library
// (privileged). The cover is not removed this way (RemoveCover). It answers with the file, for the
// notice.
func (s *Service) RemoveResource(ctx context.Context, itemID, id, requesterID int64, privileged bool) (Resource, error) {
	f, uploaderID, err := s.repo.GetResource(ctx, itemID, id)
	if err != nil || f.IsCover {
		if err == nil {
			err = ErrResourceNotFound
		}
		return Resource{}, err
	}
	if !privileged && uploaderID != requesterID {
		return Resource{}, ErrNotYours
	}
	if err := s.repo.DeleteResource(ctx, itemID, id); err != nil {
		return Resource{}, err
	}
	s.removeBytes(itemID, id)
	return f, nil
}

// OpenResource is a file's metadata and its open file, which the caller closes.
func (s *Service) OpenResource(ctx context.Context, itemID, id int64) (Resource, *os.File, error) {
	f, _, err := s.repo.GetResource(ctx, itemID, id)
	if err != nil {
		return Resource{}, nil, err
	}
	file, err := s.storage.Open(itemID, id)
	if err != nil {
		return Resource{}, nil, err
	}
	return f, file, nil
}

// OpenCover is the cover of the item and its open file, which the caller closes.
func (s *Service) OpenCover(ctx context.Context, itemID int64) (Resource, *os.File, error) {
	f, err := s.repo.GetCover(ctx, itemID)
	if err != nil {
		return Resource{}, nil, err
	}
	file, err := s.storage.Open(itemID, f.ID)
	if err != nil {
		return Resource{}, nil, err
	}
	return f, file, nil
}

// ----- loans -----

// day reads a date given by a caller, or today (the club's) when there is none.
func day(raw *string) (string, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return clubtime.Today(), nil
	}
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(*raw))
	if err != nil {
		return "", ErrInvalidDate
	}
	return parsed.Format("2006-01-02"), nil
}

func (s *Service) ListLoansOfItem(ctx context.Context, itemID int64) ([]Loan, error) {
	if _, err := s.repo.Get(ctx, itemID); err != nil {
		return nil, err
	}
	return s.repo.ListLoansOfItem(ctx, itemID)
}

func (s *Service) ListLoans(ctx context.Context, openOnly bool) ([]Loan, error) {
	return s.repo.ListLoans(ctx, openOnly)
}

// Lend records that an item goes out to a member, on the given day (today by default; never a day
// that has not come yet). Only somebody who is a member now can have an item.
func (s *Service) Lend(ctx context.Context, itemID, userID, recordedBy int64, lentAt *string, notes *string) (Loan, error) {
	date, err := day(lentAt)
	if err != nil {
		return Loan{}, err
	}
	if date > clubtime.Today() {
		return Loan{}, ErrInvalidDates
	}
	member, err := s.roles.HasAnyRole(ctx, userID, "member")
	if err != nil {
		return Loan{}, err
	}
	if !member {
		return Loan{}, ErrRecipientNotMember
	}
	return s.repo.CreateLoan(ctx, itemID, userID, recordedBy, date, blankToNil(notes))
}

// Return records that an item is back, on the given day (today by default): not before the loan began
// and never a day that has not come yet.
func (s *Service) Return(ctx context.Context, itemID, loanID int64, returnedAt *string) (Loan, error) {
	date, err := day(returnedAt)
	if err != nil {
		return Loan{}, err
	}
	if date > clubtime.Today() {
		return Loan{}, ErrInvalidDates
	}
	return s.repo.ReturnLoan(ctx, itemID, loanID, date)
}

func (s *Service) GetLoan(ctx context.Context, itemID, loanID int64) (Loan, error) {
	return s.repo.GetLoan(ctx, itemID, loanID)
}

func (s *Service) DeleteLoan(ctx context.Context, itemID, loanID int64) error {
	return s.repo.DeleteLoan(ctx, itemID, loanID)
}
