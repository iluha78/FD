package vision

import (
	"fmt"
	"math"

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
// opts may be nil (ORT defaults) or a pre-configured *ort.SessionOptions.
func NewAttributePredictor(modelPath string, opts *ort.SessionOptions) (*AttributePredictor, error) {
	// InsightFace genderage model expects 96x96 input
	inputW, inputH := 96, 96

	inputShape := ort.NewShape(1, 3, int64(inputH), int64(inputW))
	inputTensor, err := ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		return nil, fmt.Errorf("create input tensor: %w", err)
	}

	// Output: [1, 3] — [female_logit, male_logit, age_normalized]
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

	// InsightFace genderage fc1 output = [female_logit, male_logit, age_normalized]
	// fc1 is Concat of fullyconnected0 (gender, 2 classes) + fullyconnected1 (age, 1 value)
	femaleLogit := data[0]
	maleLogit := data[1]
	ageNorm := data[2]

	// Gender: argmax of first 2 logits
	gender := "female"
	if maleLogit > femaleLogit {
		gender = "male"
	}

	// Confidence: softmax probability of the predicted class
	// softmax(male) = 1 / (1 + exp(-(male - female)))
	maleProbability := float32(1.0 / (1.0 + math.Exp(float64(-(maleLogit - femaleLogit)))))
	genderConf := maleProbability
	if gender == "female" {
		genderConf = 1 - maleProbability
	}

	// Age: multiply by 100 to recover real age (InsightFace normalizes age/100 during training)
	age := int(math.Round(float64(ageNorm) * 100))
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
