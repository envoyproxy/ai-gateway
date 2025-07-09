---
id: security
title: Security
---
As Envoy AI Gateway is built on Envoy Gateway, you can leverage the Envoy Gateway Security Policy by attaching them to the Gateway and/or generated HTTPRoutes.

# Common Security Docs
Below are a list of common security configurations that can be useful when securing your gateway leveraging Envoy Gateway configurations.

## Access Control
- [JWT Validation](https://gateway.envoyproxy.io/docs/tasks/security/jwt-authentication/)
    - Validate signed JWT tokens
- [JWT Claim Based Authorization](https://gateway.envoyproxy.io/docs/tasks/security/jwt-claim-authorization/)
    - Assert values of claims in JWT tokens
- [Mutual TLS](https://gateway.envoyproxy.io/docs/tasks/security/mutual-tls/)
    - Certificate based access control
- [Integrate with External Authorization Sevice](https://gateway.envoyproxy.io/docs/tasks/security/ext-auth/)
    - Useful for custom logic to your business
- [OIDC Provider Integation](https://gateway.envoyproxy.io/docs/tasks/security/oidc/)
    - When you want to have integration with a login flow for an end-user
- [Require Basic Auth](https://gateway.envoyproxy.io/docs/tasks/security/basic-auth/)
- [Require API Key](https://gateway.envoyproxy.io/docs/tasks/security/apikey-auth/)
- [IP Allowlist/Denylist](https://gateway.envoyproxy.io/docs/tasks/security/restrict-ip-access/)

## TLS
- [Setup TLS Certificate](https://gateway.envoyproxy.io/docs/tasks/security/secure-gateways/)
- [Using TLS cert-manager](https://gateway.envoyproxy.io/docs/tasks/security/tls-cert-manager/)


[View all security docs on the Envoy Gateway docs site to learn more.](https://gateway.envoyproxy.io/docs/tasks/security/)
