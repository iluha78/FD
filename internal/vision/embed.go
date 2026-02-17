package vision

import (
	"fmt"
	"math"

	ort "github.com/yalue/onnxruntime_go"
)

// Embedder extracts face embeddings using ArcFace ONNX model.
type Embedder struct {
	session      *ort.AdvancedSession
	inputTensor  *ort.Tensor[float32]
	outputTensor *ort.Tensor[float32]
	inputW       int
	inputH       int
	embDim       int
}

// NewEmbedder loads the ArcFace ONNX model for face embedding extraction.
func NewEmbedder(modelPath string) (*Embedder, error) {
	// ArcFace w600k_r50 expects 112x112 input
	inputW, inputH := 112, 112
	embDim := 512

	inputShape := ort.NewShape(1, 3, int64(inputH), int64(inputW))
	inputTensor, err := ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		return nil, fmt.Errorf("create input tensor: %w", err)
	}

	outputShape := ort.NewShape(1, int64(embDim))
	outputTensor, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		return nil, fmt.Errorf("create output tensor: %w", err)
	}

	session, err := ort.NewAdvancedSession(modelPath,
		[]string{"input.1"},
		[]string{"683"},
		[]ort.Value{inputTensor},
		[]ort.Value{outputTensor},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create embedder session: %w", err)
	}

	return &Embedder{
		session:      session,
		inputTensor:  inputTensor,
		outputTensor: outputTensor,
		inputW:       inputW,
		inputH:       inputH,
		embDim:       embDim,
	}, nil
}

// Extract runs embedding extraction on a face crop.
// faceData should be CHW format [3, 112, 112], normalized.
// Returns a normalized 512-dimensional embedding vector.
func (e *Embedder) Extract(faceData []float32) ([]float32, error) {
	// Copy input data into the input tensor
	inputSlice := e.inputTensor.GetData()
	copy(inputSlice, faceData)

	if err := e.session.Run(); err != nil {
		return nil, fmt.Errorf("run embedding: %w", err)
	}

	// Read output directly from the output tensor
	outputData := e.outputTensor.GetData()

	embedding := make([]float32, e.embDim)
	copy(embedding, outputData)

	// L2 normalize
	normalize(embedding)

	return embedding, nil
}

// InputSize returns the expected face crop dimensions.
func (e *Embedder) InputSize() (int, int) {
	return e.inputW, e.inputH
}

// EmbeddingDim returns the embedding vector dimension.
func (e *Embedder) EmbeddingDim() int {
	return e.embDim
}

func (e *Embedder) Close() {
	if e.session != nil {
		e.session.Destroy()
	}
	if e.inputTensor != nil {
		e.inputTensor.Destroy()
	}
	if e.outputTensor != nil {
		e.outputTensor.Destroy()
	}
}

// normalize performs L2 normalization in-place.
func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := float32(math.Sqrt(sum))
	if norm > 0 {
		for i := range v {
			v[i] /= norm
		}
	}
}
