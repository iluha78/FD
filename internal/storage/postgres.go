package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/your-org/fd/internal/config"
	"github.com/your-org/fd/internal/models"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(cfg config.DatabaseConfig) (*PostgresStore, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	poolCfg.MaxConns = int32(cfg.MaxConns)

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() {
	s.pool.Close()
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// --- Collections ---

func (s *PostgresStore) CreateCollection(ctx context.Context, name, description string) (*models.Collection, error) {
	c := &models.Collection{
		ID:          uuid.New(),
		Name:        name,
		Description: description,
	}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO collections (id, name, description) VALUES ($1, $2, $3) RETURNING created_at, updated_at`,
		c.ID, c.Name, c.Description,
	).Scan(&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create collection: %w", err)
	}
	return c, nil
}

func (s *PostgresStore) ListCollections(ctx context.Context) ([]models.Collection, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, description, created_at, updated_at FROM collections ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()

	var collections []models.Collection
	for rows.Next() {
		var c models.Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan collection: %w", err)
		}
		collections = append(collections, c)
	}
	return collections, nil
}

func (s *PostgresStore) GetCollection(ctx context.Context, id uuid.UUID) (*models.Collection, error) {
	c := &models.Collection{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, description, created_at, updated_at FROM collections WHERE id = $1`, id,
	).Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get collection: %w", err)
	}
	return c, nil
}

// --- Persons ---

func (s *PostgresStore) CreatePerson(ctx context.Context, collectionID uuid.UUID, name string, metadata json.RawMessage) (*models.Person, error) {
	if metadata == nil {
		metadata = json.RawMessage("{}")
	}
	p := &models.Person{
		ID:           uuid.New(),
		CollectionID: collectionID,
		Name:         name,
		Metadata:     metadata,
	}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO persons (id, collection_id, name, metadata) VALUES ($1, $2, $3, $4) RETURNING created_at, updated_at`,
		p.ID, p.CollectionID, p.Name, p.Metadata,
	).Scan(&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create person: %w", err)
	}
	return p, nil
}

func (s *PostgresStore) GetPerson(ctx context.Context, id uuid.UUID) (*models.Person, error) {
	p := &models.Person{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, collection_id, name, metadata, created_at, updated_at FROM persons WHERE id = $1`, id,
	).Scan(&p.ID, &p.CollectionID, &p.Name, &p.Metadata, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get person: %w", err)
	}
	return p, nil
}

func (s *PostgresStore) CountFaces(ctx context.Context, personID uuid.UUID) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM face_embeddings WHERE person_id = $1`, personID,
	).Scan(&count)
	return count, err
}

// --- Face Embeddings ---

func (s *PostgresStore) AddFaceEmbedding(ctx context.Context, personID uuid.UUID, embedding []float32, quality float32, sourceKey string) (*models.FaceEmbedding, error) {
	fe := &models.FaceEmbedding{
		ID:        uuid.New(),
		PersonID:  personID,
		Embedding: embedding,
		Quality:   quality,
		SourceKey: sourceKey,
	}
	vec := pgvector.NewVector(embedding)
	err := s.pool.QueryRow(ctx,
		`INSERT INTO face_embeddings (id, person_id, embedding, quality, source_key) VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
		fe.ID, fe.PersonID, vec, fe.Quality, fe.SourceKey,
	).Scan(&fe.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("add face embedding: %w", err)
	}
	return fe, nil
}

func (s *PostgresStore) DeleteFaceEmbedding(ctx context.Context, personID, faceID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM face_embeddings WHERE id = $1 AND person_id = $2`, faceID, personID)
	if err != nil {
		return fmt.Errorf("delete face embedding: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("face embedding not found")
	}
	return nil
}

func (s *PostgresStore) ListFaceEmbeddings(ctx context.Context, personID uuid.UUID) ([]models.FaceEmbedding, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, person_id, quality, source_key, created_at FROM face_embeddings WHERE person_id = $1 ORDER BY created_at DESC`,
		personID)
	if err != nil {
		return nil, fmt.Errorf("list face embeddings: %w", err)
	}
	defer rows.Close()

	var faces []models.FaceEmbedding
	for rows.Next() {
		var fe models.FaceEmbedding
		if err := rows.Scan(&fe.ID, &fe.PersonID, &fe.Quality, &fe.SourceKey, &fe.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan face embedding: %w", err)
		}
		faces = append(faces, fe)
	}
	return faces, nil
}

// SearchFaces finds the closest matching persons for a given embedding.
func (s *PostgresStore) SearchFaces(ctx context.Context, embedding []float32, collectionID *uuid.UUID, threshold float64, limit int) ([]SearchMatch, error) {
	if limit <= 0 {
		limit = 5
	}
	vec := pgvector.NewVector(embedding)

	var query string
	var args []interface{}

	if collectionID != nil {
		query = `
			SELECT fe.person_id, p.name, 1 - (fe.embedding <=> $1) AS score
			FROM face_embeddings fe
			JOIN persons p ON p.id = fe.person_id
			WHERE p.collection_id = $2
			  AND 1 - (fe.embedding <=> $1) >= $3
			ORDER BY fe.embedding <=> $1
			LIMIT $4`
		args = []interface{}{vec, *collectionID, threshold, limit}
	} else {
		query = `
			SELECT fe.person_id, p.name, 1 - (fe.embedding <=> $1) AS score
			FROM face_embeddings fe
			JOIN persons p ON p.id = fe.person_id
			WHERE 1 - (fe.embedding <=> $1) >= $2
			ORDER BY fe.embedding <=> $1
			LIMIT $3`
		args = []interface{}{vec, threshold, limit}
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search faces: %w", err)
	}
	defer rows.Close()

	var matches []SearchMatch
	for rows.Next() {
		var m SearchMatch
		if err := rows.Scan(&m.PersonID, &m.Name, &m.Score); err != nil {
			return nil, fmt.Errorf("scan search match: %w", err)
		}
		matches = append(matches, m)
	}
	return matches, nil
}

type SearchMatch struct {
	PersonID uuid.UUID `json:"person_id"`
	Name     string    `json:"name"`
	Score    float32   `json:"score"`
}

// --- Streams ---

func (s *PostgresStore) CreateStream(ctx context.Context, st *models.Stream) error {
	st.ID = uuid.New()
	st.Status = models.StreamStatusStopped
	if st.Config == nil {
		st.Config = json.RawMessage("{}")
	}
	return s.pool.QueryRow(ctx,
		`INSERT INTO streams (id, url, stream_type, mode, fps, status, collection_id, config)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING created_at, updated_at`,
		st.ID, st.URL, st.StreamType, st.Mode, st.FPS, st.Status, st.CollectionID, st.Config,
	).Scan(&st.CreatedAt, &st.UpdatedAt)
}

func (s *PostgresStore) GetStream(ctx context.Context, id uuid.UUID) (*models.Stream, error) {
	st := &models.Stream{}
	err := s.pool.QueryRow(ctx,
		`SELECT id, url, stream_type, mode, fps, status, collection_id, config, error_message, created_at, updated_at
		 FROM streams WHERE id = $1`, id,
	).Scan(&st.ID, &st.URL, &st.StreamType, &st.Mode, &st.FPS, &st.Status,
		&st.CollectionID, &st.Config, &st.ErrorMessage, &st.CreatedAt, &st.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get stream: %w", err)
	}
	return st, nil
}

func (s *PostgresStore) ListStreams(ctx context.Context) ([]models.Stream, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, url, stream_type, mode, fps, status, collection_id, config, error_message, created_at, updated_at
		 FROM streams ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list streams: %w", err)
	}
	defer rows.Close()

	var streams []models.Stream
	for rows.Next() {
		var st models.Stream
		if err := rows.Scan(&st.ID, &st.URL, &st.StreamType, &st.Mode, &st.FPS, &st.Status,
			&st.CollectionID, &st.Config, &st.ErrorMessage, &st.CreatedAt, &st.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan stream: %w", err)
		}
		streams = append(streams, st)
	}
	return streams, nil
}

func (s *PostgresStore) UpdateStreamStatus(ctx context.Context, id uuid.UUID, status models.StreamStatus, errMsg string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE streams SET status = $1, error_message = $2 WHERE id = $3`,
		status, errMsg, id)
	return err
}

func (s *PostgresStore) DeleteStream(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM streams WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete stream: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("stream not found")
	}
	return nil
}

// --- Events ---

func (s *PostgresStore) CreateEvent(ctx context.Context, ev *models.Event) error {
	ev.ID = uuid.New()
	ev.CreatedAt = time.Now()
	var vec *pgvector.Vector
	if len(ev.Embedding) > 0 {
		v := pgvector.NewVector(ev.Embedding)
		vec = &v
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO events (id, stream_id, track_id, timestamp, gender, gender_confidence, age, age_range, confidence, embedding, matched_person_id, match_score, snapshot_key, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		ev.ID, ev.StreamID, ev.TrackID, ev.Timestamp,
		ev.Gender, ev.GenderConfidence, ev.Age, ev.AgeRange, ev.Confidence,
		vec, ev.MatchedPersonID, ev.MatchScore, ev.SnapshotKey, ev.CreatedAt)
	return err
}

func (s *PostgresStore) QueryEvents(ctx context.Context, streamID uuid.UUID, from, to *time.Time, personID *uuid.UUID, unknown *bool, limit, offset int) ([]models.Event, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	baseWhere := "WHERE stream_id = $1"
	args := []interface{}{streamID}
	argIdx := 2

	if from != nil {
		baseWhere += fmt.Sprintf(" AND timestamp >= $%d", argIdx)
		args = append(args, *from)
		argIdx++
	}
	if to != nil {
		baseWhere += fmt.Sprintf(" AND timestamp <= $%d", argIdx)
		args = append(args, *to)
		argIdx++
	}
	if personID != nil {
		baseWhere += fmt.Sprintf(" AND matched_person_id = $%d", argIdx)
		args = append(args, *personID)
		argIdx++
	}
	if unknown != nil && *unknown {
		baseWhere += " AND matched_person_id IS NULL"
	}

	// Count total
	var total int
	countQuery := "SELECT COUNT(*) FROM events " + baseWhere
	if err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count events: %w", err)
	}

	// Fetch page
	query := fmt.Sprintf(
		`SELECT id, stream_id, track_id, timestamp, gender, gender_confidence, age, age_range, confidence, matched_person_id, match_score, snapshot_key, created_at
		 FROM events %s ORDER BY timestamp DESC LIMIT $%d OFFSET $%d`,
		baseWhere, argIdx, argIdx+1)
	args = append(args, limit, offset)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []models.Event
	for rows.Next() {
		var ev models.Event
		if err := rows.Scan(&ev.ID, &ev.StreamID, &ev.TrackID, &ev.Timestamp,
			&ev.Gender, &ev.GenderConfidence, &ev.Age, &ev.AgeRange, &ev.Confidence,
			&ev.MatchedPersonID, &ev.MatchScore, &ev.SnapshotKey, &ev.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, ev)
	}
	return events, total, nil
}

// GetEvent returns a single event by ID.
func (s *PostgresStore) GetEvent(ctx context.Context, id uuid.UUID) (*models.Event, error) {
	var ev models.Event
	err := s.pool.QueryRow(ctx,
		`SELECT id, stream_id, track_id, timestamp, gender, gender_confidence, age, age_range, confidence, matched_person_id, match_score, snapshot_key, created_at
		 FROM events WHERE id = $1`, id).
		Scan(&ev.ID, &ev.StreamID, &ev.TrackID, &ev.Timestamp,
			&ev.Gender, &ev.GenderConfidence, &ev.Age, &ev.AgeRange, &ev.Confidence,
			&ev.MatchedPersonID, &ev.MatchScore, &ev.SnapshotKey, &ev.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get event: %w", err)
	}
	return &ev, nil
}
