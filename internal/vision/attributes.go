package vision

import (
	"fmt"

	ort "github.com/yalue/onnxruntime_go"
)

// GenderAge represents predicted gender and age attributes.
type GenderAge struct {
	Gender           string  // "male" or "female"
	GenderConfidence float32 // 0.0 to 1.0
	Age              int
	AgeRange         string // e.g. "30-35"
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
func NewAttributePredictor(modelPath string) (*AttributePredictor, error) {
	// InsightFace genderage model expects 96x96 input
	inputW, inputH := 96, 96

	inputShape := ort.NewShape(1, 3, int64(inputH), int64(inputW))
	inputTensor, err := ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		return nil, fmt.Errorf("create input tensor: %w", err)
	}

	// Output: [1, 3] — [gender_prob, age_value, ...]
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
		nil,
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
	// Copy input data into the input tensor
	inputSlice := p.inputTensor.GetData()
	copy(inputSlice, faceData)

	if err := p.session.Run(); err != nil {
		return nil, fmt.Errorf("run attributes: %w", err)
	}

	// Read output directly from the output tensor
	data := p.outputTensor.GetData()
	if len(data) < 3 {
		return nil, fmt.Errorf("unexpected output size: %d", len(data))
	}

	// InsightFace genderage output: [gender_score, age_raw, ...]
	genderScore := data[0]
	ageRaw := data[1]

	gender := "female"
	genderConf := 1 - genderScore
	if genderScore > 0.5 {
		gender = "male"
		genderConf = genderScore
	}

	age := int(ageRaw)
	if age < 0 {
		age = 0
	}
	if age > 100 {
		age = 100
	}

	// Compute age range (±2.5 years bucket)
	lower := (age / 5) * 5
	upper := lower + 5
	ageRange := fmt.Sprintf("%d-%d", lower, upper)

	return &GenderAge{
		Gender:           gender,
		GenderConfidence: genderConf,
		Age:              age,
		AgeRange:         ageRange,
	}, nil
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
