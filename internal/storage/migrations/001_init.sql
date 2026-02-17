-- FD: Face Detection & Recognition Service
-- Initial database schema

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "vector";

-- Collections of known faces
CREATE TABLE IF NOT EXISTS collections (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name        VARCHAR(255) NOT NULL UNIQUE,
    description TEXT DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Known persons
CREATE TABLE IF NOT EXISTS persons (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    collection_id UUID NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    name          VARCHAR(255) NOT NULL,
    metadata      JSONB DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_persons_collection ON persons(collection_id);

-- Face embeddings for known persons (multiple per person for accuracy)
CREATE TABLE IF NOT EXISTS face_embeddings (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    person_id  UUID NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    embedding  vector(512) NOT NULL,
    quality    REAL DEFAULT 0.0,
    source_key VARCHAR(512) DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_face_embeddings_person ON face_embeddings(person_id);

-- HNSW index for fast cosine similarity search
CREATE INDEX idx_face_embeddings_hnsw ON face_embeddings
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- Video streams configuration
CREATE TABLE IF NOT EXISTS streams (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    url           TEXT NOT NULL,
    stream_type   VARCHAR(50) NOT NULL DEFAULT 'rtsp',  -- rtsp, youtube, http
    mode          VARCHAR(50) NOT NULL DEFAULT 'all',    -- all, identify
    fps           INT NOT NULL DEFAULT 5,
    status        VARCHAR(50) NOT NULL DEFAULT 'stopped', -- stopped, starting, running, error
    collection_id UUID REFERENCES collections(id) ON DELETE SET NULL,
    config        JSONB DEFAULT '{}',
    error_message TEXT DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Detection/recognition events
CREATE TABLE IF NOT EXISTS events (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id         UUID NOT NULL REFERENCES streams(id) ON DELETE CASCADE,
    track_id          VARCHAR(100) NOT NULL DEFAULT '',
    timestamp         TIMESTAMPTZ NOT NULL,
    gender            VARCHAR(10) DEFAULT '',     -- male, female
    gender_confidence REAL DEFAULT 0.0,
    age               INT DEFAULT 0,
    age_range         VARCHAR(20) DEFAULT '',      -- e.g. "30-35"
    confidence        REAL DEFAULT 0.0,
    embedding         vector(512),
    matched_person_id UUID REFERENCES persons(id) ON DELETE SET NULL,
    match_score       REAL DEFAULT 0.0,
    snapshot_key      VARCHAR(512) DEFAULT '',     -- MinIO object key
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_events_stream_ts ON events(stream_id, timestamp DESC);
CREATE INDEX idx_events_person ON events(matched_person_id) WHERE matched_person_id IS NOT NULL;
CREATE INDEX idx_events_track ON events(stream_id, track_id);

-- Function to auto-update updated_at
CREATE OR REPLACE FUNCTION update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_collections_updated_at
    BEFORE UPDATE ON collections
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER trg_persons_updated_at
    BEFORE UPDATE ON persons
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();

CREATE TRIGGER trg_streams_updated_at
    BEFORE UPDATE ON streams
    FOR EACH ROW EXECUTE FUNCTION update_updated_at();
