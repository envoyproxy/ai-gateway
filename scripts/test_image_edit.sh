#!/bin/bash

# Test script for OpenAI Image Edit endpoint through AI Gateway
# Usage: ./test_image_edit.sh <gateway_url> <openai_api_key> <image_path>

GATEWAY_URL="${1:-http://localhost:8080}"
OPENAI_API_KEY="${2:-$OPENAI_API_KEY}"
IMAGE_PATH="${3:-test_image.png}"

# Check if image exists
if [ ! -f "$IMAGE_PATH" ]; then
    echo "Creating a simple test PNG image..."
    # Create a simple 256x256 PNG using Python (requires PIL)
    python3 -c "
from PIL import Image
img = Image.new('RGBA', (256, 256), (255, 200, 200, 255))
img.save('$IMAGE_PATH')
print('Created test image: $IMAGE_PATH')
" 2>/dev/null || {
        echo "Could not create test image. Please provide a PNG file."
        exit 1
    }
fi

# Base64 encode the image
IMAGE_B64=$(base64 < "$IMAGE_PATH" | tr -d '\n')

echo "Testing Image Edit endpoint: $GATEWAY_URL/v1/images/edits"
echo "Image size: $(wc -c < "$IMAGE_PATH") bytes"
echo ""

# Send the request
curl -X POST "$GATEWAY_URL/v1/images/edits" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -d "{
    \"model\": \"dall-e-2\",
    \"image\": \"data:image/png;base64,$IMAGE_B64\",
    \"prompt\": \"Add a small red circle in the center\",
    \"n\": 1,
    \"size\": \"256x256\",
    \"response_format\": \"url\"
  }"

echo ""
