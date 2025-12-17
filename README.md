# Envoy AI Gateway

Envoy AI Gateway is an open source project for using [Envoy Gateway](https://github.com/envoyproxy/gateway) to handle request traffic from application clients to Generative AI services.

## Usage

When using Envoy AI Gateway, we refer to a two-tier gateway pattern. **The Tier One Gateway** functions as a centralized entry point, and the **Tier Two Gateway** handles ingress traffic to a self-hosted model serving cluster.

- The **Tier One Gateway** handles authentication, top-level routing, and global rate limiting
- The **Tier Two Gateway** provides fine-grained control over self-hosted model access, with endpoint picker support for LLM inference optimization.

![](site/blog/images/aigw-ref.drawio.png)

## Supported AI Providers

Envoy AI Gateway supports a wide range of AI providers, making it easy to integrate with your preferred LLM services:

<div align="center">
  <table>
    <tr>
      <td align="center" width="120">
        <img src="site/static/img/providers/openai.svg" width="60" height="60" alt="OpenAI"/>
        <br><sub><b>OpenAI</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/azure-openai.svg" width="60" height="60" alt="Azure OpenAI"/>
        <br><sub><b>Azure OpenAI</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/google-gemini.svg" width="60" height="60" alt="Google Gemini"/>
        <br><sub><b>Google Gemini</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/vertex-ai.svg" width="60" height="60" alt="Vertex AI"/>
        <br><sub><b>Vertex AI</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/aws-bedrock.svg" width="60" height="60" alt="AWS Bedrock"/>
        <br><sub><b>AWS Bedrock</b></sub>
      </td>
    </tr>
    <tr>
      <td align="center" width="120">
        <img src="site/static/img/providers/mistral.svg" width="60" height="60" alt="Mistral"/>
        <br><sub><b>Mistral</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/cohere.svg" width="60" height="60" alt="Cohere"/>
        <br><sub><b>Cohere</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/groq.svg" width="60" height="60" alt="Groq"/>
        <br><sub><b>Groq</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/together-ai.svg" width="60" height="60" alt="Together AI"/>
        <br><sub><b>Together AI</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/deepinfra.svg" width="60" height="60" alt="DeepInfra"/>
        <br><sub><b>DeepInfra</b></sub>
      </td>
    </tr>
    <tr>
      <td align="center" width="120">
        <img src="site/static/img/providers/deepseek.svg" width="60" height="60" alt="DeepSeek"/>
        <br><sub><b>DeepSeek</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/hunyuan.svg" width="60" height="60" alt="Hunyuan"/>
        <br><sub><b>Hunyuan</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/sambanova.svg" width="60" height="60" alt="SambaNova"/>
        <br><sub><b>SambaNova</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/grok.svg" width="60" height="60" alt="Grok"/>
        <br><sub><b>Grok</b></sub>
      </td>
      <td align="center" width="120">
        <img src="site/static/img/providers/tars.svg" width="60" height="60" alt="Tetrate Agent Router Service"/>
        <br><sub><b>Tetrate Agent Router Service</b></sub>
      </td>
    </tr>
    <tr>
      <td align="center" width="120">
        <img src="site/static/img/providers/anthropic.svg" width="60" height="60" alt="Anthropic"/>
        <br><sub><b>Anthropic</b></sub>
      </td>
    </tr>
  </table>
</div>

## Documentation

- [Blog](https://aigateway.envoyproxy.io/blog) introducing Envoy AI Gateway.
- [Documentation](https://aigateway.envoyproxy.io/docs) for Envoy AI Gateway.
- [Quickstart](https://aigateway.envoyproxy.io/docs/getting-started/) to use Envoy AI Gateway in a few simple steps.
- [Concepts](https://aigateway.envoyproxy.io/docs/concepts/) to understand the architecture and resources of Envoy AI Gateway.

## Contact

- Slack: Join the [Envoy Slack workspace][] if you're not already a member. Otherwise, use the
  [Envoy AI Gateway channel][] to start collaborating with the community.

## Get Involved

We adhere to the [CNCF Code of conduct][Code of conduct]

The Envoy AI Gateway team and community members meet every Monday.
Please register for the meeting, add agenda points, and get involved. The
meeting details are available in the [public document][meeting].

To contribute to the project via pull requests, please read the [CONTRIBUTING.md](CONTRIBUTING.md) file
which includes information on how to build and test the project.

## Background

The proposal of using Envoy Gateway as a [Cloud Native LLM Gateway][Cloud Native LLM Gateway] inspired the initiation of this project.

[meeting]: https://docs.google.com/document/d/10e1sfsF-3G3Du5nBHGmLjXw5GVMqqCvFDqp_O65B0_w/edit?tab=t.0
[Envoy Slack workspace]: https://communityinviter.com/apps/envoyproxy/envoy
[Envoy AI Gateway channel]: https://envoyproxy.slack.com/archives/C07Q4N24VAA
[Code of conduct]: https://github.com/cncf/foundation/blob/main/code-of-conduct.md
[Cloud Native LLM Gateway]: https://docs.google.com/document/d/1FQN_hGhTNeoTgV5Jj16ialzaSiAxC0ozxH1D9ngCVew/edit?tab=t.0#heading=h.uuu99yemq4eo
