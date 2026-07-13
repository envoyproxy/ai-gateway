/**
 * Example: Setting routing plan header with JavaScript fetch API
 *
 * This example demonstrates how to use the x-ai-eg-routing-plan header
 * to control backend selection across multiple providers.
 */

// Define routing plan: try Azure first, fall back to GCP
const plan = {
  backends: ["azure-primary", "gcp-ptu"],
  fallbackEnabled: true
};

// Encode as base64 JSON
const encodedPlan = btoa(JSON.stringify(plan));

// Make request to AI Gateway with routing plan header
async function chatCompletion() {
  const response = await fetch("http://localhost:8080/v1/chat/completions", {
    method: "POST",
    headers: {
      "x-ai-eg-routing-plan": encodedPlan,
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      model: "gpt-4",
      messages: [
        { role: "user", content: "What is the capital of France?" }
      ]
    })
  });

  const data = await response.json();
  console.log(data.choices[0].message.content);
}

chatCompletion();

// Example output on fallback:
// Attempt 1: x-ai-eg-routing-plan directs to azure-primary
//   -> DFP sets :authority=snc-oai-llmproxy-dev-eastus2.openai.azure.com
//   -> Azure responds 503
//   -> Envoy retries
// Attempt 2: AI Gateway picks backends[1] (gcp-ptu)
//   -> DFP sets :authority=us-central1-aiplatform.googleapis.com
//   -> GCP returns 200 ✓
