package translator

import (
	"fmt"
	"io"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
)

// Factory creates a [Translator] for the given API schema combination and request path.
//
//   - `path`: the path of the request.
//   - `l`: the logger.
type Factory func(path string) (Translator, error)

// NewFactory returns a callback function that creates a translator for the given API schema combination.
func NewFactory(in, out extprocconfig.VersionedAPISchema) (Factory, error) {
	if in.Schema == extprocconfig.APISchemaOpenAI {
		// TODO: currently, we ignore the LLMAPISchema."Version" field.
		switch out.Schema {
		case extprocconfig.APISchemaOpenAI:
			return newOpenAIToOpenAITranslator, nil
		case extprocconfig.APISchemaAWSBedrock:
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
	// RequestBody translates the request body.
	// 	- `body` is the request body already parsed by [router.RequestBodyParser]. The concrete type is specific to the schema and the path.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	//  - This returns `override` that to change the processing mode. This is used to process streaming requests properly.
	RequestBody(body router.RequestBody) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		override *extprocv3http.ProcessingMode,
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
	ResponseBody(body io.Reader, endOfStream bool) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		usedToken uint32,
		err error,
	)
}

// defaultTranslator is a no-op translator that implements [Translator].
type defaultTranslator struct{}

// RequestBody implements [Translator.RequestBody].
func (d *defaultTranslator) RequestBody(*extprocv3.HttpBody) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, *extprocv3http.ProcessingMode, string, error) {
	return nil, nil, nil, "", nil
}

// ResponseHeaders implements [Translator.ResponseBody].
func (d *defaultTranslator) ResponseHeaders(map[string]string) (*extprocv3.HeaderMutation, error) {
	return nil, nil
}

// ResponseBody implements [Translator.ResponseBody].
func (d *defaultTranslator) ResponseBody(io.Reader, bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, uint32, error) {
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
