# Pending work

What is planned and not done yet. Each item says what is wanted and the questions to settle before
building it; nothing in it is decided. When one is started its design goes to the usual documents
(`api.md`, `database.md`, `backend-internals.md`, `frontend.md`) and the item is removed from here.

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
