package vision

import (
	"fmt"
	"image"
	"math"

	ort "github.com/yalue/onnxruntime_go"
)

// FairFace age bucket labels and midpoints for continuous age estimation.
var (
	fairfaceAgeLabels = [9]string{
		"0-2", "3-9", "10-19", "20-29", "30-39",
		"40-49", "50-59", "60-69", "70+",
	}
	fairfaceAgeMidpoints = [9]float32{
		1, 6, 14.5, 24.5, 34.5,
		44.5, 54.5, 64.5, 75,
	}
)

// FairFacePredictor predicts gender and age using the FairFace ResNet-34 model.
// Model outputs: race_output (1,7), gender_output (1,2), age_output (1,9).
// Gender: 0=Male, 1=Female. Age: 9 buckets.
type FairFacePredictor struct {
	session      *ort.AdvancedSession
	inputTensor  *ort.Tensor[float32]
	raceOutput   *ort.Tensor[float32]
	genderOutput *ort.Tensor[float32]
	ageOutput    *ort.Tensor[float32]
	inputSize    int
}

// NewFairFacePredictor loads the FairFace ONNX model (3-head version from yakhyo/fairface-onnx).
func NewFairFacePredictor(modelPath string, opts *ort.SessionOptions) (*FairFacePredictor, error) {
	inputSize := 224

	inputShape := ort.NewShape(1, 3, int64(inputSize), int64(inputSize))
	inputTensor, err := ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		return nil, fmt.Errorf("create input tensor: %w", err)
	}

	raceOutput, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 7))
	if err != nil {
		return nil, fmt.Errorf("create race output tensor: %w", err)
	}

	genderOutput, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 2))
	if err != nil {
		return nil, fmt.Errorf("create gender output tensor: %w", err)
	}

	ageOutput, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 9))
	if err != nil {
		return nil, fmt.Errorf("create age output tensor: %w", err)
	}

	session, err := ort.NewAdvancedSession(modelPath,
		[]string{"input"},
		[]string{"race_output", "gender_output", "age_output"},
		[]ort.Value{inputTensor},
		[]ort.Value{raceOutput, genderOutput, ageOutput},
		opts,
	)
	if err != nil {
		return nil, fmt.Errorf("create fairface session: %w", err)
	}

	return &FairFacePredictor{
		session:      session,
		inputTensor:  inputTensor,
		raceOutput:   raceOutput,
		genderOutput: genderOutput,
		ageOutput:    ageOutput,
		inputSize:    inputSize,
	}, nil
}

// Preprocess converts a face crop to CHW float32 in RGB order with ImageNet normalization.
// FairFace uses: pixel/255.0, then (pixel - mean) / std with ImageNet stats.
func (p *FairFacePredictor) Preprocess(img image.Image) []float32 {
	// ImageNet mean/std, but we need to account for the /255 step.
	// Final formula: ((pixel/255.0) - mean) / std = (pixel - mean*255) / (std*255)
	mean := [3]float32{0.485 * 255, 0.456 * 255, 0.406 * 255}
	std := [3]float32{0.229 * 255, 0.224 * 255, 0.225 * 255}
	return imageToFloat32CHW(img, p.inputSize, p.inputSize, mean, std)
}

// Predict runs gender/age prediction. faceData should be CHW [3, 224, 224].
func (p *FairFacePredictor) Predict(faceData []float32) (*GenderAge, error) {
	inputSlice := p.inputTensor.GetData()
	copy(inputSlice, faceData)

	if err := p.session.Run(); err != nil {
		return nil, fmt.Errorf("run fairface: %w", err)
	}

	// Parse gender output (0=Male, 1=Female)
	genderData := p.genderOutput.GetData()
	if len(genderData) < 2 {
		return nil, fmt.Errorf("unexpected gender output size: %d", len(genderData))
	}

	gender := "male"
	if genderData[1] > genderData[0] {
		gender = "female"
	}
	// Gender confidence via softmax
	genderConf := softmaxConf(genderData[0], genderData[1])
	if gender == "female" {
		genderConf = 1 - genderConf
	}
	if genderConf < minGenderConfidence {
		gender = ""
	}

	// Parse age output (9 buckets) — weighted average of midpoints
	ageData := p.ageOutput.GetData()
	if len(ageData) < 9 {
		return nil, fmt.Errorf("unexpected age output size: %d", len(ageData))
	}

	ageProbs := softmax(ageData[:9])
	age := float32(0)
	for i := 0; i < 9; i++ {
		age += ageProbs[i] * fairfaceAgeMidpoints[i]
	}
	ageInt := int(math.Round(float64(age)))
	if ageInt < 0 {
		ageInt = 0
	}
	if ageInt > 100 {
		ageInt = 100
	}

	// Find the top age bucket for the range label
	topBucket := 0
	topProb := ageProbs[0]
	for i := 1; i < 9; i++ {
		if ageProbs[i] > topProb {
			topProb = ageProbs[i]
			topBucket = i
		}
	}

	return &GenderAge{
		Gender:           gender,
		GenderConfidence: genderConf,
		Age:              ageInt,
		AgeRange:         fairfaceAgeLabels[topBucket],
	}, nil
}

// InputSize returns the expected face crop dimensions (224x224).
func (p *FairFacePredictor) InputSize() (int, int) {
	return p.inputSize, p.inputSize
}

func (p *FairFacePredictor) Close() {
	if p.session != nil {
		p.session.Destroy()
	}
	if p.inputTensor != nil {
		p.inputTensor.Destroy()
	}
	if p.raceOutput != nil {
		p.raceOutput.Destroy()
	}
	if p.genderOutput != nil {
		p.genderOutput.Destroy()
	}
	if p.ageOutput != nil {
		p.ageOutput.Destroy()
	}
}

// softmax computes softmax over a slice of logits.
func softmax(logits []float32) []float32 {
	max := logits[0]
	for _, v := range logits[1:] {
		if v > max {
			max = v
		}
	}
	probs := make([]float32, len(logits))
	sum := float32(0)
	for i, v := range logits {
		probs[i] = float32(math.Exp(float64(v - max)))
		sum += probs[i]
	}
	for i := range probs {
		probs[i] /= sum
	}
	return probs
}

// softmaxConf returns P(class0) from two logits using sigmoid.
func softmaxConf(logit0, logit1 float32) float32 {
	return float32(1.0 / (1.0 + math.Exp(float64(-(logit0 - logit1)))))
}
