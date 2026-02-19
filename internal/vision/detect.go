package vision

import (
	"fmt"
	"math"
	"sort"

	ort "github.com/yalue/onnxruntime_go"
)

// Detection represents a detected face.
type Detection struct {
	BBox       [4]float32    // x1, y1, x2, y2 (pixel coordinates)
	Confidence float32
	Landmarks  [5][2]float32 // 5 facial landmarks (eyes, nose, mouth corners)
}

// Detector runs RetinaFace face detection using ONNX Runtime.
type Detector struct {
	session       *ort.AdvancedSession
	inputTensor   *ort.Tensor[float32]
	outputTensors []*ort.Tensor[float32]
	threshold     float32
	inputW        int
	inputH        int
}

// stride configuration for RetinaFace det_10g
var strides = []int{8, 16, 32}

// anchorsPerStride is the number of anchors per pixel at each stride
const anchorsPerStride = 2

// NewDetector loads the RetinaFace ONNX model.
// opts may be nil (ORT defaults) or a pre-configured *ort.SessionOptions.
func NewDetector(modelPath string, threshold float32, opts *ort.SessionOptions) (*Detector, error) {
	inputW, inputH := 640, 640

	inputShape := ort.NewShape(1, 3, int64(inputH), int64(inputW))
	inputTensor, err := ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		return nil, fmt.Errorf("create input tensor: %w", err)
	}

	// det_10g output shapes (NO batch dimension):
	// scores:    [12800,1] [3200,1] [800,1]     -> stride 8, 16, 32
	// bboxes:    [12800,4] [3200,4] [800,4]     -> stride 8, 16, 32
	// landmarks: [12800,10] [3200,10] [800,10]  -> stride 8, 16, 32
	//
	// 12800 = (640/8)*(640/8)*2   = 80*80*2
	// 3200  = (640/16)*(640/16)*2 = 40*40*2
	// 800   = (640/32)*(640/32)*2 = 20*20*2

	type outputSpec struct {
		name  string
		shape ort.Shape
	}

	outputs := []outputSpec{
		{"448", ort.NewShape(12800, 1)},  // scores stride 8
		{"471", ort.NewShape(3200, 1)},   // scores stride 16
		{"494", ort.NewShape(800, 1)},    // scores stride 32
		{"451", ort.NewShape(12800, 4)},  // bboxes stride 8
		{"474", ort.NewShape(3200, 4)},   // bboxes stride 16
		{"497", ort.NewShape(800, 4)},    // bboxes stride 32
		{"454", ort.NewShape(12800, 10)}, // landmarks stride 8
		{"477", ort.NewShape(3200, 10)},  // landmarks stride 16
		{"500", ort.NewShape(800, 10)},   // landmarks stride 32
	}

	outputNames := make([]string, len(outputs))
	outputTensors := make([]*ort.Tensor[float32], len(outputs))
	outputValues := make([]ort.Value, len(outputs))

	for i, spec := range outputs {
		outputNames[i] = spec.name
		t, err := ort.NewEmptyTensor[float32](spec.shape)
		if err != nil {
			// Cleanup already created tensors
			for j := 0; j < i; j++ {
				outputTensors[j].Destroy()
			}
			inputTensor.Destroy()
			return nil, fmt.Errorf("create output tensor %d (%s): %w", i, spec.name, err)
		}
		outputTensors[i] = t
		outputValues[i] = t
	}

	session, err := ort.NewAdvancedSession(modelPath,
		[]string{"input.1"},
		outputNames,
		[]ort.Value{inputTensor},
		outputValues,
		opts,
	)
	if err != nil {
		inputTensor.Destroy()
		for _, t := range outputTensors {
			t.Destroy()
		}
		return nil, fmt.Errorf("create detector session: %w", err)
	}

	return &Detector{
		session:       session,
		inputTensor:   inputTensor,
		outputTensors: outputTensors,
		threshold:     threshold,
		inputW:        inputW,
		inputH:        inputH,
	}, nil
}

// Detect runs face detection on a preprocessed image.
// imgData should be CHW format [3, inputH, inputW], normalized.
// origW/origH are the original image dimensions for coordinate scaling.
func (d *Detector) Detect(imgData []float32, origW, origH int) ([]Detection, error) {
	inputSlice := d.inputTensor.GetData()
	copy(inputSlice, imgData)

	if err := d.session.Run(); err != nil {
		return nil, fmt.Errorf("run detection: %w", err)
	}

	detections := d.parseDetections(origW, origH)
	detections = nms(detections, 0.4)

	return detections, nil
}

// parseDetections decodes anchor-based RetinaFace outputs at strides 8, 16, 32.
func (d *Detector) parseDetections(origW, origH int) []Detection {
	var detections []Detection

	scaleW := float32(origW) / float32(d.inputW)
	scaleH := float32(origH) / float32(d.inputH)

	for si, stride := range strides {
		scores := d.outputTensors[si].GetData()       // [N, 1]
		bboxes := d.outputTensors[si+3].GetData()     // [N, 4]
		landmarks := d.outputTensors[si+6].GetData()  // [N, 10]

		fmW := d.inputW / stride
		fmH := d.inputH / stride

		idx := 0
		for cy := 0; cy < fmH; cy++ {
			for cx := 0; cx < fmW; cx++ {
				for a := 0; a < anchorsPerStride; a++ {
					score := scores[idx]

					if score >= d.threshold {
						// Anchor center
						anchorX := float32(cx) * float32(stride)
						anchorY := float32(cy) * float32(stride)

						// Decode bbox: distance from anchor to edges
						// Model outputs normalized distances – multiply by stride for pixel scale
						st := float32(stride)
						x1 := (anchorX - bboxes[idx*4+0]*st) * scaleW
						y1 := (anchorY - bboxes[idx*4+1]*st) * scaleH
						x2 := (anchorX + bboxes[idx*4+2]*st) * scaleW
						y2 := (anchorY + bboxes[idx*4+3]*st) * scaleH

						// Clamp to image bounds
						x1 = clampF(x1, 0, float32(origW))
						y1 = clampF(y1, 0, float32(origH))
						x2 = clampF(x2, 0, float32(origW))
						y2 = clampF(y2, 0, float32(origH))

						// Decode landmarks
						var lm [5][2]float32
						for li := 0; li < 5; li++ {
							lm[li][0] = (anchorX + landmarks[idx*10+li*2]*st) * scaleW
							lm[li][1] = (anchorY + landmarks[idx*10+li*2+1]*st) * scaleH
						}

						detections = append(detections, Detection{
							BBox:       [4]float32{x1, y1, x2, y2},
							Confidence: score,
							Landmarks:  lm,
						})
					}
					idx++
				}
			}
		}
	}

	return detections
}

// InputSize returns the model's expected input dimensions.
func (d *Detector) InputSize() (int, int) {
	return d.inputW, d.inputH
}

func (d *Detector) Close() {
	if d.session != nil {
		d.session.Destroy()
	}
	if d.inputTensor != nil {
		d.inputTensor.Destroy()
	}
	for _, t := range d.outputTensors {
		if t != nil {
			t.Destroy()
		}
	}
}

// nms performs Non-Maximum Suppression on detections.
func nms(detections []Detection, iouThreshold float32) []Detection {
	if len(detections) == 0 {
		return detections
	}

	sort.Slice(detections, func(i, j int) bool {
		return detections[i].Confidence > detections[j].Confidence
	})

	keep := make([]bool, len(detections))
	for i := range keep {
		keep[i] = true
	}

	for i := 0; i < len(detections); i++ {
		if !keep[i] {
			continue
		}
		for j := i + 1; j < len(detections); j++ {
			if !keep[j] {
				continue
			}
			if iou(detections[i].BBox, detections[j].BBox) > iouThreshold {
				keep[j] = false
			}
		}
	}

	var result []Detection
	for i, d := range detections {
		if keep[i] {
			result = append(result, d)
		}
	}
	return result
}

func iou(a, b [4]float32) float32 {
	x1 := float32(math.Max(float64(a[0]), float64(b[0])))
	y1 := float32(math.Max(float64(a[1]), float64(b[1])))
	x2 := float32(math.Min(float64(a[2]), float64(b[2])))
	y2 := float32(math.Min(float64(a[3]), float64(b[3])))

	intersection := float32(math.Max(0, float64(x2-x1))) * float32(math.Max(0, float64(y2-y1)))

	areaA := (a[2] - a[0]) * (a[3] - a[1])
	areaB := (b[2] - b[0]) * (b[3] - b[1])
	union := areaA + areaB - intersection

	if union <= 0 {
		return 0
	}
	return intersection / union
}

func clampF(v, min, max float32) float32 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
