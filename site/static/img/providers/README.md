# LLM Provider Logos

This directory contains logo images for all supported LLM providers.

## File Naming Convention

Please name the logo files using the following format (SVG only):
- `openai.svg` - OpenAI
- `aws-bedrock.svg` - AWS Bedrock
- `azure-openai.svg` - Azure OpenAI
- `google-gemini.svg` - Google Gemini
- `groq.svg` - Groq
- `mistral.svg` - Mistral
- `cohere.svg` - Cohere
- `together-ai.svg` - Together AI
- `deepinfra.svg` - DeepInfra
- `deepseek.svg` - DeepSeek
- `hunyuan.svg` - Hunyuan (Tencent)
- `sambanova.svg` - SambaNova
- `grok.svg` - Grok (X.AI)
- `vertex-ai.svg` - Google Vertex AI
- `self-hosted.svg` - Self-hosted/Docker

## Image Guidelines

- **Format**: SVG only (scalable vector graphics)
- **Size**: Optimal at 100x100px or similar square dimensions
- **Background**: Transparent background preferred
- **Quality**: Clean, scalable logos that look good at any size

## Error Handling

If an SVG file is missing or fails to load, the component will automatically show a placeholder logo.

## Adding New Logos

1. Save the SVG logo file in this directory with the correct naming convention
2. No code changes needed - the component will automatically use the logo!

## Current Status

- ✅ Directory created and ready for logo files
- 🔄 Logos currently using external URLs as fallback
- 📋 15 provider logos needed
