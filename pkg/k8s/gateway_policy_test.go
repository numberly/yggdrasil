package k8s

import (
	"strings"
	"testing"
	"time"

	"github.com/uswitch/yggdrasil/pkg/apis/yggdrasil/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func policyGatewayStores(policies cache.Store, routeAnnotations map[string]string) GatewayStores {
	hostname := gatewayv1.Hostname("app.example.com")
	return GatewayStores{
		GatewayClasses: testStore(&gatewayv1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "public"}}),
		Gateways: testStore(&gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "apps"},
			Spec: gatewayv1.GatewaySpec{
				GatewayClassName: gatewayv1.ObjectName("public"),
				Listeners: []gatewayv1.Listener{{
					Name:     gatewayv1.SectionName("web"),
					Hostname: &hostname,
					Port:     gatewayv1.PortNumber(443),
					Protocol: gatewayv1.HTTPSProtocolType,
				}},
			},
		}),
		HTTPRoutes: testStore(&gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "apps", Annotations: routeAnnotations},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName("edge")}},
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
		YggdrasilPolicies: policies,
		ClusterName:       "cluster-a",
		ClassConfigs: []GatewayClassConfig{{
			Name:             "public",
			ServiceNamespace: "gateway-system",
			ServiceName:      "envoy",
		}},
	}
}

func yggdrasilPolicy(name, namespace, targetName string) *v1alpha1.YggdrasilPolicy {
	timeout := "30s"
	return &v1alpha1.YggdrasilPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: v1alpha1.YggdrasilPolicySpec{
			TargetRef: v1alpha1.TargetRef{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: targetName},
			Timeouts:  &v1alpha1.TimeoutsSpec{Default: &timeout},
		},
	}
}

func TestGatewayConversionAttachesYggdrasilPolicy(t *testing.T) {
	result, err := ConvertGatewayResources(policyGatewayStores(
		testStore(yggdrasilPolicy("app-policy", "apps", "app")),
		map[string]string{"yggdrasil.uswitch.com/timeout": "5s"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Routes) != 1 {
		t.Fatalf("expected one route, got %d", len(result.Routes))
	}
	route := result.Routes[0]
	if route.Policy == nil || route.Policy.Timeout == nil || *route.Policy.Timeout != 30*time.Second {
		t.Fatalf("expected CRD policy to take ownership over annotations, got %+v", route.Policy)
	}
	if route.PolicySource == nil || route.PolicySource.Kind != "YggdrasilPolicy" || route.PolicySource.Name != "app-policy" {
		t.Fatalf("unexpected policy source: %+v", route.PolicySource)
	}
	for key := range route.Annotations {
		if strings.HasPrefix(key, "yggdrasil.uswitch.com/") {
			t.Fatalf("raw Yggdrasil annotations must not stay on the shared route model: %s", key)
		}
	}
}

func TestGatewayConversionAnnotationFallbackWithoutPolicy(t *testing.T) {
	result, err := ConvertGatewayResources(policyGatewayStores(
		testStore(),
		map[string]string{"yggdrasil.uswitch.com/timeout": "5s"},
	))
	if err != nil {
		t.Fatal(err)
	}
	route := result.Routes[0]
	if route.Policy == nil || route.Policy.Timeout == nil || *route.Policy.Timeout != 5*time.Second {
		t.Fatalf("expected annotation fallback policy, got %+v", route.Policy)
	}
	if route.PolicySource == nil || route.PolicySource.Kind != "Annotations" {
		t.Fatalf("unexpected policy source: %+v", route.PolicySource)
	}
}

func TestGatewayConversionIgnoresDuplicatePolicies(t *testing.T) {
	result, err := ConvertGatewayResources(policyGatewayStores(
		testStore(yggdrasilPolicy("a-policy", "apps", "app"), yggdrasilPolicy("b-policy", "apps", "app")),
		map[string]string{"yggdrasil.uswitch.com/timeout": "5s"},
	))
	if err != nil {
		t.Fatal(err)
	}
	route := result.Routes[0]
	if route.Policy != nil {
		t.Fatalf("duplicate policies must be ignored without annotation fallback, got %+v", route.Policy)
	}
	if len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Reason, "duplicate YggdrasilPolicies") {
		t.Fatalf("expected duplicate policy diagnostic, got %+v", result.Diagnostics)
	}
}

func TestGatewayConversionRejectsInvalidPolicyWithoutFallback(t *testing.T) {
	invalid := yggdrasilPolicy("app-policy", "apps", "app")
	badTimeout := "not-a-duration"
	invalid.Spec.Timeouts = &v1alpha1.TimeoutsSpec{Default: &badTimeout}

	result, err := ConvertGatewayResources(policyGatewayStores(
		testStore(invalid),
		map[string]string{"yggdrasil.uswitch.com/timeout": "5s"},
	))
	if err != nil {
		t.Fatal(err)
	}
	route := result.Routes[0]
	if route.Policy != nil {
		t.Fatalf("invalid CRD must not be masked by annotations, got %+v", route.Policy)
	}
	if len(result.Diagnostics) != 1 || !strings.Contains(result.Diagnostics[0].Reason, "invalid YggdrasilPolicy") {
		t.Fatalf("expected invalid policy diagnostic, got %+v", result.Diagnostics)
	}
	if result.Diagnostics[0].Source.Kind != "YggdrasilPolicy" {
		t.Fatalf("expected diagnostic to blame the policy, got %+v", result.Diagnostics[0].Source)
	}
}

func TestGatewayConversionReportsInvalidTargetRef(t *testing.T) {
	invalid := yggdrasilPolicy("app-policy", "apps", "app")
	invalid.Spec.TargetRef.Kind = "Gateway"

	result, err := ConvertGatewayResources(policyGatewayStores(
		testStore(invalid),
		map[string]string{"yggdrasil.uswitch.com/timeout": "5s"},
	))
	if err != nil {
		t.Fatal(err)
	}
	route := result.Routes[0]
	if route.Policy == nil || route.Policy.Timeout == nil || *route.Policy.Timeout != 5*time.Second {
		t.Fatalf("policy with invalid targetRef targets nothing; expected annotation fallback, got %+v", route.Policy)
	}
	foundDiagnostic := false
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Source.Kind == "YggdrasilPolicy" && strings.Contains(diagnostic.Reason, "targetRef") {
			foundDiagnostic = true
		}
	}
	if !foundDiagnostic {
		t.Fatalf("expected invalid targetRef diagnostic, got %+v", result.Diagnostics)
	}
}
