/**
 * Example: Setting routing plan header with fetch API
 */

const plan = {
  backends: ["azure-primary", "gcp-ptu"],
  fallbackEnabled: true
};

const encodedPlan = btoa(JSON.stringify(plan));

fetch("http://localhost:8080/v1/chat/completions", {
  method: "POST",
  headers: {
    "x-ai-eg-routing-plan": encodedPlan,
    "Content-Type": "application/json"
  },
  body: JSON.stringify({
    model: "gpt-4",
    messages: [{ role: "user", content: "Hello" }]
  })
})
  .then(r => r.json())
  .then(data => console.log(data.choices[0].message.content));
