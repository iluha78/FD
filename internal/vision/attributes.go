package vision

import (
	"fmt"
	"image"
	"math"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	minAttributeFaceSize    = 40
	minGenderConfidence     = 0.70
	attributeAgeEMAAlpha    = 0.35
	attributeSnapshotMinDim = 100
)

// GenderAge represents predicted gender and age attributes.
type GenderAge struct {
	Gender           string  // "male" or "female"; may be empty if uncertain
	GenderConfidence float32 // 0.0 to 1.0
	Age              int
	AgeRange         string // e.g. "30-35"
}

// AttributesModel is a common interface for gender/age predictors (genderage, MiVOLO, etc).
type AttributesModel interface {
	// Preprocess converts a face crop image to the model-specific input tensor (CHW float32).
	Preprocess(img image.Image) []float32
	Predict(faceData []float32) (*GenderAge, error)
	InputSize() (int, int)
	Close()
}

// AttributePredictor predicts gender and age using InsightFace genderage model.
type AttributePredictor struct {
	session      *ort.AdvancedSession
	inputTensor  *ort.Tensor[float32]
	outputTensor *ort.Tensor[float32]
	inputW       int
	inputH       int
}

// NewAttributePredictor loads the gender/age ONNX model.
// opts may be nil (ORT defaults) or a pre-configured *ort.SessionOptions.
func NewAttributePredictor(modelPath string, opts *ort.SessionOptions) (*AttributePredictor, error) {
	// InsightFace genderage model expects 96x96 input
	inputW, inputH := 96, 96

	inputShape := ort.NewShape(1, 3, int64(inputH), int64(inputW))
	inputTensor, err := ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		return nil, fmt.Errorf("create input tensor: %w", err)
	}

	outputShape := ort.NewShape(1, 3)
	outputTensor, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		return nil, fmt.Errorf("create output tensor: %w", err)
	}

	session, err := ort.NewAdvancedSession(modelPath,
		[]string{"data"},
		[]string{"fc1"},
		[]ort.Value{inputTensor},
		[]ort.Value{outputTensor},
		opts,
	)
	if err != nil {
		return nil, fmt.Errorf("create attribute session: %w", err)
	}

	return &AttributePredictor{
		session:      session,
		inputTensor:  inputTensor,
		outputTensor: outputTensor,
		inputW:       inputW,
		inputH:       inputH,
	}, nil
}

// Predict runs gender/age prediction on a face crop.
// faceData should be CHW format [3, 96, 96], normalized.
func (p *AttributePredictor) Predict(faceData []float32) (*GenderAge, error) {
	inputSlice := p.inputTensor.GetData()
	copy(inputSlice, faceData)

	if err := p.session.Run(); err != nil {
		return nil, fmt.Errorf("run attributes: %w", err)
	}

	data := p.outputTensor.GetData()
	if len(data) < 3 {
		return nil, fmt.Errorf("unexpected output size: %d", len(data))
	}

	femaleLogit := data[0]
	maleLogit := data[1]
	ageNorm := data[2]

	gender := "female"
	if maleLogit > femaleLogit {
		gender = "male"
	}

	maleProbability := float32(1.0 / (1.0 + math.Exp(float64(-(maleLogit - femaleLogit)))))
	genderConf := maleProbability
	if gender == "female" {
		genderConf = 1 - maleProbability
	}
	if genderConf < minGenderConfidence {
		gender = ""
	}

	age := int(math.Round(float64(ageNorm) * 100))
	if age < 0 {
		age = 0
	}
	if age > 100 {
		age = 100
	}

	return &GenderAge{
		Gender:           gender,
		GenderConfidence: genderConf,
		Age:              age,
		AgeRange:         ageRangeFor(age),
	}, nil
}

func ageRangeFor(age int) string {
	if age < 0 {
		age = 0
	}
	if age > 100 {
		age = 100
	}
	lower := (age / 5) * 5
	upper := lower + 5
	if upper > 100 {
		upper = 100
	}
	return fmt.Sprintf("%d-%d", lower, upper)
}

// Preprocess converts a face crop to CHW float32 in RGB order, range [0, 255].
// InsightFace genderage.onnx uses blobFromImage with swapRB=True (BGR→RGB), mean=0, std=1.
func (p *AttributePredictor) Preprocess(img image.Image) []float32 {
	return imageToFloat32CHW(img, p.inputW, p.inputH, [3]float32{0, 0, 0}, [3]float32{1, 1, 1})
}

// InputSize returns the expected face crop dimensions.
func (p *AttributePredictor) InputSize() (int, int) {
	return p.inputW, p.inputH
}

func (p *AttributePredictor) Close() {
	if p.session != nil {
		p.session.Destroy()
	}
	if p.inputTensor != nil {
		p.inputTensor.Destroy()
	}
	if p.outputTensor != nil {
		p.outputTensor.Destroy()
	}
}
