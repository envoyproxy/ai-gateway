package extproc

import (
	"testing"

	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
)

var (
	_ translator.Translator = &mockTranslator{}
	_ router.Router         = &mockRouter{}
)

// mockTranslator implements [translator.Translator] for testing.
type mockTranslator struct {
	t                 *testing.T
	expHeaders        map[string]string
	expRequestBody    router.RequestBody
	expResponseBody   *extprocv3.HttpBody
	retHeaderMutation *extprocv3.HeaderMutation
	retBodyMutation   *extprocv3.BodyMutation
	retOverride       *extprocv3http.ProcessingMode
	retErr            error
}

// RequestBody implements [translator.Translator.RequestBody].
func (m mockTranslator) RequestBody(body router.RequestBody) (headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, override *extprocv3http.ProcessingMode, err error) {
	require.Equal(m.t, m.expRequestBody, body)
	return m.retHeaderMutation, m.retBodyMutation, m.retOverride, m.retErr
}

// ResponseHeaders implements [translator.Translator.ResponseHeaders].
func (m mockTranslator) ResponseHeaders(headers map[string]string) (headerMutation *extprocv3.HeaderMutation, err error) {
	require.Equal(m.t, m.expHeaders, headers)
	return m.retHeaderMutation, m.retErr
}

// ResponseBody implements [translator.Translator.ResponseBody].
func (m mockTranslator) ResponseBody(body *extprocv3.HttpBody) (headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, usedToken uint32, err error) {
	require.Equal(m.t, m.expResponseBody, body)
	return m.retHeaderMutation, m.retBodyMutation, usedToken, m.retErr
}

// mockRouter implements [router.Router] for testing.
type mockRouter struct {
	t                     *testing.T
	expHeaders            map[string]string
	retBackendName        string
	retVersionedAPISchema extprocconfig.VersionedAPISchema
	retErr                error
}

// Calculate implements [router.Router.Calculate].
func (m mockRouter) Calculate(headers map[string]string) (string, extprocconfig.VersionedAPISchema, error) {
	require.Equal(m.t, m.expHeaders, headers)
	return m.retBackendName, m.retVersionedAPISchema, m.retErr
}

// mockRequestBodyParser implements [router.RequestBodyParser] for testing.
type mockRequestBodyParser struct {
	t            *testing.T
	expPath      string
	expBody      []byte
	retModelName string
	retRb        router.RequestBody
	retErr       error
}

// impl implements [router.RequestBodyParser].
func (m *mockRequestBodyParser) impl(path string, body *extprocv3.HttpBody) (modelName string, rb router.RequestBody, err error) {
	require.Equal(m.t, m.expPath, path)
	require.Equal(m.t, m.expBody, body.Body)
	return m.retModelName, m.retRb, m.retErr
}

// mockTranslatorFactory implements [translator.Factory] for testing.
type mockTranslatorFactory struct {
	t             *testing.T
	expPath       string
	retTranslator translator.Translator
	retErr        error
}

// NewTranslator implements [translator.Factory].
func (m mockTranslatorFactory) impl(path string) (translator.Translator, error) {
	require.Equal(m.t, m.expPath, path)
	return m.retTranslator, m.retErr
}
