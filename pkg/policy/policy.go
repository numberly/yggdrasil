// Package policy defines the shared RoutePolicy model. Annotations and
// YggdrasilPolicy CRDs are Policy Sources parsed into this single model;
// Envoy generation consumes RoutePolicy only.
package policy

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// RoutePolicy is the source-agnostic Yggdrasil route behavior attached to a
// SourceRoute. Pointer fields distinguish unset values from explicit zero or
// false values.
type RoutePolicy struct {
	// Timeout sets the route, per-try and cluster timeouts at once.
	Timeout        *time.Duration
	RouteTimeout   *time.Duration
	PerTryTimeout  *time.Duration
	ClusterTimeout *time.Duration

	HealthCheckPath *string
	HealthCheckHost *string

	RetryOn []string

	UpstreamHTTPVersion *string
	// Weight is upstream weighting; it is excluded from conflict signatures.
	Weight *uint32

	IdleTimeout              *time.Duration
	MaxConnectionDuration    *time.Duration
	MaxRequestsPerConnection *uint32

	StickySessions *StickySessions
}

// StickySessions configures cookie-based session affinity.
type StickySessions struct {
	CookieName      string
	CookiePath      string
	CookieTTL       time.Duration
	ChangeOnFailure bool
}

// Source records which Policy Source produced a RoutePolicy.
type Source struct {
	Kind      string // "Annotations" or "YggdrasilPolicy"
	Namespace string
	Name      string
}

// IsZero reports whether no policy field is set.
func (p *RoutePolicy) IsZero() bool {
	return p == nil || p.Signature() == "" && p.Weight == nil
}

// Signature returns a deterministic representation of the policy used to
// detect conflicts between routes sharing a host. Weight is excluded because
// it is upstream weighting, not a host policy.
func (p *RoutePolicy) Signature() string {
	if p == nil {
		return ""
	}
	parts := []string{}
	addDuration := func(key string, d *time.Duration) {
		if d != nil {
			parts = append(parts, key+"="+d.String())
		}
	}
	addString := func(key string, s *string) {
		if s != nil {
			parts = append(parts, key+"="+*s)
		}
	}
	addDuration("timeout", p.Timeout)
	addDuration("route-timeout", p.RouteTimeout)
	addDuration("per-try-timeout", p.PerTryTimeout)
	addDuration("cluster-timeout", p.ClusterTimeout)
	addString("healthcheck-path", p.HealthCheckPath)
	addString("healthcheck-host", p.HealthCheckHost)
	if len(p.RetryOn) > 0 {
		retryOn := append([]string{}, p.RetryOn...)
		sort.Strings(retryOn)
		parts = append(parts, "retry-on="+strings.Join(retryOn, ","))
	}
	addString("upstream-http-version", p.UpstreamHTTPVersion)
	addDuration("idle-timeout", p.IdleTimeout)
	addDuration("max-connection-duration", p.MaxConnectionDuration)
	if p.MaxRequestsPerConnection != nil {
		parts = append(parts, fmt.Sprintf("max-requests-per-connection=%d", *p.MaxRequestsPerConnection))
	}
	if p.StickySessions != nil {
		parts = append(parts, fmt.Sprintf("sticky-sessions=%s|%s|%s|%t",
			p.StickySessions.CookieName, p.StickySessions.CookiePath, p.StickySessions.CookieTTL, p.StickySessions.ChangeOnFailure))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

// allowedRetryOns mirrors the Envoy x-envoy-retry-on conditions Yggdrasil accepts.
var allowedRetryOns = map[string]bool{
	"5xx":                        true,
	"gateway-error":              true,
	"reset":                      true,
	"connect-failure":            true,
	"envoy-ratelimited":          true,
	"retriable-4xx":              true,
	"refused-stream":             true,
	"retriable-status-codes":     true,
	"retriable-headers":          true,
	"http3-post-connect-failure": true,
}

// ValidRetryOn reports whether every condition in the list is a valid Envoy
// retry-on condition.
func ValidRetryOn(conditions []string) bool {
	if len(conditions) == 0 {
		return false
	}
	for _, condition := range conditions {
		if !allowedRetryOns[condition] {
			return false
		}
	}
	return true
}
