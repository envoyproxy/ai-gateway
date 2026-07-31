# Proposal: Optimize MCP Initialize Phase via Backend-Only Authorization Pre-Check

## 1. Motivation

In the current architecture of the Envoy AI Gateway (AIGW) MCP Proxy, when a client connects and sends an `initialize` JSON-RPC request, the proxy establishes an upstream SSE (Server-Sent Events) connection to **all** backends defined in the route. It then asks each backend for its list of available tools. 

Authorization checks (via Common Expression Language, or CEL) are only performed *after* initialization, when the client sends a `tools/list` or `tools/call` request. 

Consider the following route configuration (represented as JSON for dynamic injection):

```json
{
  "name": "tenant-abc",
  "backends": [
    {
      "name": "github",
      "host": "api.githubcopilot.com",
      "backendPath": "/mcp",
      "auth": "Bearer ghp_xxx..."
    },
    {
      "name": "servicenow",
      "host": "instance.service-now.com",
      "backendPath": "/mcp/v1",
      "auth": "Bearer sn_token_xxx..."
    }
  ],
  "authorization": {
    "defaultAction": "Deny",
    "rules": [
      {
        "action": "Allow",
        "source": { "jwt": { "scopes": ["mcp:admin"] } },
        "target": { "tools": [{ "backend": "*", "tool": "*" }] }
      },
      {
        "action": "Allow",
        "source": { "jwt": { "scopes": ["mcp:snuser"] } },
        "target": { "tools": [{ "backend": "servicenow", "tool": "*" }] }
      }
    ]
  }
}
```

If a client connects with a JWT containing only the `mcp:snuser` scope, they are mathematically barred from accessing any tools on the `github` backend. However, the MCP Proxy will **still establish a connection to `github`** during the `initialize` phase.

**The Problem:**
This behavior causes several issues depending on the authentication configuration:

1. **Service Account Overhead:** If the proxy uses a hardcoded outbound credential (`auth: "Bearer ghp_xxx"`), the connection succeeds silently. The proxy wastes network I/O, memory, and compute to fetch and parse the GitHub tool list, only to completely hide it from the user during `tools/list`.
2. **Pass-Through Authentication Failures:** If the proxy relies on forwarding the client's JWT to authenticate with the upstream backend, the `initialize` call to GitHub will fail (e.g., 401 Unauthorized) because the ServiceNow token is invalid for GitHub. While AIGW gracefully recovers from single-backend connection failures, relying on network-level 401s as a secondary form of access control generates unnecessary error logs, false-positive alerts, and wastes network resources.
3. **Latency:** The client's initialization phase blocks until all backend connections (or their failure timeouts) resolve.

## 2. Why it behaves this way currently

Currently, the MCP Proxy (`internal/mcpproxy/authorization.go`) compiles authorization rules into a single CEL expression per rule. These expressions evaluate properties from the client request against both the backend and the specific tool name.

For example, a rule allowing a user to access a specific tool on GitHub is compiled to something like:
```cel
(request.Backend == "github") && (request.Tool == "list_issues") && ...
```

During the `initialize` phase, the client has not requested a specific tool yet. If the proxy tries to evaluate the CEL expression without a tool name, the expression either fails or correctly evaluates to `false`. Because the proxy cannot evaluate the rule without the tool name, it defers all authorization checks until the `tools/list` or `tools/call` phase, meaning it must connect to all backends initially.

## 3. Proposed Solution

We propose optimizing the `initialize` phase entirely within the `mcpproxy` component.

We can achieve this by having `compileAuthorization` in `mcpproxy/authorization.go` generate **two** CEL expressions for every rule:
1. **The Full Expression (Existing):** Evaluates the backend, tool, and client context. Used for `tools/list` and `tools/call`.
2. **A Backend-Only Expression (New):** A secondary CEL expression that strips out any constraints related to the tool name (`target.tools.tool`). It only evaluates the client context against the backend name (`target.tools.backend`).

### 3.1. Compilation Updates

In `internal/mcpproxy/authorization.go`, we update the internal structs:

```go
type compiledAuthorizationRule struct {
    Action               filterapi.AuthorizationAction
    celExpression        string
    backendOnlyExpression string // NEW: CEL string omitting tool checks
    
    program              cel.Program
    backendOnlyProgram   cel.Program // NEW: Compiled CEL program for backend pruning
    // ...
}
```

When iterating over the `Target.Tools` during compilation, the compiler will build the standard CEL string, and a second string that skips appending the `&& request.Tool == ...` clause.

### 3.2. Evaluation Updates

We introduce a new authorization function specifically for pre-checking backends during initialization:

```go
// authorizeBackendOnly evaluates whether the client has any potential access to the backend,
// ignoring specific tool constraints.
func (m *mcpRequestContext) authorizeBackendOnly(auth *compiledAuthorization, req *authorizationRequest) bool {
    // Evaluates against rule.backendOnlyProgram
}
```

### 3.3. Proxy `newSession` Optimization

Finally, we update the initialization loop in `mcpproxy.go`:

```go
func (m *mcpRequestContext) newSession(...) (*session, error) {
    // ...
    for _, backend := range backends.backends {
        if backends.authorization != nil {
            // Build a request without a tool name
            req := &authorizationRequest{
                Headers: m.requestHeaders,
                Backend: backend.Name,
            }
            
            // Pre-check the backend!
            allowed, _ := m.authorizeBackendOnly(backends.authorization, req)
            if !allowed {
                m.l.Debug("skipping backend connection due to authorization rules", slog.String("backend", backend.Name))
                continue // Prune the backend connection!
            }
        }
        
        // Only connect to backends the user actually has permission to see
        initResult, err := m.initializeSession(ctx, routeName, &backend, p, startAt)
    }
}
```

## 4. Dynamic Multi-Tenancy & Subsetting via CEL

In enterprise architectures where client authentication and tenant-level authorization are handled by a separate, specialized process (such as a JWT Auth filter, an Envoy `ext_authz` service, or a custom External Processor), the allowed scope of backends and tools can be computed externally and passed as metadata headers to the MCP Proxy. 

By leveraging the pre-check engine outlined in Section 3, the MCP Proxy can natively enforce these multi-tenant constraints dynamically using declarative, CEL-based SecurityPolicies.

### 4.1. Implementation Design: Unified Fully-Qualified Tool Names (FQN)
In this design, the trusted external orchestrator computes a list of allowed tools formatted as `backendName__toolName` and injects them into a single header: `x-ai-eg-mcp-tool-subset`.

#### Example Header Value:
```http
x-ai-eg-mcp-tool-subset: database-mcp__read_record,email-mcp__send_alert
```

#### Declarative MCPRoute YAML Configuration:
```yaml
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: MCPRoute
metadata:
  name: dynamic-multi-tenant-route
spec:
  parentRefs:
    - name: ai-gateway
  path: /mcp
  backendRefs:
    - name: database-mcp
    - name: email-mcp
    - name: analytics-mcp
  securityPolicy:
    authorization:
      defaultAction: Deny
      rules:
        # Rule 1: Initialize Pre-Check (Connect only to backends that have at least one tool in the subset)
        - name: dynamic-backend-initialize-allow
          action: Allow
          cel: |
            request.mcp.method == "initialize" &&
            has(request.headers["x-ai-eg-mcp-tool-subset"]) &&
            ("," + request.headers["x-ai-eg-mcp-tool-subset"]).contains("," + request.mcp.backend + "__")

        # Rule 2: Tools List & Call (Match exact fully-qualified tool name)
        - name: dynamic-tool-call-and-list-allow
          action: Allow
          cel: |
            request.mcp.method != "initialize" &&
            has(request.headers["x-ai-eg-mcp-tool-subset"]) &&
            ("," + request.headers["x-ai-eg-mcp-tool-subset"] + ",").contains("," + request.mcp.backend + "__" + request.mcp.tool + ",")
```

### 4.2. Architectural & Security Advantages
1. **Decoupled Architecture**: All tenant-lookup and permission databases remain encapsulated within the identity management process. The AI Gateway proxy remains a high-performance, stateless data-plane.
2. **Dynamic yet Declarative**: We avoid writing custom, rigid Go code or hardcoding proxy behavior. All multi-tenant routing decisions and scoping rules are handled elegantly through standard CEL expressions.
3. **Multi-layer Safeguards**: Standard CEL rules can be securely locked. For instance, because Envoy is configured to strip client-supplied headers and only inject trusted headers from verified JWT claims or dynamic metadata, the client cannot bypass these rules.
4. **Defense-in-Depth**: Because the CEL rule gates `tools/call` at runtime in addition to filtering `tools/list`, if a client attempts to forge a request to invoke an unauthorized tool, the proxy rejects the execution natively.

---

## 5. Benefits

1. **Efficiency:** Reduces upstream connection overhead and memory usage in the proxy.
2. **Latency:** Faster initialization for clients, especially in multi-tenant environments where routes map to dozens of backends.
3. **Security Posture:** Enforces backend isolation earlier in the connection lifecycle (Defense in Depth).
4. **Decoupled Control**: Perfectly supports enterprise multi-tenancy where routing and permissions are driven dynamically by separate authorization services.

