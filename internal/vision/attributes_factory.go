package vision

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/your-org/fd/internal/config"
)

func newAttributesModel(cfg config.VisionConfig, opts *ort.SessionOptions) (AttributesModel, string, error) {
	modelType := strings.ToLower(strings.TrimSpace(os.Getenv("FD_VISION_ATTRIBUTES_MODEL")))
	if modelType == "" {
		modelType = "genderage"
	}

	switch modelType {
	case "fairface":
		modelFile := strings.TrimSpace(os.Getenv("FD_VISION_ATTRIBUTES_MODEL_FILE"))
		if modelFile == "" {
			modelFile = "fairface.onnx"
		}
		modelPath := filepath.Join(cfg.ModelsDir, modelFile)
		ff, err := NewFairFacePredictor(modelPath, opts)
		if err != nil {
			return nil, "", err
		}
		return ff, modelPath, nil

	case "openvino", "openvino_age_gender", "openvino_age_gender_0013", "age-gender-recognition-retail-0013":
		modelFile := strings.TrimSpace(os.Getenv("FD_VISION_ATTRIBUTES_MODEL_FILE"))
		if modelFile == "" {
			modelFile = "age-gender-recognition-retail-0013.onnx"
		}
		modelPath := filepath.Join(cfg.ModelsDir, modelFile)

		ovCfg := OpenVINOAgeGenderConfig{
			InputName:        getEnvOrDefault("FD_VISION_ATTRIBUTES_INPUT_NAME", "data"),
			AgeOutputName:    getEnvOrDefault("FD_VISION_ATTRIBUTES_AGE_OUTPUT_NAME", "age_conv3"),
			GenderOutputName: getEnvOrDefault("FD_VISION_ATTRIBUTES_GENDER_OUTPUT_NAME", "prob"),
			InputSize:        getEnvInt("FD_VISION_ATTRIBUTES_INPUT_SIZE", 62),
			AgeOutputSize:    getEnvInt("FD_VISION_ATTRIBUTES_AGE_OUTPUT_SIZE", 1),
			GenderOutputSize: getEnvInt("FD_VISION_ATTRIBUTES_GENDER_OUTPUT_SIZE", 2),
			AgeScale:         float32(getEnvFloat("FD_VISION_ATTRIBUTES_AGE_SCALE", 100.0)),
			AgeOffset:        float32(getEnvFloat("FD_VISION_ATTRIBUTES_AGE_OFFSET", 0.0)),
		}

		ov, err := NewOpenVINOAgeGenderPredictor(modelPath, opts, ovCfg)
		if err != nil {
			return nil, "", err
		}
		return ov, modelPath, nil

	case "mivolo":
		modelFile := strings.TrimSpace(os.Getenv("FD_VISION_ATTRIBUTES_MODEL_FILE"))
		if modelFile == "" {
			modelFile = "mivolo.onnx"
		}
		modelPath := filepath.Join(cfg.ModelsDir, modelFile)

		mivoloCfg := MiVOLOConfig{
			InputName:   getEnvOrDefault("FD_VISION_ATTRIBUTES_INPUT_NAME", "input"),
			OutputName:  getEnvOrDefault("FD_VISION_ATTRIBUTES_OUTPUT_NAME", "output"),
			InputSize:   getEnvInt("FD_VISION_ATTRIBUTES_INPUT_SIZE", 224),
			OutputSize:  getEnvInt("FD_VISION_ATTRIBUTES_OUTPUT_SIZE", 3),
			AgeIndex:    getEnvInt("FD_VISION_ATTRIBUTES_AGE_INDEX", 0),
			FemaleIndex: getEnvInt("FD_VISION_ATTRIBUTES_FEMALE_INDEX", 1),
			MaleIndex:   getEnvInt("FD_VISION_ATTRIBUTES_MALE_INDEX", 2),
			AgeScale:    float32(getEnvFloat("FD_VISION_ATTRIBUTES_AGE_SCALE", 1.0)),
			AgeOffset:   float32(getEnvFloat("FD_VISION_ATTRIBUTES_AGE_OFFSET", 0.0)),
		}

		mivolo, err := NewMiVOLOPredictor(modelPath, opts, mivoloCfg)
		if err != nil {
			return nil, "", err
		}
		return mivolo, modelPath, nil

	case "genderage":
		fallthrough
	default:
		modelFile := strings.TrimSpace(os.Getenv("FD_VISION_ATTRIBUTES_MODEL_FILE"))
		if modelFile == "" {
			modelFile = "genderage.onnx"
		}
		modelPath := filepath.Join(cfg.ModelsDir, modelFile)
		attrs, err := NewAttributePredictor(modelPath, opts)
		if err != nil {
			return nil, "", err
		}
		return attrs, modelPath, nil
	}
}

func getEnvOrDefault(name, fallback string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	return v
}

func getEnvInt(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getEnvFloat(name string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func validateVisionConfig(cfg config.VisionConfig) error {
	if cfg.MinFaceSize < 0 {
		return fmt.Errorf("vision.min_face_size must be >= 0")
	}
	return nil
}
