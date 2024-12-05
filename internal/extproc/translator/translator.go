package translator

import (
	"fmt"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

// Factory creates a [Translator] for the given API schema combination and request path.
//
//   - `path`: the path of the request.
//   - `l`: the logger.
type Factory func(path string, l *slog.Logger) (Translator, error)

// NewFactory returns a callback function that creates a translator for the given API schema combination.
func NewFactory(in, out aigv1a1.LLMAPISchema) (Factory, error) {
	if in.Schema == aigv1a1.APISchemaOpenAI {
		// TODO: currently, we ignore the LLMAPISchema."Version" field.
		switch out.Schema {
		case aigv1a1.APISchemaOpenAI:
			return newOpenAIToOpenAITranslator, nil
		case aigv1a1.APISchemaAWSBedrock:
			return newOpenAIToAWSBedrockTranslator, nil
		}
	}
	return nil, fmt.Errorf("unsupported API schema combination: client=%s, backend=%s", in, out)
}

// Translator translates the request and response messages between the client and the backend API schemas for a specific path.
// The implementation can embed [defaultTranslator] to avoid implementing all methods.
//
// The instance of [Translator] is created by a [Factory].
//
// This is created per request and is not thread-safe.
type Translator interface {
	// RequestHeaders translates the request headers.
	//	- `headers` is the request headers.
	//	- This returns `headerMutation` that can be nil to indicate no mutation.
	RequestHeaders(headers map[string]string) (
		headerMutation *extprocv3.HeaderMutation,
		err error,
	)

	// RequestBody translates the request body.
	// 	- `body` is the request body either chunk or the entire body, depending on the context.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	//  - This returns `override` that to change the processing mode. This is used to process streaming requests properly.
	// 	- This returns `modelName` that is extracted from the body.
	RequestBody(body *extprocv3.HttpBody) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		override *extprocv3http.ProcessingMode,
		modelName string,
		err error,
	)

	// ResponseHeaders translates the response headers.
	// 	- `headers` is the response headers.
	//	- This returns `headerMutation` that can be nil to indicate no mutation.
	ResponseHeaders(headers map[string]string) (
		headerMutation *extprocv3.HeaderMutation,
		err error,
	)

	// ResponseBody translates the response body.
	// 	- `body` is the response body either chunk or the entire body, depending on the context.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	//  - This returns `usedToken` that is extracted from the body and will be used to do token rate limiting.
	ResponseBody(body *extprocv3.HttpBody) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		usedToken uint32,
		err error,
	)
}

// defaultTranslator is a no-op translator that implements [Translator].
type defaultTranslator struct{}

// RequestHeaders implements [Translator.RequestHeaders].
func (d *defaultTranslator) RequestHeaders(map[string]string) (*extprocv3.HeaderMutation, error) {
	return nil, nil
}

// RequestBody implements [Translator.RequestBody].
func (d *defaultTranslator) RequestBody(*extprocv3.HttpBody) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, *extprocv3http.ProcessingMode, string, error) {
	return nil, nil, nil, "", nil
}

// ResponseHeaders implements [Translator.ResponseBody].
func (d *defaultTranslator) ResponseHeaders(map[string]string) (*extprocv3.HeaderMutation, error) {
	return nil, nil
}

// ResponseBody implements [Translator.ResponseBody].
func (d *defaultTranslator) ResponseBody(*extprocv3.HttpBody) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, uint32, error) {
	return nil, nil, 0, nil
}

func setContentLength(headers *extprocv3.HeaderMutation, body []byte) {
	headers.SetHeaders = append(headers.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:      "content-length",
			RawValue: []byte(fmt.Sprintf("%d", len(body))),
		},
	})
}
