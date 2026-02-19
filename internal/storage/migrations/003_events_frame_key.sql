-- Add frame_key column to events for linking back to the full frame in MinIO
ALTER TABLE events ADD COLUMN IF NOT EXISTS frame_key VARCHAR(512) DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_events_frame_key ON events(frame_key) WHERE frame_key != '';
