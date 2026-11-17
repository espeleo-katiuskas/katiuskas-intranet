-- +goose Up
-- A local copy of the elements of OpenStreetMap that matter to the club (docs/osm-integration.md): caves,
-- canyons, huts, via ferratas. The rows are a mirror: only the loader (cmd/osmsync) writes them, from the extracts
-- that Geofabrik publishes, so nothing in the intranet asks OpenStreetMap anything. They are kept in a
-- table apart from `places`, joined by the element's type and id, and a row that is gone from
-- OpenStreetMap is marked, never removed, since a place may point at it.
CREATE TABLE osm_elements (
    osm_type      text NOT NULL CHECK (osm_type IN ('node', 'way', 'relation')),
    osm_id        bigint NOT NULL,
    -- Of the element as downloaded, and when it was last edited there.
    version       integer NOT NULL,
    osm_timestamp timestamptz NOT NULL,
    -- What the filters of the loader say it is.
    category      text NOT NULL CHECK (category IN ('cave', 'canyon', 'hut', 'via_ferrata')),
    -- The point: the node itself, or one inside the shape of a way or a relation.
    lat           double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lon           double precision NOT NULL CHECK (lon BETWEEN -180 AND 180),
    -- All the tags as they are, and the GeoJSON of the outline of a way or a relation (null for a node).
    tags          jsonb NOT NULL,
    geometry      jsonb,
    -- A hash of the tags, the point and the outline: what tells a load that something changed even when
    -- the version did not (a node of a way that moved does not change the way's version).
    content_hash  text NOT NULL,
    -- The extract that last had it.
    source        text NOT NULL,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    -- When a load last confirmed it, and when one last found it different.
    fetched_at    timestamptz NOT NULL DEFAULT now(),
    changed_at    timestamptz NOT NULL DEFAULT now(),
    -- Set when it is no longer in its extract (deleted, or no longer matching): the row stays.
    deleted_at    timestamptz,
    PRIMARY KEY (osm_type, osm_id)
);
CREATE INDEX idx_osm_elements_lat_lon ON osm_elements (lat, lon) WHERE deleted_at IS NULL;
CREATE INDEX idx_osm_elements_source ON osm_elements (source) WHERE deleted_at IS NULL;

-- One row per load of an extract.
CREATE TABLE osm_sync_runs (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source           text NOT NULL,
    -- The date of the data in the extract (its replication timestamp).
    source_timestamp timestamptz,
    started_at       timestamptz NOT NULL DEFAULT now(),
    finished_at      timestamptz,
    status           text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'ok', 'failed')),
    added            integer NOT NULL DEFAULT 0,
    changed          integer NOT NULL DEFAULT 0,
    deleted          integer NOT NULL DEFAULT 0,
    unchanged        integer NOT NULL DEFAULT 0,
    error            text
);
CREATE INDEX idx_osm_sync_runs_source ON osm_sync_runs (source, started_at DESC);

-- The OpenStreetMap element a place is, when it is one: both or none, and at most one place per element.
ALTER TABLE places
    ADD COLUMN osm_type text,
    ADD COLUMN osm_id bigint,
    ADD CONSTRAINT places_osm_both_or_none CHECK ((osm_type IS NULL) = (osm_id IS NULL)),
    ADD CONSTRAINT places_osm_element_fkey FOREIGN KEY (osm_type, osm_id)
        REFERENCES osm_elements (osm_type, osm_id) ON DELETE RESTRICT,
    ADD CONSTRAINT places_osm_element_key UNIQUE (osm_type, osm_id);

-- +goose Down
ALTER TABLE places
    DROP CONSTRAINT places_osm_element_key,
    DROP CONSTRAINT places_osm_element_fkey,
    DROP CONSTRAINT places_osm_both_or_none,
    DROP COLUMN osm_id,
    DROP COLUMN osm_type;
DROP TABLE osm_sync_runs;
DROP TABLE osm_elements;
