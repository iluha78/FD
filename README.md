# FD — Face Detection & Recognition Service

Real-time face detection, recognition, gender/age estimation from video streams (RTSP, YouTube, HTTP).

## Architecture

```
┌──────────┐     NATS      ┌──────────┐     NATS      ┌──────────┐
│   API    │──(control)───>│ Ingestor │──(frames)───>│  Worker  │
│  :8080   │<──(events)────│  :8081   │               │  :8082   │
└────┬─────┘               └────┬─────┘               └────┬─────┘
     │                          │                          │
     ├── PostgreSQL             ├── MinIO (frames)         ├── ONNX Runtime
     ├── MinIO (snapshots)      ├── FFmpeg                 ├── PostgreSQL
     └── WebSocket              └── yt-dlp (YouTube)       └── MinIO
```

- **API** — REST API (Gin), WebSocket events, face search
- **Ingestor** — captures video frames via FFmpeg, publishes to NATS
- **Worker** — ML inference: detect faces, extract embeddings, predict age/gender, match against DB

## Requirements

- **Go 1.23+**
- **Docker & Docker Compose**
- **GCC/MinGW** (for CGO — onnxruntime_go bindings)
- **FFmpeg** (in PATH or installed in WSL)
- **yt-dlp** (optional, for YouTube streams)
- **ONNX Runtime 1.23.x** (`libonnxruntime.so` / `onnxruntime.dll`)

## Quick Start

### 1. Clone and configure

```bash
git clone <repo-url> && cd FD
cp .env.example .env
```

### 2. Download ML models

```bash
# Linux / WSL / Git Bash:
bash scripts/download_models.sh ./models

# Or manually download InsightFace buffalo_l pack and place in ./models/:
#   det_10g.onnx      — RetinaFace (face detection)
#   w600k_r50.onnx    — ArcFace (face recognition, 512-dim)
#   genderage.onnx    — Gender + Age prediction
```

### 3. Start infrastructure

```bash
make infra
# Starts: PostgreSQL (pgvector), NATS (JetStream), MinIO, Prometheus, Grafana
# DB migrations apply automatically on first run
```

Wait until all containers are healthy:

```bash
docker ps
# fd-postgres   Up (healthy)
# fd-nats       Up (healthy)
# fd-minio      Up (healthy)
# fd-prometheus  Up
# fd-grafana    Up
```

### 4. Install ONNX Runtime

**Linux / WSL:**
```bash
# Download and install
wget https://github.com/microsoft/onnxruntime/releases/download/v1.23.1/onnxruntime-linux-x64-1.23.1.tgz
tar xzf onnxruntime-linux-x64-1.23.1.tgz
sudo cp onnxruntime-linux-x64-1.23.1/lib/libonnxruntime.so* /usr/local/lib/
sudo ldconfig
```

**Windows:**
Place `onnxruntime.dll` in the project root or add its directory to PATH.

### 5. Install FFmpeg and yt-dlp

**Linux / WSL:**
```bash
sudo apt update && sudo apt install -y ffmpeg
# yt-dlp (for YouTube streams):
sudo curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp
sudo chmod +x /usr/local/bin/yt-dlp
```

**Windows:**
```powershell
winget install Gyan.FFmpeg
winget install yt-dlp.yt-dlp
```

### 6. Run services

Open 3 terminals:

```bash
# Terminal 1 — Worker (start first, creates NATS streams)
make run-worker

# Terminal 2 — Ingestor
make run-ingestor

# Terminal 3 — API
make run-api
```

All services should show `"ensured NATS stream"` and start listening on their ports.

## Usage

All API calls require header: `X-API-Key: changeme`

### Create a collection

```bash
curl -X POST http://localhost:8080/v1/collections \
  -H "X-API-Key: changeme" \
  -H "Content-Type: application/json" \
  -d '{"name": "office"}'
```

### Add a video stream

**RTSP camera:**
```bash
curl -X POST http://localhost:8080/v1/streams \
  -H "X-API-Key: changeme" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "rtsp://user:pass@camera-ip:554/stream",
    "type": "rtsp",
    "mode": "all",
    "fps": 5,
    "collection_id": "<collection-uuid>"
  }'
```

**YouTube stream:**
```bash
curl -X POST http://localhost:8080/v1/streams \
  -H "X-API-Key: changeme" \
  -H "Content-Type: application/json" \
  -d '{
    "url": "https://www.youtube.com/watch?v=VIDEO_ID",
    "type": "youtube",
    "mode": "all",
    "fps": 5,
    "collection_id": "<collection-uuid>"
  }'
```

### Start/Stop a stream

```bash
# Start
curl -X POST http://localhost:8080/v1/streams/<stream-id>/start \
  -H "X-API-Key: changeme"

# Stop
curl -X POST http://localhost:8080/v1/streams/<stream-id>/stop \
  -H "X-API-Key: changeme"
```

### View detected faces (events)

```bash
curl http://localhost:8080/v1/streams/<stream-id>/events \
  -H "X-API-Key: changeme"
```

Response includes for each detection:
- `gender` — "male" / "female"
- `gender_confidence` — 0.0-1.0
- `age` — estimated age
- `age_range` — e.g. "25-30"
- `confidence` — face detection confidence
- `matched_person_id` — UUID if face matched a known person
- `match_score` — cosine similarity score
- `snapshot_url` — URL to face crop image (`GET /v1/events/:id/snapshot`)
- `frame_url` — URL to full camera frame (`GET /v1/events/:id/frame`)
- `track_id` — track identifier, use for `/v1/events/similar`

### Get face snapshot

```bash
curl http://localhost:8080/v1/events/<event-id>/snapshot \
  -H "X-API-Key: changeme" \
  --output face.jpg
```

### Get full frame

Returns the full camera frame in which the face was detected.

```bash
curl http://localhost:8080/v1/events/<event-id>/frame \
  -H "X-API-Key: changeme" \
  --output frame.jpg
```

### Search events by face photo

Upload a face photo — returns all past detection events where this face appeared.

```bash
# Across all streams
curl -X POST http://localhost:8080/v1/search/events \
  -H "X-API-Key: changeme" \
  -F "image=@photo.jpg"

# Filter by stream, custom threshold and limit
curl -X POST "http://localhost:8080/v1/search/events?stream_id=<uuid>&threshold=0.5&limit=20" \
  -H "X-API-Key: changeme" \
  -F "image=@photo.jpg"
```

Response:
```json
{
  "results": [
    {
      "event_id": "...",
      "stream_id": "...",
      "timestamp": "2026-02-19T10:00:00Z",
      "score": 0.82,
      "gender": "female",
      "age": 28,
      "age_range": "25-30",
      "snapshot_url": "/v1/events/.../snapshot"
    }
  ],
  "total": 3
}
```

### Find similar faces by track_id

Find all events across all streams where the same face appeared, identified by a `track_id` from a known event.

```bash
curl "http://localhost:8080/v1/events/similar?stream_id=<uuid>&track_id=<track_id>" \
  -H "X-API-Key: changeme"

# With custom threshold and limit
curl "http://localhost:8080/v1/events/similar?stream_id=<uuid>&track_id=<track_id>&threshold=0.5&limit=20" \
  -H "X-API-Key: changeme"
```

`track_id` is available in the events list response (`GET /v1/streams/:id/events`).

### Add a known person with face

```bash
# Create person
curl -X POST http://localhost:8080/v1/persons \
  -H "X-API-Key: changeme" \
  -H "Content-Type: application/json" \
  -d '{"name": "John Doe", "collection_id": "<collection-uuid>"}'

# Upload face photo
curl -X POST http://localhost:8080/v1/persons/<person-id>/faces \
  -H "X-API-Key: changeme" \
  -F "image=@photo.jpg"
```

### List persons

```bash
# All persons
curl http://localhost:8080/v1/persons \
  -H "X-API-Key: changeme"

# Filter by collection
curl "http://localhost:8080/v1/persons?collection_id=<collection-uuid>" \
  -H "X-API-Key: changeme"
```

Response:
```json
{
  "persons": [
    {
      "id": "...",
      "name": "John Doe",
      "collection_id": "...",
      "face_count": 2,
      "created_at": "2026-02-18T12:00:00Z"
    }
  ],
  "total": 1
}
```

### Get a person

```bash
curl http://localhost:8080/v1/persons/<person-id> \
  -H "X-API-Key: changeme"
```

### List faces of a person

```bash
curl http://localhost:8080/v1/persons/<person-id>/faces \
  -H "X-API-Key: changeme"
```

### Delete a face

```bash
curl -X DELETE http://localhost:8080/v1/persons/<person-id>/faces/<face-id> \
  -H "X-API-Key: changeme"
```

### Search persons by face photo

Upload a face photo — returns matching persons from the library.

```bash
# Across all collections
curl -X POST http://localhost:8080/v1/search \
  -H "X-API-Key: changeme" \
  -F "image=@unknown.jpg"

# Filter by collection
curl -X POST http://localhost:8080/v1/search \
  -H "X-API-Key: changeme" \
  -F "image=@unknown.jpg" \
  -F "collection_id=<uuid>"
```

Response:
```json
{
  "results": [
    { "person_id": "...", "name": "John Doe", "score": 0.91 }
  ],
  "total": 1
}
```

### WebSocket (real-time events)

```
ws://localhost:8080/v1/ws?stream_id=<stream-id>
```

Events arrive as JSON:
```json
{
  "type": "face_detected",
  "stream_id": "...",
  "data": {
    "track_id": "...",
    "gender": "male",
    "age": 32,
    "matched_person": "John Doe",
    "confidence": 0.87
  }
}
```

## Stream Modes

- `"all"` — detect all faces, estimate gender/age, try to match against DB
- `"identify"` — only report faces that match known persons in the collection

## Configuration

Edit `configs/config.yaml`:

```yaml
server:
  port: 8080
  api_key: "changeme"

database:
  host: localhost
  port: 5432
  name: fd
  user: fd
  password: fd

nats:
  url: nats://localhost:4222

minio:
  endpoint: localhost:9000
  access_key: minioadmin
  secret_key: minioadmin
  bucket: fd-frames

vision:
  models_dir: ./models
  detection_threshold: 0.3    # min face confidence (0.0-1.0)
  recognition_threshold: 0.4  # min cosine similarity for match
  default_fps: 5              # frames per second from streams
  worker_count: 6             # parallel inference workers
  frame_width: 1920           # frame width for processing
  min_face_size: 20           # min face size in pixels (filters out tiny detections)

tracking:
  max_age: 30                 # frames before losing a track
  min_hits: 3                 # min detections to confirm track
  re_recognize_interval: 3s   # re-run recognition per track

storage:
  frame_retention: 1000       # keep last N frames per stream in MinIO (0 = keep all)
```

## Web Interfaces

| Service | URL | Credentials |
|---------|-----|-------------|
| MinIO Console | http://localhost:9001 | minioadmin / minioadmin |
| Prometheus | http://localhost:9090 | — |
| Grafana | http://localhost:3000 | admin / admin |
| NATS Monitor | http://localhost:8222 | — |

## Makefile Targets

```
make build            # Build all 3 services to bin/
make run-api          # Run API server
make run-ingestor     # Run Ingestor
make run-worker       # Run Vision Worker
make infra            # Start Docker infrastructure
make infra-down       # Stop Docker infrastructure
make migrate          # Apply DB migrations manually
make models           # Download ML models
make test             # Run tests
make lint             # Run linter
```

## ML Models

| Model | File | Purpose | Input | Output |
|-------|------|---------|-------|--------|
| RetinaFace | det_10g.onnx | Face detection | 640x640 RGB | Bounding boxes + landmarks |
| ArcFace | w600k_r50.onnx | Face recognition | 112x112 RGB | 512-dim embedding |
| GenderAge | genderage.onnx | Gender + Age | 96x96 RGB | 2 gender logits + age |

## Troubleshooting

**"context deadline exceeded" on startup**
Services started before NATS was ready. Built-in retry handles this (up to 30s). Just wait.

**"read frames: EOF"**
FFmpeg couldn't connect to stream. Check URL, ffmpeg availability, network. Ingestor retries up to 5 times.

**"vision pipeline not initialized"**
ONNX Runtime not found or models missing. Check `models/` directory and library path.

**All faces show age 0**
Outdated `attributes.go`. Ensure latest code with correct genderage output parsing.

**Face snapshots return 404**
MinIO might be down or bucket missing. Check `docker ps` and MinIO console.

**Disk fills up / MinIO growing too large**
Frames accumulate in MinIO. Enable auto-cleanup in `configs/config.yaml`:
```yaml
storage:
  frame_retention: 1000  # keep last 1000 frames per stream (0 = keep all)
```
Ingestor will purge oldest frames every 60 seconds automatically.

**Detections stopped after disk was full / Docker crashed**
NATS JetStream data files may be corrupted. Fix:
```powershell
docker compose -f deploy/docker-compose.yml down
docker volume rm deploy_nats_data
docker compose -f deploy/docker-compose.yml up -d
# Then restart api.exe and worker.exe
```
