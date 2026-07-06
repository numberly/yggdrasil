package k8s

import (
	"testing"

	"github.com/uswitch/yggdrasil/pkg/policy"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestConvertGatewayResources(t *testing.T) {
	listenerHostname := gatewayv1.Hostname("*.example.com")
	sectionName := gatewayv1.SectionName("web")
	routeHostname := gatewayv1.Hostname("app.example.com")

	result, err := ConvertGatewayResources(GatewayStores{
		GatewayClasses: testStore(&gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "public"}}),
		Gateways: testStore(&gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "gateway-system"},
			Spec: gatewayv1.GatewaySpec{
				GatewayClassName: gatewayv1.ObjectName("public"),
				Listeners: []gatewayv1.Listener{
					{
						Name:     sectionName,
						Hostname: &listenerHostname,
						Port:     gatewayv1.PortNumber(443),
						Protocol: gatewayv1.HTTPSProtocolType,
						AllowedRoutes: &gatewayv1.AllowedRoutes{
							Namespaces: &gatewayv1.RouteNamespaces{From: fromNamespacesPtr(gatewayv1.NamespacesFromAll)},
						},
						TLS: &gatewayv1.ListenerTLSConfig{
							CertificateRefs: []gatewayv1.SecretObjectReference{{Name: gatewayv1.ObjectName("edge-cert")}},
						},
					},
				},
			},
		}),
		HTTPRoutes: testStore(&gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "app",
				Namespace:   "apps",
				Annotations: map[string]string{"yggdrasil.uswitch.com/timeout": "2s"},
			},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{{
						Name:        gatewayv1.ObjectName("edge"),
						Namespace:   namespacePtr("gateway-system"),
						SectionName: &sectionName,
					}},
				},
				Hostnames: []gatewayv1.Hostname{routeHostname, gatewayv1.Hostname("other.test")},
			},
		}),
		Services: testStore(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "envoy", Namespace: "gateway-system"},
			Spec: corev1.ServiceSpec{
				ExternalIPs: []string{"10.0.0.1"},
				Ports:       []corev1.ServicePort{{Name: "http", Port: 80}, {Name: "https", Port: 443}},
			},
			Status: corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{{IP: "10.0.0.2"}, {Hostname: "lb.example.net"}},
				},
			},
		}),
		Maintenance: true,
		ClusterName: "cluster-a",
		ClassConfigs: []GatewayClassConfig{{
			Name:             "public",
			ServiceNamespace: "gateway-system",
			ServiceName:      "envoy",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 {
		t.Fatalf("expected one Gateway-derived route, got %d", len(result.Routes))
	}

	route := result.Routes[0]
	if route.Source.Kind != "HTTPRoute" || route.Source.Namespace != "apps" || route.Source.Name != "app" || route.Source.Class != "public" || route.Source.KubernetesClusterName != "cluster-a" {
		t.Fatalf("unexpected route source: %+v", route.Source)
	}
	if !route.Maintenance {
		t.Fatal("expected maintenance mode to propagate")
	}
	if !testEq(route.RulesHosts, []string{"app.example.com"}) {
		t.Fatalf("unexpected hosts: %+v", route.RulesHosts)
	}
	expectedUpstreams := []UpstreamEndpoint{
		{Host: "10.0.0.1", Port: 443},
		{Host: "10.0.0.2", Port: 443},
		{Host: "lb.example.net", Port: 443},
	}
	if len(route.UpstreamEndpoints) != len(expectedUpstreams) {
		t.Fatalf("unexpected upstreams: %+v", route.UpstreamEndpoints)
	}
	for i := range expectedUpstreams {
		if route.UpstreamEndpoints[i] != expectedUpstreams[i] {
			t.Fatalf("unexpected upstream %d: %+v", i, route.UpstreamEndpoints[i])
		}
	}
	if route.TLS["app.example.com"].SecretName != "edge-cert" {
		t.Fatalf("unexpected TLS map: %+v", route.TLS)
	}
	if route.TLS["app.example.com"].SecretNamespace != "gateway-system" {
		t.Fatalf("expected Gateway namespace for TLS secret, got %+v", route.TLS["app.example.com"])
	}
}

func TestHostMatchesGatewayListenerWildcardMatchesOneLabel(t *testing.T) {
	tests := []struct {
		name         string
		routeHost    string
		listenerHost string
		want         bool
	}{
		{name: "single label", routeHost: "app.example.com", listenerHost: "*.example.com", want: true},
		{name: "multiple labels", routeHost: "a.b.example.com", listenerHost: "*.example.com", want: false},
		{name: "bare suffix", routeHost: "example.com", listenerHost: "*.example.com", want: false},
		{name: "exact", routeHost: "app.example.com", listenerHost: "app.example.com", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostMatchesGatewayListener(tt.routeHost, tt.listenerHost); got != tt.want {
				t.Fatalf("hostMatchesGatewayListener(%q, %q) = %t, want %t", tt.routeHost, tt.listenerHost, got, tt.want)
			}
		})
	}
}

func TestConvertGatewayResourcesHonorsAllowedRoutesNamespaces(t *testing.T) {
	hostname := gatewayv1.Hostname("app.example.com")
	selectorFrom := gatewayv1.NamespacesFromSelector
	allFrom := gatewayv1.NamespacesFromAll
	sameFrom := gatewayv1.NamespacesFromSame

	tests := []struct {
		name          string
		routeNS       string
		allowedRoutes *gatewayv1.AllowedRoutes
		namespaces    cache.Store
		wantRoutes    int
	}{
		{
			name:       "default rejects cross namespace",
			routeNS:    "apps",
			namespaces: testStore(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "apps"}}),
		},
		{
			name:    "same rejects cross namespace",
			routeNS: "apps",
			allowedRoutes: &gatewayv1.AllowedRoutes{
				Namespaces: &gatewayv1.RouteNamespaces{From: &sameFrom},
			},
			namespaces: testStore(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "apps"}}),
		},
		{
			name:    "same accepts gateway namespace",
			routeNS: "gateway-system",
			allowedRoutes: &gatewayv1.AllowedRoutes{
				Namespaces: &gatewayv1.RouteNamespaces{From: &sameFrom},
			},
			namespaces: testStore(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "gateway-system"}}),
			wantRoutes: 1,
		},
		{
			name:    "all accepts cross namespace",
			routeNS: "apps",
			allowedRoutes: &gatewayv1.AllowedRoutes{
				Namespaces: &gatewayv1.RouteNamespaces{From: &allFrom},
			},
			namespaces: testStore(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "apps"}}),
			wantRoutes: 1,
		},
		{
			name:    "selector accepts matching namespace",
			routeNS: "apps",
			allowedRoutes: &gatewayv1.AllowedRoutes{
				Namespaces: &gatewayv1.RouteNamespaces{
					From:     &selectorFrom,
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"gateway-access": "public"}},
				},
			},
			namespaces: testStore(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "apps", Labels: map[string]string{"gateway-access": "public"}}}),
			wantRoutes: 1,
		},
		{
			name:    "selector rejects non matching namespace",
			routeNS: "apps",
			allowedRoutes: &gatewayv1.AllowedRoutes{
				Namespaces: &gatewayv1.RouteNamespaces{
					From:     &selectorFrom,
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"gateway-access": "public"}},
				},
			},
			namespaces: testStore(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "apps", Labels: map[string]string{"gateway-access": "private"}}}),
		},
		{
			name:    "selector rejects missing namespace",
			routeNS: "apps",
			allowedRoutes: &gatewayv1.AllowedRoutes{
				Namespaces: &gatewayv1.RouteNamespaces{
					From:     &selectorFrom,
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"gateway-access": "public"}},
				},
			},
			namespaces: testStore(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ConvertGatewayResources(GatewayStores{
				GatewayClasses: testStore(&gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "public"}}),
				Gateways: testStore(&gatewayv1.Gateway{
					ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "gateway-system"},
					Spec: gatewayv1.GatewaySpec{
						GatewayClassName: gatewayv1.ObjectName("public"),
						Listeners: []gatewayv1.Listener{{
							Name:          gatewayv1.SectionName("web"),
							Hostname:      &hostname,
							Port:          gatewayv1.PortNumber(80),
							Protocol:      gatewayv1.HTTPProtocolType,
							AllowedRoutes: tt.allowedRoutes,
						}},
					},
				}),
				HTTPRoutes: testStore(&gatewayv1.HTTPRoute{
					ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: tt.routeNS},
					Spec: gatewayv1.HTTPRouteSpec{
						CommonRouteSpec: gatewayv1.CommonRouteSpec{
							ParentRefs: []gatewayv1.ParentReference{{
								Name:      gatewayv1.ObjectName("edge"),
								Namespace: namespacePtr("gateway-system"),
							}},
						},
						Hostnames: []gatewayv1.Hostname{hostname},
					},
				}),
				Services: testStore(&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "envoy", Namespace: "gateway-system"},
					Spec: corev1.ServiceSpec{
						ExternalIPs: []string{"10.0.0.1"},
						Ports:       []corev1.ServicePort{{Port: 80}},
					},
				}),
				Namespaces: tt.namespaces,
				ClassConfigs: []GatewayClassConfig{{
					Name:             "public",
					ServiceNamespace: "gateway-system",
					ServiceName:      "envoy",
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Routes) != tt.wantRoutes {
				t.Fatalf("expected %d routes, got %d: %+v", tt.wantRoutes, len(result.Routes), result.Routes)
			}
		})
	}
}

func TestGatewayListenerTLSHonorsCertificateRefNamespace(t *testing.T) {
	hostname := "app.example.com"
	gateway := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "gateway-system"}}
	listener := gatewayv1.Listener{
		TLS: &gatewayv1.ListenerTLSConfig{
			CertificateRefs: []gatewayv1.SecretObjectReference{{
				Name:      gatewayv1.ObjectName("edge-cert"),
				Namespace: namespacePtr("cert-system"),
			}},
		},
	}

	tls := gatewayListenerTLS(gateway, listener, []string{hostname})

	if tls[hostname].SecretName != "edge-cert" {
		t.Fatalf("expected SecretName edge-cert, got %+v", tls[hostname])
	}
	if tls[hostname].SecretNamespace != "cert-system" {
		t.Fatalf("expected SecretNamespace cert-system, got %+v", tls[hostname])
	}
}

func TestConvertGatewayResourcesDropsSameKindPolicyConflicts(t *testing.T) {
	hostname := gatewayv1.Hostname("app.example.com")

	result, err := ConvertGatewayResources(GatewayStores{
		GatewayClasses: testStore(&gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "public"}}),
		Gateways: testStore(&gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "apps"},
			Spec: gatewayv1.GatewaySpec{
				GatewayClassName: gatewayv1.ObjectName("public"),
				Listeners: []gatewayv1.Listener{{
					Name:     gatewayv1.SectionName("web"),
					Hostname: &hostname,
					Port:     gatewayv1.PortNumber(80),
					Protocol: gatewayv1.HTTPProtocolType,
				}},
			},
		}),
		HTTPRoutes: testStore(
			gatewayHTTPRoute("one", "1s", hostname),
			gatewayHTTPRoute("two", "2s", hostname),
		),
		Services: testStore(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "envoy", Namespace: "apps"},
			Spec: corev1.ServiceSpec{
				ExternalIPs: []string{"10.0.0.1"},
				Ports:       []corev1.ServicePort{{Port: 80}},
			},
		}),
		ClassConfigs: []GatewayClassConfig{{
			Name:             "public",
			ServiceNamespace: "apps",
			ServiceName:      "envoy",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 0 {
		t.Fatalf("expected conflicted host to be dropped, got %+v", result.Routes)
	}
	if len(result.Diagnostics) == 0 {
		t.Fatal("expected a diagnostic for same-kind policy conflict")
	}
}

func TestConvertGatewayResourcesDropsOnlyConflictedHosts(t *testing.T) {
	aHost := gatewayv1.Hostname("a.example.com")
	bHost := gatewayv1.Hostname("b.example.com")

	multiHostRoute := gatewayHTTPRoute("one", "1s", aHost)
	multiHostRoute.Spec.Hostnames = []gatewayv1.Hostname{aHost, bHost}

	result, err := ConvertGatewayResources(GatewayStores{
		GatewayClasses: testStore(&gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "public"}}),
		Gateways: testStore(&gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "apps"},
			Spec: gatewayv1.GatewaySpec{
				GatewayClassName: gatewayv1.ObjectName("public"),
				Listeners: []gatewayv1.Listener{{
					Name:     gatewayv1.SectionName("web"),
					Port:     gatewayv1.PortNumber(80),
					Protocol: gatewayv1.HTTPProtocolType,
				}},
			},
		}),
		HTTPRoutes: testStore(
			multiHostRoute,
			gatewayHTTPRoute("two", "2s", aHost),
		),
		Services: testStore(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "envoy", Namespace: "apps"},
			Spec: corev1.ServiceSpec{
				ExternalIPs: []string{"10.0.0.1"},
				Ports:       []corev1.ServicePort{{Port: 80}},
			},
		}),
		ClassConfigs: []GatewayClassConfig{{
			Name:             "public",
			ServiceNamespace: "apps",
			ServiceName:      "envoy",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 {
		t.Fatalf("expected only the non-conflicted host route to remain, got %+v", result.Routes)
	}
	if result.Routes[0].Name != "one" || !testEq(result.Routes[0].RulesHosts, []string{"b.example.com"}) {
		t.Fatalf("expected route one to keep only b.example.com, got %+v", result.Routes[0])
	}
}

func TestConvertGatewayResourcesKeepsHostWhenSiblingHasNoPolicy(t *testing.T) {
	hostname := gatewayv1.Hostname("app.example.com")

	noPolicyRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "two", Namespace: "apps"}, // no yggdrasil annotation
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName("edge")}},
			},
			Hostnames: []gatewayv1.Hostname{hostname},
		},
	}

	result, err := ConvertGatewayResources(GatewayStores{
		GatewayClasses: testStore(&gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "public"}}),
		Gateways: testStore(&gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "apps"},
			Spec: gatewayv1.GatewaySpec{
				GatewayClassName: gatewayv1.ObjectName("public"),
				Listeners: []gatewayv1.Listener{{
					Name:     gatewayv1.SectionName("web"),
					Hostname: &hostname,
					Port:     gatewayv1.PortNumber(80),
					Protocol: gatewayv1.HTTPProtocolType,
				}},
			},
		}),
		HTTPRoutes: testStore(
			gatewayHTTPRoute("one", "1s", hostname), // has a policy
			noPolicyRoute,                            // has none
		),
		Services: testStore(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "envoy", Namespace: "apps"},
			Spec: corev1.ServiceSpec{
				ExternalIPs: []string{"10.0.0.1"},
				Ports:       []corev1.ServicePort{{Port: 80}},
			},
		}),
		ClassConfigs: []GatewayClassConfig{{
			Name: "public", ServiceNamespace: "apps", ServiceName: "envoy",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Both routes keep the shared host; the policy-less sibling must not trigger a conflict.
	if len(result.Routes) != 2 {
		t.Fatalf("expected both routes retained, got %+v", result.Routes)
	}
	for _, r := range result.Routes {
		if !testEq(r.RulesHosts, []string{"app.example.com"}) {
			t.Fatalf("route %s lost its host: %+v", r.Name, r.RulesHosts)
		}
	}
}

func TestConvertGatewayResourcesReportsAdditionalCertificateRefs(t *testing.T) {
	hostname := gatewayv1.Hostname("app.example.com")

	result, err := ConvertGatewayResources(GatewayStores{
		GatewayClasses: testStore(&gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "public"}}),
		Gateways: testStore(&gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "gateway-system"},
			Spec: gatewayv1.GatewaySpec{
				GatewayClassName: gatewayv1.ObjectName("public"),
				Listeners: []gatewayv1.Listener{{
					Name:     gatewayv1.SectionName("web"),
					Hostname: &hostname,
					Port:     gatewayv1.PortNumber(443),
					Protocol: gatewayv1.HTTPSProtocolType,
					AllowedRoutes: &gatewayv1.AllowedRoutes{
						Namespaces: &gatewayv1.RouteNamespaces{From: fromNamespacesPtr(gatewayv1.NamespacesFromAll)},
					},
					TLS: &gatewayv1.ListenerTLSConfig{
						CertificateRefs: []gatewayv1.SecretObjectReference{
							{Name: gatewayv1.ObjectName("first-cert")},
							{Name: gatewayv1.ObjectName("second-cert")},
						},
					},
				}},
			},
		}),
		HTTPRoutes: testStore(&gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "apps"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{{
						Name:      gatewayv1.ObjectName("edge"),
						Namespace: namespacePtr("gateway-system"),
					}},
				},
				Hostnames: []gatewayv1.Hostname{hostname},
			},
		}),
		Services: testStore(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "envoy", Namespace: "gateway-system"},
			Spec: corev1.ServiceSpec{
				ExternalIPs: []string{"10.0.0.1"},
				Ports:       []corev1.ServicePort{{Port: 443}},
			},
		}),
		ReferenceGrants: testStore(),
		ClassConfigs:    []GatewayClassConfig{{Name: "public", ServiceNamespace: "gateway-system", ServiceName: "envoy"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(result.Routes))
	}
	if result.Routes[0].TLS["app.example.com"].SecretName != "first-cert" {
		t.Fatalf("expected first certificate ref to be used, got %+v", result.Routes[0].TLS)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("expected certificateRef diagnostic, got %+v", result.Diagnostics)
	}
	if result.Diagnostics[0].Reason != "multiple certificateRefs configured; only the first certificateRef is used" {
		t.Fatalf("unexpected diagnostic: %+v", result.Diagnostics[0])
	}
}

func TestPolicySignatureIgnoresWeight(t *testing.T) {
	firstPolicy, _ := policy.ParseAnnotations(map[string]string{
		"yggdrasil.uswitch.com/timeout": "2s",
		"yggdrasil.uswitch.com/weight":  "2",
	})
	secondPolicy, _ := policy.ParseAnnotations(map[string]string{
		"yggdrasil.uswitch.com/timeout": "2s",
		"yggdrasil.uswitch.com/weight":  "3",
	})

	if firstPolicy.Signature() != secondPolicy.Signature() {
		t.Fatalf("expected weight-only differences to be ignored, got %q and %q", firstPolicy.Signature(), secondPolicy.Signature())
	}
}

func gatewayHTTPRoute(name, timeout string, hostname gatewayv1.Hostname) *gatewayv1.HTTPRoute {
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "apps",
			Annotations: map[string]string{"yggdrasil.uswitch.com/timeout": timeout},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName("edge")}},
			},
			Hostnames: []gatewayv1.Hostname{hostname},
		},
	}
}

func TestConvertGatewayResourcesEnforcesReferenceGrant(t *testing.T) {
	hostname := gatewayv1.Hostname("app.example.com")

	gateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "gateway-system"},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName("public"),
			Listeners: []gatewayv1.Listener{{
				Name:     gatewayv1.SectionName("web"),
				Hostname: &hostname,
				Port:     gatewayv1.PortNumber(443),
				Protocol: gatewayv1.HTTPSProtocolType,
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Namespaces: &gatewayv1.RouteNamespaces{From: fromNamespacesPtr(gatewayv1.NamespacesFromAll)},
				},
				TLS: &gatewayv1.ListenerTLSConfig{
					CertificateRefs: []gatewayv1.SecretObjectReference{{
						Name:      gatewayv1.ObjectName("edge-cert"),
						Namespace: namespacePtr("cert-system"),
					}},
				},
			}},
		},
	}

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "apps"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{
					Name:      gatewayv1.ObjectName("edge"),
					Namespace: namespacePtr("gateway-system"),
				}},
			},
			Hostnames: []gatewayv1.Hostname{hostname},
		},
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "envoy", Namespace: "gateway-system"},
		Spec: corev1.ServiceSpec{
			ExternalIPs: []string{"10.0.0.1"},
			Ports:       []corev1.ServicePort{{Port: 443}},
		},
	}
	classConfigs := []GatewayClassConfig{{Name: "public", ServiceNamespace: "gateway-system", ServiceName: "envoy"}}
	gatewayClasses := testStore(&gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "public"}})

	secretName := gatewayv1.ObjectName("edge-cert")
	wrongName := gatewayv1.ObjectName("other-cert")

	tests := []struct {
		name       string
		grants     cache.Store
		wantRoutes int
		wantDeny   bool
	}{
		{
			name:     "no grant denies",
			grants:   testStore(),
			wantDeny: true,
		},
		{
			name: "matching grant any-name permits",
			grants: testStore(referenceGrant("allow", "cert-system",
				[]gatewayv1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "Gateway", Namespace: "gateway-system"}},
				[]gatewayv1.ReferenceGrantTo{{Group: "", Kind: "Secret"}})),
			wantRoutes: 1,
		},
		{
			name: "matching grant exact name permits",
			grants: testStore(referenceGrant("allow", "cert-system",
				[]gatewayv1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "Gateway", Namespace: "gateway-system"}},
				[]gatewayv1.ReferenceGrantTo{{Group: "", Kind: "Secret", Name: &secretName}})),
			wantRoutes: 1,
		},
		{
			name: "grant naming different secret denies",
			grants: testStore(referenceGrant("allow", "cert-system",
				[]gatewayv1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "Gateway", Namespace: "gateway-system"}},
				[]gatewayv1.ReferenceGrantTo{{Group: "", Kind: "Secret", Name: &wrongName}})),
			wantDeny: true,
		},
		{
			name: "grant from wrong namespace denies",
			grants: testStore(referenceGrant("allow", "cert-system",
				[]gatewayv1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "Gateway", Namespace: "other-system"}},
				[]gatewayv1.ReferenceGrantTo{{Group: "", Kind: "Secret"}})),
			wantDeny: true,
		},
		{
			name: "grant from wrong kind denies",
			grants: testStore(referenceGrant("allow", "cert-system",
				[]gatewayv1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "HTTPRoute", Namespace: "gateway-system"}},
				[]gatewayv1.ReferenceGrantTo{{Group: "", Kind: "Secret"}})),
			wantDeny: true,
		},
		{
			name: "grant in wrong namespace denies",
			grants: testStore(referenceGrant("allow", "gateway-system",
				[]gatewayv1.ReferenceGrantFrom{{Group: gatewayv1.GroupName, Kind: "Gateway", Namespace: "gateway-system"}},
				[]gatewayv1.ReferenceGrantTo{{Group: "", Kind: "Secret"}})),
			wantDeny: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ConvertGatewayResources(GatewayStores{
				GatewayClasses:  gatewayClasses,
				Gateways:        testStore(gateway),
				HTTPRoutes:      testStore(route),
				ReferenceGrants: tt.grants,
				Services:        testStore(service),
				ClassConfigs:    classConfigs,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Routes) != tt.wantRoutes {
				t.Fatalf("expected %d routes, got %d", tt.wantRoutes, len(result.Routes))
			}
			if tt.wantDeny && len(result.Diagnostics) == 0 {
				t.Fatal("expected a diagnostic for denied cross-namespace ref")
			}
			if !tt.wantDeny && len(result.Diagnostics) != 0 {
				t.Fatalf("expected no diagnostics, got %+v", result.Diagnostics)
			}
		})
	}
}

func TestConvertGatewayResourcesAllowsSameNamespaceSecretRef(t *testing.T) {
	hostname := gatewayv1.Hostname("app.example.com")

	result, err := ConvertGatewayResources(GatewayStores{
		GatewayClasses: testStore(&gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "public"}}),
		Gateways: testStore(&gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "gateway-system"},
			Spec: gatewayv1.GatewaySpec{
				GatewayClassName: gatewayv1.ObjectName("public"),
				Listeners: []gatewayv1.Listener{{
					Name:     gatewayv1.SectionName("web"),
					Hostname: &hostname,
					Port:     gatewayv1.PortNumber(443),
					Protocol: gatewayv1.HTTPSProtocolType,
					AllowedRoutes: &gatewayv1.AllowedRoutes{
						Namespaces: &gatewayv1.RouteNamespaces{From: fromNamespacesPtr(gatewayv1.NamespacesFromAll)},
					},
					TLS: &gatewayv1.ListenerTLSConfig{
						CertificateRefs: []gatewayv1.SecretObjectReference{{
							Name:      gatewayv1.ObjectName("edge-cert"),
							Namespace: namespacePtr("gateway-system"),
						}},
					},
				}},
			},
		}),
		HTTPRoutes: testStore(&gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "apps"},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{{
						Name:      gatewayv1.ObjectName("edge"),
						Namespace: namespacePtr("gateway-system"),
					}},
				},
				Hostnames: []gatewayv1.Hostname{hostname},
			},
		}),
		Services: testStore(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "envoy", Namespace: "gateway-system"},
			Spec: corev1.ServiceSpec{
				ExternalIPs: []string{"10.0.0.1"},
				Ports:       []corev1.ServicePort{{Port: 443}},
			},
		}),
		// Empty ReferenceGrants store — same-namespace refs must not require a grant.
		ReferenceGrants: testStore(),
		ClassConfigs:    []GatewayClassConfig{{Name: "public", ServiceNamespace: "gateway-system", ServiceName: "envoy"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(result.Routes))
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", result.Diagnostics)
	}
}

func TestListenerSecretRefDenyReasonRejectsNonSecretRefs(t *testing.T) {
	group := gatewayv1.Group("example.com")
	kind := gatewayv1.Kind("ConfigMap")
	gateway := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "gateway-system"}}
	listener := gatewayv1.Listener{
		TLS: &gatewayv1.ListenerTLSConfig{
			CertificateRefs: []gatewayv1.SecretObjectReference{{
				Group: &group,
				Kind:  &kind,
				Name:  gatewayv1.ObjectName("edge-cert"),
			}},
		},
	}

	if denyReason := listenerSecretRefDenyReason(gateway, listener, testStore()); denyReason == "" {
		t.Fatal("expected non-Secret certificateRef to be denied")
	}
}

func referenceGrant(name, namespace string, from []gatewayv1.ReferenceGrantFrom, to []gatewayv1.ReferenceGrantTo) *gatewayv1.ReferenceGrant {
	return &gatewayv1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       gatewayv1.ReferenceGrantSpec{From: from, To: to},
	}
}

func testStore(objects ...interface{}) cache.Store {
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	for _, object := range objects {
		_ = store.Add(object)
	}
	return store
}

func namespacePtr(namespace string) *gatewayv1.Namespace {
	value := gatewayv1.Namespace(namespace)
	return &value
}

func fromNamespacesPtr(from gatewayv1.FromNamespaces) *gatewayv1.FromNamespaces {
	return &from
}

func TestResolveGatewayUpstreamsPrecedence(t *testing.T) {
	listener := gatewayv1.Listener{Name: gatewayv1.SectionName("https"), Port: gatewayv1.PortNumber(443), Protocol: gatewayv1.HTTPSProtocolType}
	classConfig := GatewayClassConfig{Name: "public", ServiceNamespace: "gateway-system", ServiceName: "envoy"}
	configuredService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "envoy", Namespace: "gateway-system"},
		Spec:       corev1.ServiceSpec{ExternalIPs: []string{"10.0.0.3"}, Ports: []corev1.ServicePort{{Port: 443}}},
	}
	ownedService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "edge-owned",
			Namespace:       "gateway-system",
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "gateway.networking.k8s.io/v1", Kind: "Gateway", Name: "edge"}},
		},
		Spec: corev1.ServiceSpec{ExternalIPs: []string{"10.0.0.2"}, Ports: []corev1.ServicePort{{Port: 443}}},
	}
	gatewayWithStatus := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "gateway-system"},
		Status: gatewayv1.GatewayStatus{
			Addresses: []gatewayv1.GatewayStatusAddress{{Value: "10.0.0.1"}, {Value: "lb.example.net"}},
		},
	}
	gatewayWithoutStatus := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "gateway-system"}}

	services := map[string]*corev1.Service{
		"gateway-system/envoy":      configuredService,
		"gateway-system/edge-owned": ownedService,
	}

	got := resolveGatewayUpstreams(gatewayWithStatus, listener, classConfig, services)
	want := []UpstreamEndpoint{{Host: "10.0.0.1", Port: 443}, {Host: "lb.example.net", Port: 443}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("expected status addresses to win, got %+v", got)
	}

	got = resolveGatewayUpstreams(gatewayWithoutStatus, listener, classConfig, services)
	if len(got) != 1 || got[0] != (UpstreamEndpoint{Host: "10.0.0.2", Port: 443}) {
		t.Fatalf("expected owned Service fallback, got %+v", got)
	}

	got = resolveGatewayUpstreams(gatewayWithoutStatus, listener, classConfig, map[string]*corev1.Service{"gateway-system/envoy": configuredService})
	if len(got) != 1 || got[0] != (UpstreamEndpoint{Host: "10.0.0.3", Port: 443}) {
		t.Fatalf("expected configured Service fallback, got %+v", got)
	}

	got = resolveGatewayUpstreams(gatewayWithoutStatus, listener, GatewayClassConfig{Name: "public"}, map[string]*corev1.Service{})
	if len(got) != 0 {
		t.Fatalf("expected no upstreams without any discovery source, got %+v", got)
	}
}

func TestOwnedServiceUpstreamsIgnoresForeignOwners(t *testing.T) {
	listener := gatewayv1.Listener{Port: gatewayv1.PortNumber(443)}
	gateway := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "gateway-system"}}
	services := map[string]*corev1.Service{
		"gateway-system/other": {
			ObjectMeta: metav1.ObjectMeta{
				Name:            "other",
				Namespace:       "gateway-system",
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "Deployment", Name: "edge"}},
			},
			Spec: corev1.ServiceSpec{ExternalIPs: []string{"10.0.0.9"}, Ports: []corev1.ServicePort{{Port: 443}}},
		},
		"apps/edge-owned": {
			ObjectMeta: metav1.ObjectMeta{
				Name:            "edge-owned",
				Namespace:       "apps",
				OwnerReferences: []metav1.OwnerReference{{APIVersion: "gateway.networking.k8s.io/v1", Kind: "Gateway", Name: "edge"}},
			},
			Spec: corev1.ServiceSpec{ExternalIPs: []string{"10.0.0.8"}, Ports: []corev1.ServicePort{{Port: 443}}},
		},
	}
	if got := ownedServiceUpstreams(gateway, listener, services); len(got) != 0 {
		t.Fatalf("expected no owned upstreams (wrong owner kind / wrong namespace), got %+v", got)
	}
}
