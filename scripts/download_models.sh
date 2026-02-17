#!/bin/bash
# Download InsightFace ONNX models (buffalo_l pack)
# These are required for the vision worker.

set -e

MODELS_DIR="${1:-./models}"
mkdir -p "$MODELS_DIR"

echo "=== FD Model Downloader ==="
echo "Target directory: $MODELS_DIR"
echo ""

# InsightFace buffalo_l models from GitHub
BASE_URL="https://github.com/deepinsight/insightface/releases/download/v0.7"
PACK="buffalo_l.zip"

if [ -f "$MODELS_DIR/det_10g.onnx" ] && [ -f "$MODELS_DIR/w600k_r50.onnx" ] && [ -f "$MODELS_DIR/genderage.onnx" ]; then
    echo "All models already present. Skipping download."
    exit 0
fi

echo "Downloading InsightFace buffalo_l model pack..."
echo "URL: $BASE_URL/$PACK"

if command -v wget &> /dev/null; then
    wget -q --show-progress -O "$MODELS_DIR/$PACK" "$BASE_URL/$PACK"
elif command -v curl &> /dev/null; then
    curl -L --progress-bar -o "$MODELS_DIR/$PACK" "$BASE_URL/$PACK"
else
    echo "ERROR: Neither wget nor curl found. Please install one of them."
    exit 1
fi

echo "Extracting models..."
if command -v unzip &> /dev/null; then
    unzip -o "$MODELS_DIR/$PACK" -d "$MODELS_DIR/tmp_extract"
else
    echo "ERROR: unzip not found. Please install it."
    exit 1
fi

# Move ONNX files to models dir
find "$MODELS_DIR/tmp_extract" -name "*.onnx" -exec mv {} "$MODELS_DIR/" \;

# Cleanup
rm -rf "$MODELS_DIR/tmp_extract" "$MODELS_DIR/$PACK"

echo ""
echo "=== Models downloaded ==="
echo "Files:"
ls -lh "$MODELS_DIR"/*.onnx 2>/dev/null || echo "No ONNX files found!"

echo ""
echo "Expected models:"
echo "  - det_10g.onnx      (RetinaFace face detection)"
echo "  - w600k_r50.onnx    (ArcFace face recognition)"
echo "  - genderage.onnx    (Gender + Age prediction)"

# Also download ONNX Runtime for Windows if needed
echo ""
echo "=== ONNX Runtime ==="
ONNX_VERSION="1.17.1"

if [ ! -f "$MODELS_DIR/../onnxruntime.dll" ] && [ "$(uname -s)" = "MINGW"* ] || [ "$(uname -s)" = "MSYS"* ]; then
    echo "Downloading ONNX Runtime for Windows..."
    ONNX_URL="https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-win-x64-${ONNX_VERSION}.zip"
    curl -L --progress-bar -o "$MODELS_DIR/ort.zip" "$ONNX_URL"
    unzip -o "$MODELS_DIR/ort.zip" -d "$MODELS_DIR/tmp_ort"
    find "$MODELS_DIR/tmp_ort" -name "onnxruntime.dll" -exec cp {} "$MODELS_DIR/../" \;
    rm -rf "$MODELS_DIR/tmp_ort" "$MODELS_DIR/ort.zip"
    echo "ONNX Runtime DLL placed at project root."
else
    echo "Skipping ONNX Runtime download (not Windows or already present)."
fi

echo ""
echo "=== Done ==="
