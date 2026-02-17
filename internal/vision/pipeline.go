package vision

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/your-org/fd/internal/config"
	"github.com/your-org/fd/internal/models"
	"github.com/your-org/fd/internal/observability"
	"github.com/your-org/fd/internal/queue"
	"github.com/your-org/fd/internal/storage"
)

// Pipeline orchestrates the full vision processing:
// detect → track → embed → attributes → match → emit event.
type Pipeline struct {
	detector   *Detector
	embedder   *Embedder
	attributes *AttributePredictor
	trackers   map[uuid.UUID]*Tracker // per-stream trackers
	db         *storage.PostgresStore
	minio      *storage.MinIOStore
	producer   *queue.Producer
	cfg        config.VisionConfig
	trackCfg   config.TrackingConfig
}

// NewPipeline initialises all ONNX models and returns a ready pipeline.
func NewPipeline(
	cfg config.VisionConfig,
	trackCfg config.TrackingConfig,
	db *storage.PostgresStore,
	minio *storage.MinIOStore,
	producer *queue.Producer,
) (*Pipeline, error) {

	detPath := filepath.Join(cfg.ModelsDir, "det_10g.onnx")
	embPath := filepath.Join(cfg.ModelsDir, "w600k_r50.onnx")
	attrPath := filepath.Join(cfg.ModelsDir, "genderage.onnx")

	slog.Info("loading detection model", "path", detPath)
	det, err := NewDetector(detPath, float32(cfg.DetectionThreshold))
	if err != nil {
		return nil, fmt.Errorf("load detector: %w", err)
	}

	slog.Info("loading embedding model", "path", embPath)
	emb, err := NewEmbedder(embPath)
	if err != nil {
		det.Close()
		return nil, fmt.Errorf("load embedder: %w", err)
	}

	slog.Info("loading attribute model", "path", attrPath)
	attr, err := NewAttributePredictor(attrPath)
	if err != nil {
		det.Close()
		emb.Close()
		return nil, fmt.Errorf("load attributes: %w", err)
	}

	slog.Info("vision pipeline ready")

	return &Pipeline{
		detector:   det,
		embedder:   emb,
		attributes: attr,
		trackers:   make(map[uuid.UUID]*Tracker),
		db:         db,
		minio:      minio,
		producer:   producer,
		cfg:        cfg,
		trackCfg:   trackCfg,
	}, nil
}

// ProcessFrame handles one frame task: detect → track → embed → attrs → match → event.
func (p *Pipeline) ProcessFrame(ctx context.Context, task models.FrameTask) error {
	// 1. Load frame from MinIO
	frameData, err := p.minio.GetObject(ctx, task.FrameRef)
	if err != nil {
		return fmt.Errorf("load frame: %w", err)
	}

	// Decode JPEG
	img, err := jpeg.Decode(bytes.NewReader(frameData))
	if err != nil {
		return fmt.Errorf("decode jpeg: %w", err)
	}

	bounds := img.Bounds()
	origW := bounds.Dx()
	origH := bounds.Dy()

	// 2. Preprocess for detection
	start := time.Now()
	detInput := preprocessForDetection(img, p.detector.inputW, p.detector.inputH)
	observability.InferenceDuration.WithLabelValues("preprocess").Observe(time.Since(start).Seconds())

	// 3. Detect faces
	start = time.Now()
	detections, err := p.detector.Detect(detInput, origW, origH)
	if err != nil {
		return fmt.Errorf("detect: %w", err)
	}
	observability.InferenceDuration.WithLabelValues("detect").Observe(time.Since(start).Seconds())

	if len(detections) == 0 {
		return nil // No faces
	}

	observability.FacesDetected.WithLabelValues(task.StreamID.String()).Add(float64(len(detections)))

	// 4. Update tracker
	tracker := p.getTracker(task.StreamID)
	updates := tracker.Update(detections)

	// 5. For each tracked face that needs processing
	for _, upd := range updates {
		track := upd.Track

		needRecognition := tracker.ShouldRecognize(track, p.trackCfg.ReRecognizeInterval)
		if !needRecognition && !upd.IsNew {
			continue
		}

		// Crop face from original image
		faceCrop := cropFace(img, track.BBox)
		if faceCrop == nil {
			continue
		}

		// 6. Extract embedding
		start = time.Now()
		embInput := preprocessForEmbedding(faceCrop, p.embedder.inputW, p.embedder.inputH)
		embedding, err := p.embedder.Extract(embInput)
		if err != nil {
			slog.Warn("embed error", "error", err, "track", track.ID)
			continue
		}
		observability.InferenceDuration.WithLabelValues("embed").Observe(time.Since(start).Seconds())

		track.Embedding = embedding
		track.LastRecognized = time.Now()

		// 7. Predict gender/age
		start = time.Now()
		attrInput := preprocessForAttributes(faceCrop, p.attributes.inputW, p.attributes.inputH)
		ga, err := p.attributes.Predict(attrInput)
		if err != nil {
			slog.Warn("attributes error", "error", err, "track", track.ID)
		} else {
			track.Gender = ga.Gender
			track.GenderConf = ga.GenderConfidence
			track.FaceAge = ga.Age
			track.AgeRange = ga.AgeRange
		}
		observability.InferenceDuration.WithLabelValues("attrs").Observe(time.Since(start).Seconds())

		// 8. Match against DB
		var matchedPersonID *uuid.UUID
		var matchScore float32

		start = time.Now()
		matches, err := p.db.SearchFaces(ctx, embedding, nil, p.cfg.RecognitionThreshold, 1)
		if err != nil {
			slog.Warn("search error", "error", err)
		} else if len(matches) > 0 {
			matchedPersonID = &matches[0].PersonID
			matchScore = matches[0].Score
			track.PersonID = matches[0].PersonID.String()
			track.MatchScore = matchScore

			observability.FacesRecognized.WithLabelValues(task.StreamID.String()).Inc()
		}
		observability.InferenceDuration.WithLabelValues("match").Observe(time.Since(start).Seconds())

		// 9. Save face snapshot to MinIO
		snapshotKey := fmt.Sprintf("snapshots/%s/%s_%s.jpg",
			task.StreamID.String(), track.ID, time.Now().Format("20060102_150405"))
		snapshotData := encodeJPEG(faceCrop, 85)
		if err := p.minio.PutObject(ctx, snapshotKey, snapshotData, "image/jpeg"); err != nil {
			slog.Warn("save snapshot", "error", err)
			snapshotKey = ""
		}

		// 10. Publish detection event
		result := models.DetectionResult{
			StreamID:         task.StreamID,
			TrackID:          track.ID,
			Timestamp:        task.Timestamp,
			BBox:             track.BBox,
			Gender:           track.Gender,
			GenderConfidence: track.GenderConf,
			Age:              track.FaceAge,
			AgeRange:         track.AgeRange,
			Confidence:       track.Confidence,
			Embedding:        embedding,
			MatchedPersonID:  matchedPersonID,
			MatchScore:       matchScore,
			SnapshotKey:      snapshotKey,
		}

		if err := p.producer.PublishEvent(ctx, task.StreamID.String(), result); err != nil {
			slog.Error("publish event", "error", err, "track", track.ID)
		}
	}

	return nil
}

// EmbedImage extracts an embedding from a standalone image (for AddFace endpoint).
func (p *Pipeline) EmbedImage(imageData []byte) ([]float32, float32, error) {
	img, err := jpeg.Decode(bytes.NewReader(imageData))
	if err != nil {
		// Try other formats
		img, _, err = image.Decode(bytes.NewReader(imageData))
		if err != nil {
			return nil, 0, fmt.Errorf("decode image: %w", err)
		}
	}

	bounds := img.Bounds()
	origW := bounds.Dx()
	origH := bounds.Dy()

	// Detect face
	detInput := preprocessForDetection(img, p.detector.inputW, p.detector.inputH)
	detections, err := p.detector.Detect(detInput, origW, origH)
	if err != nil {
		return nil, 0, fmt.Errorf("detect: %w", err)
	}
	if len(detections) == 0 {
		return nil, 0, fmt.Errorf("no face detected in image")
	}

	// Use the highest confidence detection
	best := detections[0]
	for _, d := range detections[1:] {
		if d.Confidence > best.Confidence {
			best = d
		}
	}

	faceCrop := cropFace(img, best.BBox)
	if faceCrop == nil {
		return nil, 0, fmt.Errorf("failed to crop face")
	}

	embInput := preprocessForEmbedding(faceCrop, p.embedder.inputW, p.embedder.inputH)
	embedding, err := p.embedder.Extract(embInput)
	if err != nil {
		return nil, 0, fmt.Errorf("embed: %w", err)
	}

	return embedding, best.Confidence, nil
}

func (p *Pipeline) getTracker(streamID uuid.UUID) *Tracker {
	if t, ok := p.trackers[streamID]; ok {
		return t
	}
	t := NewTracker(streamID.String(), p.trackCfg.MaxAge, p.trackCfg.MinHits)
	p.trackers[streamID] = t
	return t
}

// Close releases all ONNX sessions.
func (p *Pipeline) Close() {
	if p.detector != nil {
		p.detector.Close()
	}
	if p.embedder != nil {
		p.embedder.Close()
	}
	if p.attributes != nil {
		p.attributes.Close()
	}
}

// --- Image preprocessing helpers ---

func preprocessForDetection(img image.Image, targetW, targetH int) []float32 {
	return imageToFloat32CHW(img, targetW, targetH, [3]float32{127.5, 127.5, 127.5}, [3]float32{128.0, 128.0, 128.0})
}

func preprocessForEmbedding(img image.Image, targetW, targetH int) []float32 {
	return imageToFloat32CHW(img, targetW, targetH, [3]float32{127.5, 127.5, 127.5}, [3]float32{127.5, 127.5, 127.5})
}

func preprocessForAttributes(img image.Image, targetW, targetH int) []float32 {
	return imageToFloat32CHW(img, targetW, targetH, [3]float32{0, 0, 0}, [3]float32{1, 1, 1})
}

// imageToFloat32CHW converts an image to CHW float32 format with normalization:
//
//	pixel = (pixel - mean) / std
func imageToFloat32CHW(img image.Image, targetW, targetH int, mean, std [3]float32) []float32 {
	resized := resizeImage(img, targetW, targetH)
	bounds := resized.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	data := make([]float32, 3*h*w)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, b, _ := resized.At(x+bounds.Min.X, y+bounds.Min.Y).RGBA()

			// Convert from 16-bit to 8-bit
			rf := float32(r >> 8)
			gf := float32(g >> 8)
			bf := float32(b >> 8)

			// CHW layout: [C][H][W]
			idx := y*w + x
			data[0*h*w+idx] = (rf - mean[0]) / std[0] // R
			data[1*h*w+idx] = (gf - mean[1]) / std[1] // G
			data[2*h*w+idx] = (bf - mean[2]) / std[2] // B
		}
	}

	return data
}

// resizeImage performs nearest-neighbour resize (fast, good enough for ML input).
func resizeImage(img image.Image, targetW, targetH int) image.Image {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))

	for y := 0; y < targetH; y++ {
		for x := 0; x < targetW; x++ {
			srcX := bounds.Min.X + x*srcW/targetW
			srcY := bounds.Min.Y + y*srcH/targetH
			dst.Set(x, y, img.At(srcX, srcY))
		}
	}

	return dst
}

// cropFace extracts a face region from the image given a bounding box.
func cropFace(img image.Image, bbox [4]float32) image.Image {
	bounds := img.Bounds()

	x1 := int(bbox[0])
	y1 := int(bbox[1])
	x2 := int(bbox[2])
	y2 := int(bbox[3])

	// Clamp to image bounds
	if x1 < bounds.Min.X {
		x1 = bounds.Min.X
	}
	if y1 < bounds.Min.Y {
		y1 = bounds.Min.Y
	}
	if x2 > bounds.Max.X {
		x2 = bounds.Max.X
	}
	if y2 > bounds.Max.Y {
		y2 = bounds.Max.Y
	}

	w := x2 - x1
	h := y2 - y1
	if w <= 0 || h <= 0 {
		return nil
	}

	// Add padding (20%)
	padW := int(float32(w) * 0.1)
	padH := int(float32(h) * 0.1)
	x1 -= padW
	y1 -= padH
	x2 += padW
	y2 += padH

	if x1 < bounds.Min.X {
		x1 = bounds.Min.X
	}
	if y1 < bounds.Min.Y {
		y1 = bounds.Min.Y
	}
	if x2 > bounds.Max.X {
		x2 = bounds.Max.X
	}
	if y2 > bounds.Max.Y {
		y2 = bounds.Max.Y
	}

	crop := image.NewRGBA(image.Rect(0, 0, x2-x1, y2-y1))
	for cy := y1; cy < y2; cy++ {
		for cx := x1; cx < x2; cx++ {
			crop.Set(cx-x1, cy-y1, img.At(cx, cy))
		}
	}

	return crop
}

// encodeJPEG encodes an image as JPEG with the given quality.
func encodeJPEG(img image.Image, quality int) []byte {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	return buf.Bytes()
}
