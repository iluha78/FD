package vision

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	ort "github.com/yalue/onnxruntime_go"

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

	// Build session options to cap ORT thread usage per model session.
	// Each call to newSessionOptions() returns a fresh *ort.SessionOptions
	// that must be destroyed after the session is created.
	newSessionOptions := func() (*ort.SessionOptions, error) {
		opts, err := ort.NewSessionOptions()
		if err != nil {
			return nil, fmt.Errorf("create session options: %w", err)
		}
		if cfg.IntraOpThreads > 0 {
			if err := opts.SetIntraOpNumThreads(cfg.IntraOpThreads); err != nil {
				opts.Destroy()
				return nil, fmt.Errorf("set intra_op_threads: %w", err)
			}
		}
		if cfg.InterOpThreads > 0 {
			if err := opts.SetInterOpNumThreads(cfg.InterOpThreads); err != nil {
				opts.Destroy()
				return nil, fmt.Errorf("set inter_op_threads: %w", err)
			}
		}
		return opts, nil
	}

	slog.Info("loading detection model", "path", detPath,
		"intra_op_threads", cfg.IntraOpThreads, "inter_op_threads", cfg.InterOpThreads)
	detOpts, err := newSessionOptions()
	if err != nil {
		return nil, err
	}
	det, err := NewDetector(detPath, float32(cfg.DetectionThreshold), detOpts)
	detOpts.Destroy()
	if err != nil {
		return nil, fmt.Errorf("load detector: %w", err)
	}

	slog.Info("loading embedding model", "path", embPath)
	embOpts, err := newSessionOptions()
	if err != nil {
		det.Close()
		return nil, err
	}
	emb, err := NewEmbedder(embPath, embOpts)
	embOpts.Destroy()
	if err != nil {
		det.Close()
		return nil, fmt.Errorf("load embedder: %w", err)
	}

	slog.Info("loading attribute model", "path", attrPath)
	attrOpts, err := newSessionOptions()
	if err != nil {
		det.Close()
		emb.Close()
		return nil, err
	}
	attr, err := NewAttributePredictor(attrPath, attrOpts)
	attrOpts.Destroy()
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

	// Filter out faces smaller than min_face_size
	minSize := float32(p.cfg.MinFaceSize)
	if minSize > 0 {
		filtered := detections[:0]
		for _, d := range detections {
			w := d.BBox[2] - d.BBox[0]
			h := d.BBox[3] - d.BBox[1]
			if w >= minSize && h >= minSize {
				filtered = append(filtered, d)
			}
		}
		detections = filtered
		if len(detections) == 0 {
			return nil
		}
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
		matches, err := p.db.SearchFaces(ctx, embedding, task.CollectionID, p.cfg.RecognitionThreshold, 1)
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

		// 9. Save face snapshot to MinIO only on first sighting (avoid redundant writes)
		var snapshotKey string
		if upd.IsNew {
			snapshotKey = fmt.Sprintf("snapshots/%s/%s_%s.jpg",
				task.StreamID.String(), track.ID, time.Now().Format("20060102_150405"))
			snapshotImg := upscaleFace(faceCrop, 100)
			snapshotData := encodeJPEG(snapshotImg, 100)
			if err := p.minio.PutObject(ctx, snapshotKey, snapshotData, "image/jpeg"); err != nil {
				slog.Warn("save snapshot", "error", err)
				snapshotKey = ""
			}
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
			FrameKey:         task.FrameRef,
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

// imageToFloat32CHW resizes img to targetW×targetH and converts to CHW float32
// in a single pass, normalising as: pixel = (pixel - mean) / std.
// Direct pixel access avoids the image.Image interface overhead.
func imageToFloat32CHW(img image.Image, targetW, targetH int, mean, std [3]float32) []float32 {
	data := make([]float32, 3*targetH*targetW)
	planeSize := targetH * targetW

	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	minX := bounds.Min.X
	minY := bounds.Min.Y

	// Fast path: source is already *image.RGBA (most common after cropFace / SubImage)
	switch src := img.(type) {
	case *image.RGBA:
		for y := 0; y < targetH; y++ {
			srcY := minY + y*srcH/targetH
			for x := 0; x < targetW; x++ {
				srcX := minX + x*srcW/targetW
				off := src.PixOffset(srcX, srcY)
				pix := src.Pix[off : off+3 : off+3]
				idx := y*targetW + x
				data[idx] = (float32(pix[0]) - mean[0]) / std[0]           // R
				data[planeSize+idx] = (float32(pix[1]) - mean[1]) / std[1] // G
				data[2*planeSize+idx] = (float32(pix[2]) - mean[2]) / std[2] // B
			}
		}
	case *image.YCbCr:
		for y := 0; y < targetH; y++ {
			srcY := minY + y*srcH/targetH
			for x := 0; x < targetW; x++ {
				srcX := minX + x*srcW/targetW
				yi := src.YOffset(srcX, srcY)
				ci := src.COffset(srcX, srcY)
				r8, g8, b8 := color.YCbCrToRGB(src.Y[yi], src.Cb[ci], src.Cr[ci])
				idx := y*targetW + x
				data[idx] = (float32(r8) - mean[0]) / std[0]
				data[planeSize+idx] = (float32(g8) - mean[1]) / std[1]
				data[2*planeSize+idx] = (float32(b8) - mean[2]) / std[2]
			}
		}
	default:
		// Slow path: generic interface (handles NRGBA, Gray, etc.)
		for y := 0; y < targetH; y++ {
			srcY := minY + y*srcH/targetH
			for x := 0; x < targetW; x++ {
				srcX := minX + x*srcW/targetW
				r, g, b, _ := img.At(srcX, srcY).RGBA()
				idx := y*targetW + x
				data[idx] = (float32(r>>8) - mean[0]) / std[0]
				data[planeSize+idx] = (float32(g>>8) - mean[1]) / std[1]
				data[2*planeSize+idx] = (float32(b>>8) - mean[2]) / std[2]
			}
		}
	}

	return data
}

// resizeImage performs nearest-neighbour resize. Returns *image.RGBA.
// Kept for callers that need an image.Image result.
func resizeImage(img image.Image, targetW, targetH int) image.Image {
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))

	// Fast path for *image.RGBA source
	if src, ok := img.(*image.RGBA); ok {
		minX := bounds.Min.X
		minY := bounds.Min.Y
		for y := 0; y < targetH; y++ {
			srcY := minY + y*srcH/targetH
			for x := 0; x < targetW; x++ {
				srcX := minX + x*srcW/targetW
				sOff := src.PixOffset(srcX, srcY)
				dOff := dst.PixOffset(x, y)
				copy(dst.Pix[dOff:dOff+4], src.Pix[sOff:sOff+4])
			}
		}
		return dst
	}

	for y := 0; y < targetH; y++ {
		srcY := bounds.Min.Y + y*srcH/targetH
		for x := 0; x < targetW; x++ {
			srcX := bounds.Min.X + x*srcW/targetW
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

	rect := image.Rect(x1, y1, x2, y2)

	// Zero-copy path: SubImage shares the underlying pixel buffer.
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if si, ok := img.(subImager); ok {
		return si.SubImage(rect)
	}

	// Fallback: generic pixel copy for types that don't support SubImage.
	crop := image.NewRGBA(image.Rect(0, 0, x2-x1, y2-y1))
	for cy := y1; cy < y2; cy++ {
		for cx := x1; cx < x2; cx++ {
			crop.Set(cx-x1, cy-y1, img.At(cx, cy))
		}
	}
	return crop
}

// upscaleFace scales up a face crop so its shortest side is at least minSize pixels.
// If the crop is already large enough, it is returned as-is.
func upscaleFace(img image.Image, minSize int) image.Image {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	shortest := w
	if h < shortest {
		shortest = h
	}
	if shortest >= minSize {
		return img
	}

	scale := float64(minSize) / float64(shortest)
	newW := int(float64(w) * scale)
	newH := int(float64(h) * scale)

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			srcX := bounds.Min.X + x*w/newW
			srcY := bounds.Min.Y + y*h/newH
			dst.Set(x, y, img.At(srcX, srcY))
		}
	}
	return dst
}

// encodeJPEG encodes an image as JPEG with the given quality.
func encodeJPEG(img image.Image, quality int) []byte {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	return buf.Bytes()
}
