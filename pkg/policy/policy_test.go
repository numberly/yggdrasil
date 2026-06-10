package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/uswitch/yggdrasil/pkg/apis/yggdrasil/v1alpha1"
)

func durationPtr(d time.Duration) *time.Duration { return &d }
func strPtr(s string) *string                    { return &s }
func uint32Ptr(v uint32) *uint32                 { return &v }
func boolPtr(b bool) *bool                       { return &b }

func TestParseAnnotationsAllBehaviors(t *testing.T) {
	parsed, diagnostics := ParseAnnotations(map[string]string{
		"kubernetes.io/ingress.class":                            "bar",
		"yggdrasil.uswitch.com/timeout":                          "30s",
		"yggdrasil.uswitch.com/route-timeout":                    "10s",
		"yggdrasil.uswitch.com/per-try-timeout":                  "5s",
		"yggdrasil.uswitch.com/cluster-timeout":                  "20s",
		"yggdrasil.uswitch.com/healthcheck-path":                 "/healthz",
		"yggdrasil.uswitch.com/healthcheck-host":                 "hc.example.com",
		"yggdrasil.uswitch.com/retry-on":                         "5xx,connect-failure",
		"yggdrasil.uswitch.com/upstream-http-version":            "2",
		"yggdrasil.uswitch.com/weight":                           "7",
		"yggdrasil.uswitch.com/idle-timeout":                     "1m",
		"yggdrasil.uswitch.com/max-connection-duration":          "2m",
		"yggdrasil.uswitch.com/max-requests-per-connection":      "100",
		"yggdrasil.uswitch.com/sticky-sessions":                  "true",
		"yggdrasil.uswitch.com/sticky-session-cookie-name":       "session",
		"yggdrasil.uswitch.com/sticky-session-cookie-path":       "/",
		"yggdrasil.uswitch.com/sticky-session-cookie-ttl":        "1h",
		"yggdrasil.uswitch.com/sticky-session-change-on-failure": "false",
	})
	if len(diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diagnostics)
	}
	if parsed == nil {
		t.Fatal("expected a policy")
	}
	if *parsed.Timeout != 30*time.Second || *parsed.RouteTimeout != 10*time.Second ||
		*parsed.PerTryTimeout != 5*time.Second || *parsed.ClusterTimeout != 20*time.Second {
		t.Fatalf("unexpected timeouts: %+v", parsed)
	}
	if *parsed.HealthCheckPath != "/healthz" || *parsed.HealthCheckHost != "hc.example.com" {
		t.Fatalf("unexpected healthcheck: %+v", parsed)
	}
	if strings.Join(parsed.RetryOn, ",") != "5xx,connect-failure" {
		t.Fatalf("unexpected retryOn: %+v", parsed.RetryOn)
	}
	if *parsed.UpstreamHTTPVersion != "2" || *parsed.Weight != 7 {
		t.Fatalf("unexpected upstream config: %+v", parsed)
	}
	if *parsed.IdleTimeout != time.Minute || *parsed.MaxConnectionDuration != 2*time.Minute || *parsed.MaxRequestsPerConnection != 100 {
		t.Fatalf("unexpected connection config: %+v", parsed)
	}
	if parsed.StickySessions == nil ||
		parsed.StickySessions.CookieName != "session" ||
		parsed.StickySessions.CookiePath != "/" ||
		parsed.StickySessions.CookieTTL != time.Hour ||
		parsed.StickySessions.ChangeOnFailure {
		t.Fatalf("unexpected sticky sessions: %+v", parsed.StickySessions)
	}
}

func TestParseAnnotationsReturnsNilWithoutYggdrasilAnnotations(t *testing.T) {
	parsed, diagnostics := ParseAnnotations(map[string]string{"kubernetes.io/ingress.class": "bar"})
	if parsed != nil || len(diagnostics) != 0 {
		t.Fatalf("expected nil policy, got %+v %+v", parsed, diagnostics)
	}
}

func TestParseAnnotationsKeepsLegacyLeniency(t *testing.T) {
	parsed, diagnostics := ParseAnnotations(map[string]string{
		"yggdrasil.uswitch.com/timeout":  "not-a-duration",
		"yggdrasil.uswitch.com/retry-on": "bogus",
		"yggdrasil.uswitch.com/weight":   "nan",
	})
	if parsed == nil {
		t.Fatal("expected a policy despite invalid values")
	}
	if parsed.Timeout != nil || parsed.RetryOn != nil {
		t.Fatalf("invalid values must be skipped, got %+v", parsed)
	}
	if parsed.Weight != nil {
		t.Fatalf("unparsable weight must fall back to default weighting, got %+v", parsed.Weight)
	}
	if len(diagnostics) != 2 {
		t.Fatalf("expected diagnostics for timeout and retry-on, got %+v", diagnostics)
	}
}

func TestParseAnnotationsStickySessionsRequireAllCookieFields(t *testing.T) {
	parsed, diagnostics := ParseAnnotations(map[string]string{
		"yggdrasil.uswitch.com/sticky-sessions": "true",
	})
	if parsed == nil || parsed.StickySessions != nil {
		t.Fatalf("expected sticky sessions to be skipped, got %+v", parsed)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", diagnostics)
	}
}

func TestStripAnnotations(t *testing.T) {
	stripped := StripAnnotations(map[string]string{
		"kubernetes.io/ingress.class":   "bar",
		"yggdrasil.uswitch.com/timeout": "30s",
	})
	if len(stripped) != 1 || stripped["kubernetes.io/ingress.class"] != "bar" {
		t.Fatalf("expected only non-Yggdrasil annotations, got %+v", stripped)
	}
}

func validTargetRef() v1alpha1.TargetRef {
	return v1alpha1.TargetRef{Group: HTTPRouteGroup, Kind: "HTTPRoute", Name: "app"}
}

func TestParseSpecFullPolicy(t *testing.T) {
	parsed, err := ParseSpec(v1alpha1.YggdrasilPolicySpec{
		TargetRef: validTargetRef(),
		HealthCheck: &v1alpha1.HealthCheckSpec{
			Path: strPtr("/healthz"),
			Host: strPtr("hc.example.com"),
		},
		Timeouts: &v1alpha1.TimeoutsSpec{
			Default: strPtr("30s"),
			Route:   strPtr("10s"),
			PerTry:  strPtr("5s"),
			Cluster: strPtr("20s"),
		},
		Retry:    &v1alpha1.RetrySpec{RetryOn: []string{"5xx", "connect-failure"}},
		Upstream: &v1alpha1.UpstreamSpec{HTTPVersion: strPtr("2"), Weight: uint32Ptr(7)},
		Connection: &v1alpha1.ConnectionSpec{
			IdleTimeout:              strPtr("1m"),
			MaxConnectionDuration:    strPtr("2m"),
			MaxRequestsPerConnection: uint32Ptr(100),
		},
		StickySessions: &v1alpha1.StickySessionsSpec{
			Enabled:         true,
			CookieName:      strPtr("session"),
			CookiePath:      strPtr("/"),
			CookieTTL:       strPtr("1h"),
			ChangeOnFailure: boolPtr(false),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if *parsed.Timeout != 30*time.Second || *parsed.RouteTimeout != 10*time.Second ||
		*parsed.PerTryTimeout != 5*time.Second || *parsed.ClusterTimeout != 20*time.Second {
		t.Fatalf("unexpected timeouts: %+v", parsed)
	}
	if *parsed.HealthCheckPath != "/healthz" || *parsed.HealthCheckHost != "hc.example.com" {
		t.Fatalf("unexpected healthcheck: %+v", parsed)
	}
	if strings.Join(parsed.RetryOn, ",") != "5xx,connect-failure" {
		t.Fatalf("unexpected retryOn: %+v", parsed.RetryOn)
	}
	if *parsed.UpstreamHTTPVersion != "2" || *parsed.Weight != 7 {
		t.Fatalf("unexpected upstream config: %+v", parsed)
	}
	if *parsed.IdleTimeout != time.Minute || *parsed.MaxConnectionDuration != 2*time.Minute || *parsed.MaxRequestsPerConnection != 100 {
		t.Fatalf("unexpected connection config: %+v", parsed)
	}
	if parsed.StickySessions == nil || parsed.StickySessions.ChangeOnFailure {
		t.Fatalf("unexpected sticky sessions: %+v", parsed.StickySessions)
	}
}

func TestParseSpecOptionalSectionsCanBeOmitted(t *testing.T) {
	parsed, err := ParseSpec(v1alpha1.YggdrasilPolicySpec{TargetRef: validTargetRef()})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Signature() != "" || parsed.Weight != nil {
		t.Fatalf("expected empty policy, got %+v", parsed)
	}
}

func TestParseSpecRejectsWholeSourceOnInvalidValues(t *testing.T) {
	specs := map[string]v1alpha1.YggdrasilPolicySpec{
		"invalid duration": {
			TargetRef: validTargetRef(),
			Timeouts:  &v1alpha1.TimeoutsSpec{Route: strPtr("ten-seconds")},
		},
		"invalid retry list": {
			TargetRef: validTargetRef(),
			Retry:     &v1alpha1.RetrySpec{RetryOn: []string{"5xx", "bogus"}},
		},
		"invalid http version": {
			TargetRef: validTargetRef(),
			Upstream:  &v1alpha1.UpstreamSpec{HTTPVersion: strPtr("3")},
		},
		"invalid connection duration": {
			TargetRef:  validTargetRef(),
			Connection: &v1alpha1.ConnectionSpec{IdleTimeout: strPtr("forever")},
		},
		"sticky sessions missing cookie fields": {
			TargetRef:      validTargetRef(),
			StickySessions: &v1alpha1.StickySessionsSpec{Enabled: true},
		},
		"sticky sessions invalid ttl": {
			TargetRef: validTargetRef(),
			StickySessions: &v1alpha1.StickySessionsSpec{
				Enabled:    true,
				CookieName: strPtr("session"),
				CookiePath: strPtr("/"),
				CookieTTL:  strPtr("soon"),
			},
		},
	}
	for name, spec := range specs {
		if _, err := ParseSpec(spec); err == nil {
			t.Errorf("%s: expected whole-source rejection", name)
		}
	}
}

func TestValidateTargetRef(t *testing.T) {
	if err := ValidateTargetRef(validTargetRef()); err != nil {
		t.Fatal(err)
	}
	invalid := []v1alpha1.TargetRef{
		{Group: "networking.k8s.io", Kind: "HTTPRoute", Name: "app"},
		{Group: HTTPRouteGroup, Kind: "Gateway", Name: "app"},
		{Group: HTTPRouteGroup, Kind: "HTTPRoute", Name: ""},
	}
	for _, ref := range invalid {
		if err := ValidateTargetRef(ref); err == nil {
			t.Errorf("expected invalid targetRef %+v to be rejected", ref)
		}
	}
}

func TestSignatureEquivalenceAcrossSources(t *testing.T) {
	annotationPolicy, _ := ParseAnnotations(map[string]string{
		"yggdrasil.uswitch.com/timeout":  "30s",
		"yggdrasil.uswitch.com/retry-on": "5xx",
	})
	crdPolicy, err := ParseSpec(v1alpha1.YggdrasilPolicySpec{
		TargetRef: validTargetRef(),
		Timeouts:  &v1alpha1.TimeoutsSpec{Default: strPtr("30s")},
		Retry:     &v1alpha1.RetrySpec{RetryOn: []string{"5xx"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if annotationPolicy.Signature() != crdPolicy.Signature() {
		t.Fatalf("expected equal signatures, got %q and %q", annotationPolicy.Signature(), crdPolicy.Signature())
	}
}
