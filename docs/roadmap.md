# Pending work

What is planned and not done yet. Each item says what is wanted and the questions to settle before
building it; none of them is decided. When one is started its design goes to the usual documents
(`api.md`, `database.md`, `backend-internals.md`, `frontend.md`) and the item is removed from here.

## Book catalogue of the club's library

A catalogue of the books the club has.

- **Data of a book**: the title at least; probably more (year, author, publisher…).
- **Cover**: an image of each book.
- **Comments**: the users can comment on each book.
- **Other resources** attached to a book: documents and, perhaps, topographies or scans of some
  parts of it or all of it.
- **Loans**: a book can be lent to a member, with the date it goes out and the date it comes back.

To settle:

- Which fields are required and which optional (an ISBN? a shelf mark or physical location?).
- Who adds and edits books, and who may comment (every member? admin and board?).
- Loans: who records them (admin, board and the `material` role, as with material?), whether there
  is a due date besides the date it was returned, whether a book that is out can be asked for, and
  whether a title can have several copies that are lent one by one. Material loans
  (`material_movements`) are the nearest thing there is, but they are made to an activity and
  counted by stock; a book is lent to a person and comes back as a whole.
- How covers and resources are stored: the pattern already used for the memorias of activities and
  the files of transactions (metadata in Postgres, bytes on disk keyed by the row id, created once
  and never rewritten) is the one to reuse.
- The name. The library of the club already exists as `internal/library` and `/library`, a read-only
  tree of documents on disk; the catalogue needs another name (`books`, for instance) and the
  interface has to make clear which is which.

## Catalogue of places (caves, shafts, canyons…)

A catalogue of the places where the club goes, with plenty of information about each.

- **Data of a place**: type, coordinates, state, description, topographies and photos.
- **Activities**: a place can be associated with activities.
- **Users**: every user can consult them and add information to each one.

To settle:

- What "adding information" means (comments, photos, topographies, corrections to the data), whether
  it is published at once or reviewed by somebody (admin or board) and whether it keeps who added it
  and when, as the participants of an activity do (`added_by`, `added_at`).
- Who creates and edits the places themselves, and who may delete what others added.
- The types of place (a catalogue an admin edits, like the types of material?) and the states
  (active, closed, being equipped, gone…).
- The coordinates: the system (WGS84 latitude and longitude, and/or UTM), whether there is a map,
  and whether the location of some places should be hidden.
- Who sees what: the location of some places is better not made public.
- Photos and topographies: the same pattern for files as above, the types allowed and a maximum
  size.
- The link with `activities`: a table joining places and activities, shown on the page of each
  activity and on that of each place (what has been done there).
