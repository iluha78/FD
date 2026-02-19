-- HNSW index for fast cosine similarity search on detection events
CREATE INDEX IF NOT EXISTS idx_events_embedding_hnsw ON events
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);
