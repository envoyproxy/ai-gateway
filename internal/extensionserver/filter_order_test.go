// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/envoyproxy/ai-gateway/internal/controller"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestParseFilterOrderAnnotation(t *testing.T) {
	t.Run("backward compat: before-extproc", func(t *testing.T) {
		r, err := parseFilterOrderAnnotation("before-extproc")
		require.NoError(t, err)
		require.Equal(t, extensionFilterPrefixes, r.beforePrefixes)
		require.Empty(t, r.afterPrefixes)
	})

	t.Run("backward compat: before-extproc case-insensitive", func(t *testing.T) {
		r, err := parseFilterOrderAnnotation("Before-ExtProc")
		require.NoError(t, err)
		require.Equal(t, extensionFilterPrefixes, r.beforePrefixes)
		require.Empty(t, r.afterPrefixes)
	})

	t.Run("sequence: all before pivot", func(t *testing.T) {
		r, err := parseFilterOrderAnnotation("Lua,Wasm,ExtProc")
		require.NoError(t, err)
		require.Equal(t, []string{
			egv1a1.EnvoyFilterLua.String() + "/",
			egv1a1.EnvoyFilterWasm.String() + "/",
		}, r.beforePrefixes)
		require.Empty(t, r.afterPrefixes)
	})

	t.Run("sequence: before and after pivot", func(t *testing.T) {
		r, err := parseFilterOrderAnnotation("Wasm,Lua,ExtProc,LocalRateLimit,RateLimit")
		require.NoError(t, err)
		require.Equal(t, []string{
			egv1a1.EnvoyFilterWasm.String() + "/",
			egv1a1.EnvoyFilterLua.String() + "/",
		}, r.beforePrefixes)
		require.Equal(t, []string{
			egv1a1.EnvoyFilterLocalRateLimit.String(),
			egv1a1.EnvoyFilterRateLimit.String(),
		}, r.afterPrefixes)
	})

	t.Run("sequence: after only", func(t *testing.T) {
		r, err := parseFilterOrderAnnotation("ExtProc,rbac,LocalRateLimit")
		require.NoError(t, err)
		require.Empty(t, r.beforePrefixes)
		require.Equal(t, []string{
			egv1a1.EnvoyFilterRBAC.String(),
			egv1a1.EnvoyFilterLocalRateLimit.String(),
		}, r.afterPrefixes)
	})

	t.Run("sequence: no pivot — all treated as before", func(t *testing.T) {
		r, err := parseFilterOrderAnnotation("Lua,rbac")
		require.NoError(t, err)
		require.Equal(t, []string{
			egv1a1.EnvoyFilterLua.String() + "/",
			egv1a1.EnvoyFilterRBAC.String(),
		}, r.beforePrefixes)
		require.Empty(t, r.afterPrefixes)
	})

	t.Run("sequence: case-insensitive tokens", func(t *testing.T) {
		r, err := parseFilterOrderAnnotation("lua,WASM,extproc,RBAC")
		require.NoError(t, err)
		require.Equal(t, []string{
			egv1a1.EnvoyFilterLua.String() + "/",
			egv1a1.EnvoyFilterWasm.String() + "/",
		}, r.beforePrefixes)
		require.Equal(t, []string{egv1a1.EnvoyFilterRBAC.String()}, r.afterPrefixes)
	})

	t.Run("sequence: spaces around tokens", func(t *testing.T) {
		r, err := parseFilterOrderAnnotation("  Lua , Wasm , ExtProc , rbac  ")
		require.NoError(t, err)
		require.Equal(t, []string{
			egv1a1.EnvoyFilterLua.String() + "/",
			egv1a1.EnvoyFilterWasm.String() + "/",
		}, r.beforePrefixes)
		require.Equal(t, []string{egv1a1.EnvoyFilterRBAC.String()}, r.afterPrefixes)
	})

	t.Run("sequence: DynamicModules token", func(t *testing.T) {
		r, err := parseFilterOrderAnnotation("DynamicModules,ExtProc")
		require.NoError(t, err)
		require.Equal(t, []string{egv1a1.EnvoyFilterDynamicModules.String() + "/"}, r.beforePrefixes)
		require.Empty(t, r.afterPrefixes)
	})

	t.Run("unknown token returns error", func(t *testing.T) {
		_, err := parseFilterOrderAnnotation("Lua,UnknownFilter,ExtProc")
		require.Error(t, err)
		require.Contains(t, err.Error(), "UnknownFilter")
	})
}

func TestBuildFilterOrderSets(t *testing.T) {
	makePolicy := func(namespace, name, targetName, annotationVal string) egv1a1.EnvoyExtensionPolicy {
		return egv1a1.EnvoyExtensionPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
				Annotations: map[string]string{
					internalapi.FilterOrderAnnotation: annotationVal,
				},
			},
			Spec: egv1a1.EnvoyExtensionPolicySpec{
				PolicyTargetReferences: egv1a1.PolicyTargetReferences{
					TargetRefs: []gwapiv1.LocalPolicyTargetReferenceWithSectionName{
						{LocalPolicyTargetReference: gwapiv1.LocalPolicyTargetReference{Name: gwapiv1.ObjectName(targetName)}},
					},
				},
			},
		}
	}

	t.Run("no policies — both sets nil", func(t *testing.T) {
		before, after := buildFilterOrderSets(nil, nil)
		require.Nil(t, before)
		require.Nil(t, after)
	})

	t.Run("unannotated policy — both sets nil", func(t *testing.T) {
		policy := egv1a1.EnvoyExtensionPolicy{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "p1"}}
		before, after := buildFilterOrderSets([]egv1a1.EnvoyExtensionPolicy{policy}, nil)
		require.Nil(t, before)
		require.Nil(t, after)
	})

	t.Run("backward compat: before-extproc populates before set", func(t *testing.T) {
		policy := makePolicy("ns", "p1", "myroute", "before-extproc")
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "myroute", []string{"envoy.filters.http.lua/0", "envoy.filters.http.lua/1"}),
				},
			}},
		}}
		before, after := buildFilterOrderSets([]egv1a1.EnvoyExtensionPolicy{policy}, routes)
		require.Equal(t, map[string]bool{
			"envoy.filters.http.lua/0": true,
			"envoy.filters.http.lua/1": true,
		}, before)
		require.Nil(t, after)
	})

	t.Run("sequence: before and after sets populated", func(t *testing.T) {
		policy := makePolicy("ns", "p1", "myroute", "Wasm,Lua,ExtProc,LocalRateLimit,RateLimit")
		route := routeWithLuaMetadata(t, "ns", "myroute", []string{"envoy.filters.http.lua/0"})
		route.TypedPerFilterConfig["envoy.filters.http.wasm/0"] = &anypb.Any{}
		routes := []*routev3.RouteConfiguration{{VirtualHosts: []*routev3.VirtualHost{{Routes: []*routev3.Route{route}}}}}
		before, after := buildFilterOrderSets([]egv1a1.EnvoyExtensionPolicy{policy}, routes)
		require.Equal(t, map[string]bool{
			"envoy.filters.http.lua/0":  true,
			"envoy.filters.http.wasm/0": true,
		}, before)
		require.Equal(t, map[string]bool{
			egv1a1.EnvoyFilterLocalRateLimit.String(): true,
			egv1a1.EnvoyFilterRateLimit.String():      true,
		}, after)
	})

	t.Run("rbac in before set — added directly without route lookup", func(t *testing.T) {
		policy := makePolicy("ns", "p1", "myroute", "rbac,ExtProc")
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{routeWithLuaMetadata(t, "ns", "myroute", nil)},
			}},
		}}
		before, after := buildFilterOrderSets([]egv1a1.EnvoyExtensionPolicy{policy}, routes)
		require.Equal(t, map[string]bool{egv1a1.EnvoyFilterRBAC.String(): true}, before)
		require.Nil(t, after)
	})

	t.Run("extension filter not collected when route doesn't match", func(t *testing.T) {
		policy := makePolicy("ns", "p1", "myroute", "Lua,ExtProc")
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "other-route", []string{"envoy.filters.http.lua/0"}),
				},
			}},
		}}
		before, after := buildFilterOrderSets([]egv1a1.EnvoyExtensionPolicy{policy}, routes)
		require.Nil(t, before)
		require.Nil(t, after)
	})

	t.Run("default lua-filter-order annotation still recognized", func(t *testing.T) {
		policy := egv1a1.EnvoyExtensionPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns",
				Name:      "default-ann",
				Annotations: map[string]string{
					internalapi.DefaultFilterOrderAnnotation: internalapi.FilterOrderBeforeExtProc,
				},
			},
			Spec: egv1a1.EnvoyExtensionPolicySpec{
				PolicyTargetReferences: egv1a1.PolicyTargetReferences{
					TargetRefs: []gwapiv1.LocalPolicyTargetReferenceWithSectionName{
						{LocalPolicyTargetReference: gwapiv1.LocalPolicyTargetReference{Name: "myroute"}},
					},
				},
			},
		}
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "myroute", []string{"envoy.filters.http.lua/0"}),
				},
			}},
		}}
		before, _ := buildFilterOrderSets([]egv1a1.EnvoyExtensionPolicy{policy}, routes)
		require.Equal(t, map[string]bool{"envoy.filters.http.lua/0": true}, before)
	})

	t.Run("singular targetRef field is respected", func(t *testing.T) {
		policy := egv1a1.EnvoyExtensionPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns", Name: "singular",
				Annotations: map[string]string{internalapi.FilterOrderAnnotation: "Lua,ExtProc"},
			},
			Spec: egv1a1.EnvoyExtensionPolicySpec{
				PolicyTargetReferences: egv1a1.PolicyTargetReferences{
					TargetRef: &gwapiv1.LocalPolicyTargetReferenceWithSectionName{
						LocalPolicyTargetReference: gwapiv1.LocalPolicyTargetReference{Name: "myroute"},
					},
				},
			},
		}
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "myroute", []string{"envoy.filters.http.lua/0"}),
				},
			}},
		}}
		before, _ := buildFilterOrderSets([]egv1a1.EnvoyExtensionPolicy{policy}, routes)
		require.Equal(t, map[string]bool{"envoy.filters.http.lua/0": true}, before)
	})

	t.Run("gateway-targeted policy collects filters from all routes", func(t *testing.T) {
		policy := egv1a1.EnvoyExtensionPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns", Name: "gw-policy",
				Annotations: map[string]string{internalapi.FilterOrderAnnotation: "Lua,ExtProc"},
			},
			Spec: egv1a1.EnvoyExtensionPolicySpec{
				PolicyTargetReferences: egv1a1.PolicyTargetReferences{
					TargetRefs: []gwapiv1.LocalPolicyTargetReferenceWithSectionName{
						{LocalPolicyTargetReference: gwapiv1.LocalPolicyTargetReference{Kind: "Gateway", Name: "my-gw"}},
					},
				},
			},
		}
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "route-a", []string{"envoy.filters.http.lua/0"}),
					routeWithLuaMetadata(t, "ns", "route-b", []string{"envoy.filters.http.lua/1"}),
				},
			}},
		}}
		before, _ := buildFilterOrderSets([]egv1a1.EnvoyExtensionPolicy{policy}, routes)
		require.Equal(t, map[string]bool{
			"envoy.filters.http.lua/0": true,
			"envoy.filters.http.lua/1": true,
		}, before)
	})
}

func TestReorderFiltersRelativeToExtProc(t *testing.T) {
	f := func(name string) *httpconnectionmanagerv3.HttpFilter {
		return &httpconnectionmanagerv3.HttpFilter{Name: name}
	}
	mgr := func(names ...string) *httpconnectionmanagerv3.HttpConnectionManager {
		filters := make([]*httpconnectionmanagerv3.HttpFilter, len(names))
		for i, n := range names {
			filters[i] = f(n)
		}
		return &httpconnectionmanagerv3.HttpConnectionManager{HttpFilters: filters}
	}
	filterNames := func(m *httpconnectionmanagerv3.HttpConnectionManager) []string {
		names := make([]string, len(m.HttpFilters))
		for i, fi := range m.HttpFilters {
			names[i] = fi.Name
		}
		return names
	}

	t.Run("both sets empty — chain unchanged", func(t *testing.T) {
		m := mgr(aiGatewayExtProcName, "envoy.filters.http.lua/0", "envoy.filters.http.router")
		changed := reorderFiltersRelativeToExtProc(m, nil, nil)
		require.False(t, changed)
		require.Equal(t, []string{aiGatewayExtProcName, "envoy.filters.http.lua/0", "envoy.filters.http.router"}, filterNames(m))
	})

	t.Run("ext_proc absent — chain unchanged", func(t *testing.T) {
		m := mgr("envoy.filters.http.lua/0", "envoy.filters.http.router")
		changed := reorderFiltersRelativeToExtProc(m, map[string]bool{"envoy.filters.http.lua/0": true}, nil)
		require.False(t, changed)
	})

	t.Run("move filter to before ext_proc", func(t *testing.T) {
		m := mgr(aiGatewayExtProcName, "envoy.filters.http.lua/0", "envoy.filters.http.rbac", "envoy.filters.http.router")
		changed := reorderFiltersRelativeToExtProc(m, map[string]bool{"envoy.filters.http.lua/0": true}, nil)
		require.True(t, changed)
		require.Equal(t, []string{
			"envoy.filters.http.lua/0",
			aiGatewayExtProcName,
			"envoy.filters.http.rbac",
			"envoy.filters.http.router",
		}, filterNames(m))
	})

	t.Run("move filter to after ext_proc", func(t *testing.T) {
		m := mgr("envoy.filters.http.rbac", aiGatewayExtProcName, "envoy.filters.http.lua/0", "envoy.filters.http.router")
		changed := reorderFiltersRelativeToExtProc(m, nil, map[string]bool{"envoy.filters.http.rbac": true})
		require.True(t, changed)
		require.Equal(t, []string{
			aiGatewayExtProcName,
			"envoy.filters.http.rbac",
			"envoy.filters.http.lua/0",
			"envoy.filters.http.router",
		}, filterNames(m))
	})

	t.Run("before and after in same call", func(t *testing.T) {
		m := mgr(
			"envoy.filters.http.wasm/0",
			"envoy.filters.http.rbac",
			aiGatewayExtProcName,
			"envoy.filters.http.lua/0",
			"envoy.filters.http.ratelimit",
			"envoy.filters.http.router",
		)
		changed := reorderFiltersRelativeToExtProc(m,
			map[string]bool{"envoy.filters.http.wasm/0": true},
			map[string]bool{"envoy.filters.http.rbac": true},
		)
		require.True(t, changed)
		require.Equal(t, []string{
			"envoy.filters.http.wasm/0",
			aiGatewayExtProcName,
			"envoy.filters.http.rbac",
			"envoy.filters.http.lua/0",
			"envoy.filters.http.ratelimit",
			"envoy.filters.http.router",
		}, filterNames(m))
	})

	t.Run("already in correct position — returns false", func(t *testing.T) {
		m := mgr("envoy.filters.http.lua/0", aiGatewayExtProcName, "envoy.filters.http.rbac", "envoy.filters.http.router")
		changed := reorderFiltersRelativeToExtProc(m,
			map[string]bool{"envoy.filters.http.lua/0": true},
			map[string]bool{"envoy.filters.http.rbac": true},
		)
		require.False(t, changed)
		require.Equal(t, []string{
			"envoy.filters.http.lua/0",
			aiGatewayExtProcName,
			"envoy.filters.http.rbac",
			"envoy.filters.http.router",
		}, filterNames(m))
	})

	t.Run("multiple before filters preserve relative order", func(t *testing.T) {
		m := mgr(
			aiGatewayExtProcName,
			"envoy.filters.http.lua/0",
			"envoy.filters.http.lua/1",
			"envoy.filters.http.router",
		)
		changed := reorderFiltersRelativeToExtProc(m, map[string]bool{
			"envoy.filters.http.lua/0": true,
			"envoy.filters.http.lua/1": true,
		}, nil)
		require.True(t, changed)
		require.Equal(t, []string{
			"envoy.filters.http.lua/0",
			"envoy.filters.http.lua/1",
			aiGatewayExtProcName,
			"envoy.filters.http.router",
		}, filterNames(m))
	})

	t.Run("wasm filter moved before ext_proc", func(t *testing.T) {
		m := mgr(aiGatewayExtProcName, "envoy.filters.http.wasm/0", "envoy.filters.http.router")
		changed := reorderFiltersRelativeToExtProc(m, map[string]bool{"envoy.filters.http.wasm/0": true}, nil)
		require.True(t, changed)
		require.Equal(t, []string{"envoy.filters.http.wasm/0", aiGatewayExtProcName, "envoy.filters.http.router"}, filterNames(m))
	})

	t.Run("dynamic_modules filter moved before ext_proc", func(t *testing.T) {
		m := mgr(aiGatewayExtProcName, "envoy.filters.http.dynamic_modules/0", "envoy.filters.http.router")
		changed := reorderFiltersRelativeToExtProc(m, map[string]bool{"envoy.filters.http.dynamic_modules/0": true}, nil)
		require.True(t, changed)
		require.Equal(t, []string{"envoy.filters.http.dynamic_modules/0", aiGatewayExtProcName, "envoy.filters.http.router"}, filterNames(m))
	})
}

func TestMaybeReorderFilters(t *testing.T) {
	makeListener := func(t *testing.T, filters ...*httpconnectionmanagerv3.HttpFilter) *listenerv3.Listener {
		t.Helper()
		hcm := &httpconnectionmanagerv3.HttpConnectionManager{HttpFilters: filters}
		hcmAny := mustToAny(t, hcm)
		return &listenerv3.Listener{
			FilterChains: []*listenerv3.FilterChain{{
				Filters: []*listenerv3.Filter{{
					Name:       wellknown.HTTPConnectionManager,
					ConfigType: &listenerv3.Filter_TypedConfig{TypedConfig: hcmAny},
				}},
			}},
		}
	}

	extractFilterNames := func(t *testing.T, listener *listenerv3.Listener) []string {
		t.Helper()
		hcm, _, err := findHCM(listener.FilterChains[0])
		require.NoError(t, err)
		names := make([]string, len(hcm.HttpFilters))
		for i, f := range hcm.HttpFilters {
			names[i] = f.Name
		}
		return names
	}

	makePolicy := func(namespace, name, targetName, annotationKey, annotationVal string) *egv1a1.EnvoyExtensionPolicy {
		return &egv1a1.EnvoyExtensionPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
				Annotations: map[string]string{
					annotationKey: annotationVal,
				},
			},
			Spec: egv1a1.EnvoyExtensionPolicySpec{
				PolicyTargetReferences: egv1a1.PolicyTargetReferences{
					TargetRefs: []gwapiv1.LocalPolicyTargetReferenceWithSectionName{
						{LocalPolicyTargetReference: gwapiv1.LocalPolicyTargetReference{Name: gwapiv1.ObjectName(targetName)}},
					},
				},
			},
		}
	}

	t.Run("no annotated policies — chain unchanged", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().WithScheme(controller.Scheme).Build()
		s := &Server{log: zap.New(), k8sClient: k8sClient}

		listener := makeListener(t,
			&httpconnectionmanagerv3.HttpFilter{Name: aiGatewayExtProcName},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.lua/0"},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.router"},
		)
		require.NoError(t, s.maybeReorderFilters(context.Background(), []*listenerv3.Listener{listener}, nil))
		require.Equal(t,
			[]string{aiGatewayExtProcName, "envoy.filters.http.lua/0", "envoy.filters.http.router"},
			extractFilterNames(t, listener),
		)
	})

	t.Run("backward compat: before-extproc moves lua filter before AI GW ext_proc", func(t *testing.T) {
		policy := makePolicy("ns", "wrap", "myroute", internalapi.FilterOrderAnnotation, internalapi.FilterOrderBeforeExtProc)
		k8sClient := fake.NewClientBuilder().WithScheme(controller.Scheme).WithObjects(policy).Build()
		s := &Server{log: zap.New(), k8sClient: k8sClient}

		listener := makeListener(t,
			&httpconnectionmanagerv3.HttpFilter{Name: aiGatewayExtProcName},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.lua/0"},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.router"},
		)
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "myroute", []string{"envoy.filters.http.lua/0"}),
				},
			}},
		}}
		require.NoError(t, s.maybeReorderFilters(context.Background(), []*listenerv3.Listener{listener}, routes))
		require.Equal(t,
			[]string{"envoy.filters.http.lua/0", aiGatewayExtProcName, "envoy.filters.http.router"},
			extractFilterNames(t, listener),
		)
	})

	t.Run("default lua-filter-order annotation still moves lua filter before AI GW ext_proc", func(t *testing.T) {
		policy := makePolicy("ns", "wrap", "myroute", internalapi.DefaultFilterOrderAnnotation, internalapi.FilterOrderBeforeExtProc)
		k8sClient := fake.NewClientBuilder().WithScheme(controller.Scheme).WithObjects(policy).Build()
		s := &Server{log: zap.New(), k8sClient: k8sClient}

		listener := makeListener(t,
			&httpconnectionmanagerv3.HttpFilter{Name: aiGatewayExtProcName},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.lua/0"},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.router"},
		)
		routes := []*routev3.RouteConfiguration{{
			VirtualHosts: []*routev3.VirtualHost{{
				Routes: []*routev3.Route{
					routeWithLuaMetadata(t, "ns", "myroute", []string{"envoy.filters.http.lua/0"}),
				},
			}},
		}}
		require.NoError(t, s.maybeReorderFilters(context.Background(), []*listenerv3.Listener{listener}, routes))
		require.Equal(t,
			[]string{"envoy.filters.http.lua/0", aiGatewayExtProcName, "envoy.filters.http.router"},
			extractFilterNames(t, listener),
		)
	})

	t.Run("sequence: wasm before and rbac after ext_proc", func(t *testing.T) {
		policy := makePolicy("ns", "wrap", "myroute", internalapi.FilterOrderAnnotation, "Wasm,ExtProc,rbac")
		k8sClient := fake.NewClientBuilder().WithScheme(controller.Scheme).WithObjects(policy).Build()
		s := &Server{log: zap.New(), k8sClient: k8sClient}

		listener := makeListener(t,
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.rbac"},
			&httpconnectionmanagerv3.HttpFilter{Name: aiGatewayExtProcName},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.wasm/0"},
			&httpconnectionmanagerv3.HttpFilter{Name: "envoy.filters.http.router"},
		)
		route := routeWithLuaMetadata(t, "ns", "myroute", nil)
		route.TypedPerFilterConfig["envoy.filters.http.wasm/0"] = &anypb.Any{}
		routes := []*routev3.RouteConfiguration{{VirtualHosts: []*routev3.VirtualHost{{Routes: []*routev3.Route{route}}}}}

		require.NoError(t, s.maybeReorderFilters(context.Background(), []*listenerv3.Listener{listener}, routes))
		require.Equal(t,
			[]string{
				"envoy.filters.http.wasm/0",
				aiGatewayExtProcName,
				"envoy.filters.http.rbac",
				"envoy.filters.http.router",
			},
			extractFilterNames(t, listener),
		)
	})
}
