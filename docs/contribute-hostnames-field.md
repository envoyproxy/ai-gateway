# Contribution 분석: AIGatewayRoute에 `hostnames` 필드 추가

## 1. 개요

AIGatewayRoute에 `hostnames` 필드를 추가하여, 생성되는 HTTPRoute에 hostname 기반 필터링을 지원하는 기능.

- **대상 저장소**: `envoyproxy/ai-gateway`
- **관련 이슈**: https://github.com/envoyproxy/ai-gateway/issues/695

## 2. 코드 변경 영향 분석

### 수정 대상 파일

| 파일                                           | 변경 내용                                                                     |
| ---------------------------------------------- | ----------------------------------------------------------------------------- |
| `api/v1beta1/ai_gateway_route.go`              | `AIGatewayRouteSpec`에 `Hostnames` 필드 추가                                  |
| `api/v1alpha1/ai_gateway_route.go`             | `AIGatewayRouteSpec`에 `Hostnames` 필드 추가 (deprecated version도 동일 지원) |
| `api/v1beta1/zz_generated.deepcopy.go`         | `make apigen`으로 자동 생성                                                   |
| `api/v1alpha1/zz_generated.deepcopy.go`        | `make apigen`으로 자동 생성                                                   |
| `internal/controller/ai_gateway_route.go`      | `newHTTPRoute()` 메서드에서 Hostnames를 HTTPRoute에 전달                      |
| `internal/controller/ai_gateway_route_test.go` | Hostnames 전달 관련 테스트 추가                                               |

### 핵심 코드 포인트

**1) API 정의** (`api/v1beta1/ai_gateway_route.go:57`)

현재 `AIGatewayRouteSpec` 구조체:

```go
type AIGatewayRouteSpec struct {
	ParentRefs      []gwapiv1.ParentReference `json:"parentRefs,omitempty"`
	Rules           []AIGatewayRouteRule      `json:"rules"`
	LLMRequestCosts []LLMRequestCost          `json:"llmRequestCosts,omitempty"`
}
```

추가할 필드:

```go
    // Hostnames is a list of hostnames matched against the HTTP Host header.
    // This is equivalent to the Hostnames field in the Gateway API HTTPRouteSpec.
    // When specified, the generated HTTPRoute will include these hostnames for
    // hostname-based filtering.
    //
    // +optional
    // +kubebuilder:validation:MaxItems=16
    Hostnames []gwapiv1.Hostname `json:"hostnames,omitempty"`
```

**2) HTTPRoute 생성 로직** (`internal/controller/ai_gateway_route.go:246-358`)

`newHTTPRoute()` 메서드의 마지막 부분 (line 357):

```go
dst.Spec.ParentRefs = aiGatewayRoute.Spec.ParentRefs
```

여기에 한 줄 추가:

```go
dst.Spec.ParentRefs = aiGatewayRoute.Spec.ParentRefs
dst.Spec.Hostnames = aiGatewayRoute.Spec.Hostnames // <-- 추가
```

## 3. Contribution 절차 (단계별)

### Step 1: 사전 준비

```bash
# Fork: GitHub에서 envoyproxy/ai-gateway Fork
# Clone
git clone https://github.com/ < your-username > /ai-gateway.git
cd ai-gateway
git remote add upstream https://github.com/envoyproxy/ai-gateway.git

# 브랜치 생성
git checkout -b feat/add-hostnames-to-aigatewayroute
```

### Step 2: API 변경

1. `api/v1beta1/ai_gateway_route.go` — `AIGatewayRouteSpec`에 `Hostnames` 필드 추가
2. `api/v1alpha1/ai_gateway_route.go` — 동일하게 추가
3. CRD 및 deepcopy 자동 생성:

```bash
make apigen
```

### Step 3: 컨트롤러 변경

1. `internal/controller/ai_gateway_route.go`의 `newHTTPRoute()` — Hostnames 전달 로직 추가

### Step 4: 테스트 작성

1. `internal/controller/ai_gateway_route_test.go` — Hostnames가 HTTPRoute로 정상 전달되는지 검증

### Step 5: 로컬 검증

```bash
make precommit test
```

### Step 6: 커밋 & PR

```bash
git add -A
git commit -s -m "feat: add hostnames field to AIGatewayRoute spec"
git push origin feat/add-hostnames-to-aigatewayroute
```

PR 생성 시:

- **제목**: `feat: add hostnames field to AIGatewayRoute spec`
- **설명**: Feature Request 내용 포함, AI 사용 여부 명시
- **DCO sign-off** 필수 (`-s` 플래그)

### Step 7: Code Review 대응

- 리뷰 수정 시 squash/force push 하지 않고 새 커밋으로 추가
- 브랜치 동기화는 `git merge` 사용

## 4. 변경 범위 평가

| 항목        | 평가                                          |
| ----------- | --------------------------------------------- |
| 난이도      | **낮음** — ParentRefs 전달 패턴과 동일        |
| 코드 변경량 | API 필드 추가 + 컨트롤러 1줄 + 테스트         |
| 리스크      | **낮음** — optional 필드, 기존 동작 영향 없음 |
| 자동 생성   | CRD, deepcopy는 `make apigen`으로 처리        |

## 5. 참고

- Gateway API HTTPRoute `Hostnames` 스펙: `gwapiv1.Hostname` 타입 (`sigs.k8s.io/gateway-api/apis/v1`)
- `ParentRefs`가 이미 동일한 패턴으로 pass-through 되고 있어 구현 참고 가능
- 기존 테스트 패턴은 `internal/controller/ai_gateway_route_test.go` 참고
