/**
 * Homepage content — edit copy here, not in the components.
 * Components live in src/components/home/.
 */

export type Cta = { label: string; to: string };

export const hero = {
  headline: 'Models. Tools. Agents.',
  headlineAccent: 'One point of control.',
  lead:
    'Application teams get one consistent, OpenAI-compatible API for every model and tool, from hosted providers to self-hosted inference. Platform teams keep credentials, quotas, routing, failover, and usage — managed centrally, enforced by Envoy.',
  leadStrong:
    'A stable contract between application and platform teams.',
  ctas: [
    { label: 'Get Started', to: '/docs/getting-started/' },
    { label: 'View on GitHub', to: 'https://github.com/theagentrouter/agent-router' },
  ] satisfies Cta[],
  command: 'OPENAI_API_KEY=sk-... aigw run',
  sub: 'An Agentic AI Foundation project · Built on Envoy',
};

export const howItFits = {
  label: 'How it fits together',
  title: 'A control plane for AI traffic',
  standfirst:
    'Applications speak one API. Agent Router decides where each request goes — which provider, which model, which credentials, at what rate — and Envoy carries it with production-proxy reliability.',
  planes: [
    {
      name: 'Agent Router',
      role: 'Control plane — routing, policy, credentials, quotas, observability',
      image: { light: '/img/brand/ar-mark-marquee.svg', dark: '/img/brand/ar-mark-marquee.svg' },
    },
    {
      name: 'Envoy',
      role: 'Data plane — the proxy that carries every request',
      // official CNCF icon-only cuts (cncf/artwork) — never recoloured
      image: { light: '/img/brand/envoy-icon-color.svg', dark: '/img/brand/envoy-icon-white.svg' },
    },
  ],
  refrainStrong: 'Agent Router controls.',
  refrain: 'Envoy carries.',
};

export type CapabilityIcon =
  | 'hexagon'
  | 'lanes'
  | 'lock'
  | 'hub'
  | 'bars'
  | 'clock';

export type Capability = {
  icon: CapabilityIcon;
  title: string;
  body: string;
  to?: string;
};

export const capabilities = {
  label: 'Capabilities',
  title: 'One router, one agent integration point',
  standfirst:
    'One place to govern how applications and agents reach models and MCP tools — independent of any one agent framework or model provider.',
  items: [
    {
      icon: 'hexagon',
      title: 'One API, every provider',
      body: 'Route OpenAI-compatible requests to Anthropic, Bedrock, Vertex, Azure, or self-hosted vLLM — no application changes.',
      to: '/docs/capabilities/llm-integrations/',
    },
    {
      icon: 'lanes',
      title: 'Traffic management',
      body: 'Model virtualization, provider fallback, and token-aware rate limiting that understands LLM usage, not just requests.',
      to: '/docs/capabilities/traffic/',
    },
    {
      icon: 'lock',
      title: 'Security & upstream auth',
      body: 'Credentials live in the gateway, not in every app — API keys, AWS, GCP, and Azure identities, rotated centrally.',
      to: '/docs/capabilities/security/',
    },
    {
      icon: 'hub',
      title: 'MCP Gateway',
      body: 'Aggregate MCP servers, filter tools, and enforce authorization — agents reach tools through one governed front door.',
      to: '/docs/capabilities/mcp/',
    },
    {
      icon: 'bars',
      title: 'Inference-aware routing',
      body: 'Route to self-managed models with Kubernetes InferencePool support and inference-aware endpoint picking.',
      to: '/docs/capabilities/inference/',
    },
    {
      icon: 'clock',
      title: 'Observability',
      body: 'Token-level metrics, traces, and access logs — see cost, latency, and usage per model, per team.',
      to: '/docs/capabilities/observability/',
    },
  ] satisfies Capability[],
};

export type TerminalLine = { kind: 'comment' | 'command' | 'blank'; text?: string };

export type QuickstartTab = {
  id: string;
  label: string;
  cta: Cta;
  terminal: TerminalLine[];
};

export const quickstart = {
  label: 'Get running',
  title: 'From laptop to production',
  body:
    'Try it with one command on your laptop. Ship the same configuration on Kubernetes — no rewrite in between.',
  tabs: [
    {
      id: 'local',
      label: 'On your laptop',
      cta: { label: 'Read the guide', to: '/docs/cli/aigwrun' },
      terminal: [
        { kind: 'comment', text: '# 1 — start the router locally' },
        { kind: 'command', text: 'OPENAI_API_KEY=sk-... aigw run' },
        { kind: 'blank' },
        { kind: 'comment', text: '# 2 — point anything OpenAI-compatible at it' },
        { kind: 'command', text: 'curl localhost:1975/v1/chat/completions \\' },
        { kind: 'command', text: `  -d '{"model": "gpt-5", "messages": [...]}'` },
      ],
    },
    {
      id: 'kubernetes',
      label: 'On Kubernetes',
      cta: { label: 'Read the guide', to: '/docs/getting-started/' },
      terminal: [
        { kind: 'comment', text: '# 1 — install the gateway' },
        { kind: 'command', text: 'helm install aigw oci://docker.io/envoyproxy/ai-gateway-helm \\' },
        { kind: 'command', text: '  --namespace envoy-ai-gateway-system --create-namespace' },
        { kind: 'blank' },
        { kind: 'comment', text: '# 2 — declare a route' },
        { kind: 'command', text: 'kubectl apply -f aigatewayroute.yaml' },
        { kind: 'blank' },
        { kind: 'comment', text: '# 3 — send a request' },
        { kind: 'command', text: 'curl $GATEWAY/v1/chat/completions \\' },
        { kind: 'command', text: `  -d '{"model": "claude-sonnet-5", "messages": [...]}'` },
      ],
    },
  ] satisfies QuickstartTab[],
};

export type CommunityIcon = 'chat' | 'calendar' | 'github' | 'mic';

export const community = {
  ctaText: 'Using Agent Router?',
  ctaLink: {
    label: 'Add your logo →',
    to: 'https://github.com/theagentrouter/agent-router/edit/main/site/src/data/adopters/adopters.json',
  },
  pills: [
    // TODO(launch): replace with the real AAIF Discord invite.
    { icon: 'chat', label: 'Join the Discord', to: 'https://discord.gg/TODO-agent-router' },
    {
      icon: 'calendar',
      label: 'Weekly meeting — Mondays',
      to: 'https://zoom-lfx.platform.linuxfoundation.org/meeting/91546415944?password=61fd5a5d-41e9-4b0c-86ea-b607c4513e37',
    },
    {
      icon: 'github',
      label: 'GitHub Discussions',
      to: 'https://github.com/theagentrouter/agent-router/issues?q=is%3Aissue+label%3Adiscussion',
    },
    { icon: 'mic', label: 'Talks', to: '/talks' },
  ] satisfies { icon: CommunityIcon; label: string; to: string }[],
};
