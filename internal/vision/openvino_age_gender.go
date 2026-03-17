package vision

import (
	"fmt"
	"image"
	"math"

	ort "github.com/yalue/onnxruntime_go"
)

// OpenVINOAgeGenderConfig configures age-gender-recognition-retail-0013 ONNX IO.
type OpenVINOAgeGenderConfig struct {
	InputName        string
	AgeOutputName    string
	GenderOutputName string
	InputSize        int
	AgeOutputSize    int
	GenderOutputSize int
	AgeScale         float32
	AgeOffset        float32
}

// OpenVINOAgeGenderPredictor predicts age/gender from OpenVINO retail model.
type OpenVINOAgeGenderPredictor struct {
	session      *ort.AdvancedSession
	inputTensor  *ort.Tensor[float32]
	ageTensor    *ort.Tensor[float32]
	genderTensor *ort.Tensor[float32]
	cfg          OpenVINOAgeGenderConfig
}

func NewOpenVINOAgeGenderPredictor(modelPath string, opts *ort.SessionOptions, cfg OpenVINOAgeGenderConfig) (*OpenVINOAgeGenderPredictor, error) {
	if cfg.InputName == "" {
		cfg.InputName = "data"
	}
	if cfg.AgeOutputName == "" {
		cfg.AgeOutputName = "age_conv3"
	}
	if cfg.GenderOutputName == "" {
		cfg.GenderOutputName = "prob"
	}
	if cfg.InputSize <= 0 {
		cfg.InputSize = 62
	}
	if cfg.AgeOutputSize <= 0 {
		cfg.AgeOutputSize = 1
	}
	if cfg.GenderOutputSize <= 0 {
		cfg.GenderOutputSize = 2
	}
	if cfg.AgeScale == 0 {
		cfg.AgeScale = 100
	}

	inputTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, int64(cfg.InputSize), int64(cfg.InputSize)))
	if err != nil {
		return nil, fmt.Errorf("create openvino age-gender input tensor: %w", err)
	}
	ageTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(cfg.AgeOutputSize), 1, 1))
	if err != nil {
		inputTensor.Destroy()
		return nil, fmt.Errorf("create openvino age output tensor: %w", err)
	}
	genderTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(cfg.GenderOutputSize), 1, 1))
	if err != nil {
		inputTensor.Destroy()
		ageTensor.Destroy()
		return nil, fmt.Errorf("create openvino gender output tensor: %w", err)
	}

	session, err := ort.NewAdvancedSession(modelPath,
		[]string{cfg.InputName},
		[]string{cfg.AgeOutputName, cfg.GenderOutputName},
		[]ort.Value{inputTensor},
		[]ort.Value{ageTensor, genderTensor},
		opts,
	)
	if err != nil {
		inputTensor.Destroy()
		ageTensor.Destroy()
		genderTensor.Destroy()
		return nil, fmt.Errorf("create openvino age-gender session: %w", err)
	}

	return &OpenVINOAgeGenderPredictor{
		session:      session,
		inputTensor:  inputTensor,
		ageTensor:    ageTensor,
		genderTensor: genderTensor,
		cfg:          cfg,
	}, nil
}

func (p *OpenVINOAgeGenderPredictor) Predict(faceData []float32) (*GenderAge, error) {
	copy(p.inputTensor.GetData(), faceData)

	if err := p.session.Run(); err != nil {
		return nil, fmt.Errorf("run openvino age-gender: %w", err)
	}

	ageData := p.ageTensor.GetData()
	if len(ageData) == 0 {
		return nil, fmt.Errorf("unexpected openvino age output size: 0")
	}
	genderData := p.genderTensor.GetData()
	if len(genderData) < 2 {
		return nil, fmt.Errorf("unexpected openvino gender output size: %d", len(genderData))
	}

	age := int(math.Round(float64(ageData[0]*p.cfg.AgeScale + p.cfg.AgeOffset)))
	if age < 0 {
		age = 0
	}
	if age > 100 {
		age = 100
	}

	female := genderData[0]
	male := genderData[1]
	gender := "female"
	genderConf := female
	if male > female {
		gender = "male"
		genderConf = male
	}
	// If model exported logits instead of probabilities, map to [0..1].
	if genderConf < 0 || genderConf > 1 {
		maleProb := float32(1.0 / (1.0 + math.Exp(float64(-(male - female)))))
		gender = "female"
		genderConf = 1 - maleProb
		if maleProb > 0.5 {
			gender = "male"
			genderConf = maleProb
		}
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

// Preprocess converts face crop to BGR CHW float32 with Caffe-style mean subtraction.
// age-gender-recognition-retail-0013 was trained with OpenCV BGR + mean=[104, 117, 123].
func (p *OpenVINOAgeGenderPredictor) Preprocess(img image.Image) []float32 {
	return imageToFloat32CHW_BGR(img, p.cfg.InputSize, p.cfg.InputSize,
		[3]float32{104, 117, 123}, [3]float32{1, 1, 1})
}

func (p *OpenVINOAgeGenderPredictor) InputSize() (int, int) {
	return p.cfg.InputSize, p.cfg.InputSize
}

func (p *OpenVINOAgeGenderPredictor) Close() {
	if p.session != nil {
		p.session.Destroy()
	}
	if p.inputTensor != nil {
		p.inputTensor.Destroy()
	}
	if p.ageTensor != nil {
		p.ageTensor.Destroy()
	}
	if p.genderTensor != nil {
		p.genderTensor.Destroy()
	}
}
