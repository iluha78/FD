package vision

import (
	"fmt"
	"image"
	"math"

	ort "github.com/yalue/onnxruntime_go"
)

// MiVOLOConfig describes tensor names and output mapping for exported MiVOLO ONNX models.
type MiVOLOConfig struct {
	InputName   string
	OutputName  string
	InputSize   int
	OutputSize  int
	AgeIndex    int
	FemaleIndex int
	MaleIndex   int
	AgeScale    float32
	AgeOffset   float32
}

// MiVOLOPredictor predicts gender and age with a MiVOLO ONNX model.
type MiVOLOPredictor struct {
	session      *ort.AdvancedSession
	inputTensor  *ort.Tensor[float32]
	outputTensor *ort.Tensor[float32]
	cfg          MiVOLOConfig
}

func NewMiVOLOPredictor(modelPath string, opts *ort.SessionOptions, cfg MiVOLOConfig) (*MiVOLOPredictor, error) {
	if cfg.InputName == "" {
		cfg.InputName = "input"
	}
	if cfg.OutputName == "" {
		cfg.OutputName = "output"
	}
	if cfg.InputSize <= 0 {
		cfg.InputSize = 224
	}
	if cfg.OutputSize <= 0 {
		cfg.OutputSize = 3
	}
	if cfg.AgeScale == 0 {
		cfg.AgeScale = 1
	}

	inputShape := ort.NewShape(1, 3, int64(cfg.InputSize), int64(cfg.InputSize))
	inputTensor, err := ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		return nil, fmt.Errorf("create mivolo input tensor: %w", err)
	}

	outputShape := ort.NewShape(1, int64(cfg.OutputSize))
	outputTensor, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		inputTensor.Destroy()
		return nil, fmt.Errorf("create mivolo output tensor: %w", err)
	}

	session, err := ort.NewAdvancedSession(modelPath,
		[]string{cfg.InputName},
		[]string{cfg.OutputName},
		[]ort.Value{inputTensor},
		[]ort.Value{outputTensor},
		opts,
	)
	if err != nil {
		inputTensor.Destroy()
		outputTensor.Destroy()
		return nil, fmt.Errorf("create mivolo session: %w", err)
	}

	return &MiVOLOPredictor{
		session:      session,
		inputTensor:  inputTensor,
		outputTensor: outputTensor,
		cfg:          cfg,
	}, nil
}

func (p *MiVOLOPredictor) Predict(faceData []float32) (*GenderAge, error) {
	inputSlice := p.inputTensor.GetData()
	copy(inputSlice, faceData)

	if err := p.session.Run(); err != nil {
		return nil, fmt.Errorf("run mivolo: %w", err)
	}

	data := p.outputTensor.GetData()
	maxIdx := p.cfg.AgeIndex
	if p.cfg.FemaleIndex > maxIdx {
		maxIdx = p.cfg.FemaleIndex
	}
	if p.cfg.MaleIndex > maxIdx {
		maxIdx = p.cfg.MaleIndex
	}
	if maxIdx < 0 || len(data) <= maxIdx {
		return nil, fmt.Errorf("unexpected mivolo output size: got=%d required_index=%d", len(data), maxIdx)
	}

	ageRaw := data[p.cfg.AgeIndex]
	age := int(math.Round(float64(ageRaw*p.cfg.AgeScale + p.cfg.AgeOffset)))
	if age < 0 {
		age = 0
	}
	if age > 100 {
		age = 100
	}

	femaleLogit := data[p.cfg.FemaleIndex]
	maleLogit := data[p.cfg.MaleIndex]
	maleProb := float32(1.0 / (1.0 + math.Exp(float64(-(maleLogit - femaleLogit)))))

	gender := "female"
	genderConf := 1 - maleProb
	if maleProb > 0.5 {
		gender = "male"
		genderConf = maleProb
	}
	if genderConf < minGenderConfidence {
		gender = ""
	}

	return &GenderAge{
		Gender:           gender,
		GenderConfidence: genderConf,
		Age:              age,
		AgeRange:         ageRangeFor(age),
	}, nil
}

// Preprocess converts face crop to RGB CHW float32 with ImageNet normalization.
// MiVOLO uses standard torchvision transforms: mean=[0.485,0.456,0.406], std=[0.229,0.224,0.225].
func (p *MiVOLOPredictor) Preprocess(img image.Image) []float32 {
	return imageToFloat32CHW(img, p.cfg.InputSize, p.cfg.InputSize,
		[3]float32{123.675, 116.28, 103.53},
		[3]float32{58.395, 57.12, 57.375})
}

func (p *MiVOLOPredictor) InputSize() (int, int) {
	return p.cfg.InputSize, p.cfg.InputSize
}

func (p *MiVOLOPredictor) Close() {
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
