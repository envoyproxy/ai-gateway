// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_translate(t *testing.T) {
	yamlInput := `
# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: envoy-ai-gateway-basic
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: envoy-ai-gateway-basic
  namespace: default
spec:
  gatewayClassName: envoy-ai-gateway-basic
  listeners:
    - name: http
      protocol: HTTP
      port: 80
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIGatewayRoute
metadata:
  name: envoy-ai-gateway-basic
  namespace: default
spec:
  schema:
    name: OpenAI
  targetRefs:
    - name: envoy-ai-gateway-basic
      kind: Gateway
      group: gateway.networking.k8s.io
  rules:
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: gpt-4o-mini
      backendRefs:
        - name: envoy-ai-gateway-basic-openai
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: us.meta.llama3-2-1b-instruct-v1:0
      backendRefs:
        - name: envoy-ai-gateway-basic-aws
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: some-cool-self-hosted-model
      backendRefs:
        - name: envoy-ai-gateway-basic-testupstream
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: envoy-ai-gateway-basic-openai
  namespace: default
spec:
  schema:
    name: OpenAI
  backendRef:
    name: envoy-ai-gateway-basic-openai
    kind: Backend
    group: gateway.envoyproxy.io
  backendSecurityPolicyRef:
    name: envoy-ai-gateway-basic-openai-apikey
    kind: BackendSecurityPolicy
    group: aigateway.envoyproxy.io
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: envoy-ai-gateway-basic-aws
  namespace: default
spec:
  schema:
    name: AWSBedrock
  backendRef:
    name: envoy-ai-gateway-basic-aws
    kind: Backend
    group: gateway.envoyproxy.io
  backendSecurityPolicyRef:
    name: envoy-ai-gateway-basic-aws-credentials
    kind: BackendSecurityPolicy
    group: aigateway.envoyproxy.io
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: BackendSecurityPolicy
metadata:
  name: envoy-ai-gateway-basic-openai-apikey
  namespace: default
spec:
  type: APIKey
  apiKey:
    secretRef:
      name: envoy-ai-gateway-basic-openai-apikey
      namespace: default
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: BackendSecurityPolicy
metadata:
  name: envoy-ai-gateway-basic-aws-credentials
  namespace: default
spec:
  type: AWSCredentials
  awsCredentials:
    region: us-east-1
    credentialsFile:
      secretRef:
        name: envoy-ai-gateway-basic-aws-credentials
---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: Backend
metadata:
  name: envoy-ai-gateway-basic-openai
  namespace: default
spec:
  endpoints:
    - fqdn:
        hostname: api.openai.com
        port: 443
---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: Backend
metadata:
  name: envoy-ai-gateway-basic-aws
  namespace: default
spec:
  endpoints:
    - fqdn:
        hostname: bedrock-runtime.us-east-1.amazonaws.com
        port: 443
---
apiVersion: gateway.networking.k8s.io/v1alpha3
kind: BackendTLSPolicy
metadata:
  name: envoy-ai-gateway-basic-openai-tls
  namespace: default
spec:
  targetRefs:
    - group: 'gateway.envoyproxy.io'
      kind: Backend
      name: envoy-ai-gateway-basic-openai
  validation:
    wellKnownCACertificates: "System"
    hostname: api.openai.com
---
apiVersion: gateway.networking.k8s.io/v1alpha3
kind: BackendTLSPolicy
metadata:
  name: envoy-ai-gateway-basic-aws-tls
  namespace: default
spec:
  targetRefs:
    - group: 'gateway.envoyproxy.io'
      kind: Backend
      name: envoy-ai-gateway-basic-aws
  validation:
    wellKnownCACertificates: "System"
    hostname: bedrock-runtime.us-east-1.amazonaws.com
---
apiVersion: v1
kind: Secret
metadata:
  name: envoy-ai-gateway-basic-openai-apikey
  namespace: default
type: Opaque
stringData:
  apiKey: OPENAI_API_KEY  # Replace with your OpenAI API key.
---
apiVersion: v1
kind: Secret
metadata:
  name: envoy-ai-gateway-basic-aws-credentials
  namespace: default
type: Opaque
stringData:
  # Replace with your AWS credentials.
  credentials: |
    [default]
    aws_access_key_id = AWS_ACCESS_KEY_ID
    aws_secret_access_key = AWS_SECRET_ACCESS_KEY
---
apiVersion: aigateway.envoyproxy.io/v1alpha1
kind: AIServiceBackend
metadata:
  name: envoy-ai-gateway-basic-testupstream
  namespace: default
spec:
  schema:
    name: OpenAI
  backendRef:
    name: envoy-ai-gateway-basic-testupstream
    kind: Service
    port: 80
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: envoy-ai-gateway-basic-testupstream
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: envoy-ai-gateway-basic-testupstream
  template:
    metadata:
      labels:
        app: envoy-ai-gateway-basic-testupstream
    spec:
      containers:
        - name: testupstream
          image: docker.io/envoyproxy/ai-gateway-testupstream:latest
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8080
          env:
            - name: TESTUPSTREAM_ID
              value: test
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 1
            periodSeconds: 1
---
apiVersion: v1
kind: Service
metadata:
  name: envoy-ai-gateway-basic-testupstream
  namespace: default
spec:
  selector:
    app: envoy-ai-gateway-basic-testupstream
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8080
  type: ClusterIP
`
	tmpFile := t.TempDir() + "/test.yaml"
	err := os.WriteFile(tmpFile, []byte(yamlInput), 0o600)
	require.NoError(t, err)
	err = translate(cmdTranslate{Paths: []string{tmpFile}}, os.Stdout, os.Stderr)
	require.NoError(t, err)
}
