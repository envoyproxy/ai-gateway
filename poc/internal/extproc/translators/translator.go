package translators

import (
	"fmt"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
)

// TranslatorFactory creates a [Translator] for the given API schema combination and path.
type TranslatorFactory func(path string, l *slog.Logger) (Translator, error)

// NewTranslatorFactory returns a callback function that creates a translator for the given API schema combination.
func NewTranslatorFactory(in, out aigv1a1.LLMRouteAPISchema) (TranslatorFactory, error) {
	if in == aigv1a1.LLMRouteAPISchemaOpenAI {
		switch out {
		case aigv1a1.LLMRouteAPISchemaOpenAI:
			return newOpenAIToOpenAITranslator, nil
		case aigv1a1.LLMRouteAPISchemaAWSBedrock:
			return newOpenAIToAWSBedrockTranslator, nil
		}
	}
	return nil, fmt.Errorf("unsupported API schema combination: client=%s, backend=%s", in, out)
}

// Translator translates the request and response messages between the client and the backend API schemas for a specific path.
// The implementation can embed [defaultTranslator] to avoid implementing all methods.
//
// The instance of [Translator] is created by a [TranslatorFactory].
type Translator interface {
	// RequestHeaders translates the request headers.
	RequestHeaders(headers map[string]string) (
		headerMutation *extprocv3.HeaderMutation,
		override *extprocv3http.ProcessingMode,
		err error,
	)
	// RequestBody translates the request body.
	RequestBody(body *extprocv3.HttpBody) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		modelName string,
		err error,
	)
	// ResponseHeaders translates the response headers.
	ResponseHeaders(headers map[string]string) (
		headerMutation *extprocv3.HeaderMutation,
		err error,
	)
	// ResponseBody translates the response body.
	ResponseBody(body *extprocv3.HttpBody) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		usedToken uint32,
		err error,
	)
}

// defaultTranslator is a no-op translator that does not modify the messages.
type defaultTranslator struct{}

// RequestHeaders implements [Translator.RequestHeaders].
func (d *defaultTranslator) RequestHeaders(map[string]string) (*extprocv3.HeaderMutation, *extprocv3http.ProcessingMode, error) {
	return nil, nil, nil
}

// RequestBody implements [Translator.RequestBody].
func (d *defaultTranslator) RequestBody(*extprocv3.HttpBody) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, string, error) {
	return nil, nil, "", nil
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
