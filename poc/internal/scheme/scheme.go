package scheme

import (
	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1b1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
)

// scheme contains all the API types necessary for the provider's dynamic
// clients to work. Any new non-core types must be added here.
//
// NOTE: The discovery mechanism used by the client doesn't automatically
// refresh, so only add types here that are guaranteed to exist before the
// provider starts.
var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	// Add AI Gateway types.
	utilruntime.Must(aigv1a1.AddToScheme(scheme))
	// Add CRD kind to known types, experimental conformance test requires this.
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	// Add Envoy Gateway API types.
	utilruntime.Must(egv1a1.AddToScheme(scheme))
	// Add Gateway API types.
	utilruntime.Must(gwapiv1.Install(scheme))
	utilruntime.Must(gwapiv1b1.Install(scheme))
}

// GetScheme returns a scheme with types supported by the Kubernetes provider.
func GetScheme() *runtime.Scheme {
	return scheme
}
