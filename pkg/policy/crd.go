package policy

import (
	"fmt"
	"strings"
	"time"

	"github.com/uswitch/yggdrasil/pkg/apis/yggdrasil/v1alpha1"
)

// HTTPRouteGroup is the only targetRef group accepted by YggdrasilPolicy.
const HTTPRouteGroup = "gateway.networking.k8s.io"

// ValidateTargetRef checks that a YggdrasilPolicy targets an HTTPRoute.
func ValidateTargetRef(ref v1alpha1.TargetRef) error {
	if ref.Group != HTTPRouteGroup {
		return fmt.Errorf("unsupported targetRef.group %q: only %q is supported", ref.Group, HTTPRouteGroup)
	}
	if ref.Kind != "HTTPRoute" {
		return fmt.Errorf("unsupported targetRef.kind %q: only HTTPRoute is supported", ref.Kind)
	}
	if ref.Name == "" {
		return fmt.Errorf("targetRef.name must not be empty")
	}
	return nil
}

// ParseSpec parses a YggdrasilPolicy spec into a RoutePolicy. Unlike
// annotations, a malformed CRD spec rejects the whole policy source so
// partial policy application never happens.
func ParseSpec(spec v1alpha1.YggdrasilPolicySpec) (*RoutePolicy, error) {
	if err := ValidateTargetRef(spec.TargetRef); err != nil {
		return nil, err
	}

	p := &RoutePolicy{}

	parseDuration := func(field string, value *string, target **time.Duration) error {
		if value == nil {
			return nil
		}
		d, err := time.ParseDuration(*value)
		if err != nil {
			return fmt.Errorf("invalid %s: %s", field, err)
		}
		*target = &d
		return nil
	}

	if spec.Timeouts != nil {
		if err := parseDuration("timeouts.default", spec.Timeouts.Default, &p.Timeout); err != nil {
			return nil, err
		}
		if err := parseDuration("timeouts.route", spec.Timeouts.Route, &p.RouteTimeout); err != nil {
			return nil, err
		}
		if err := parseDuration("timeouts.perTry", spec.Timeouts.PerTry, &p.PerTryTimeout); err != nil {
			return nil, err
		}
		if err := parseDuration("timeouts.cluster", spec.Timeouts.Cluster, &p.ClusterTimeout); err != nil {
			return nil, err
		}
	}

	if spec.HealthCheck != nil {
		p.HealthCheckPath = spec.HealthCheck.Path
		p.HealthCheckHost = spec.HealthCheck.Host
	}

	if spec.Retry != nil && len(spec.Retry.RetryOn) > 0 {
		if !ValidRetryOn(spec.Retry.RetryOn) {
			return nil, fmt.Errorf("invalid retry.retryOn: %s", strings.Join(spec.Retry.RetryOn, ","))
		}
		p.RetryOn = append([]string{}, spec.Retry.RetryOn...)
	}

	if spec.Upstream != nil {
		if spec.Upstream.HTTPVersion != nil {
			version := *spec.Upstream.HTTPVersion
			if version != "1.1" && version != "2" {
				return nil, fmt.Errorf("invalid upstream.httpVersion %q: must be \"1.1\" or \"2\"", version)
			}
			p.UpstreamHTTPVersion = &version
		}
		p.Weight = spec.Upstream.Weight
	}

	if spec.Connection != nil {
		if err := parseDuration("connection.idleTimeout", spec.Connection.IdleTimeout, &p.IdleTimeout); err != nil {
			return nil, err
		}
		if err := parseDuration("connection.maxConnectionDuration", spec.Connection.MaxConnectionDuration, &p.MaxConnectionDuration); err != nil {
			return nil, err
		}
		p.MaxRequestsPerConnection = spec.Connection.MaxRequestsPerConnection
	}

	if spec.StickySessions != nil && spec.StickySessions.Enabled {
		if spec.StickySessions.CookieName == nil || *spec.StickySessions.CookieName == "" ||
			spec.StickySessions.CookiePath == nil || *spec.StickySessions.CookiePath == "" ||
			spec.StickySessions.CookieTTL == nil || *spec.StickySessions.CookieTTL == "" {
			return nil, fmt.Errorf("stickySessions enabled but cookieName, cookiePath and cookieTTL are required")
		}
		cookieTTL, err := time.ParseDuration(*spec.StickySessions.CookieTTL)
		if err != nil {
			return nil, fmt.Errorf("invalid stickySessions.cookieTTL: %s", err)
		}
		changeOnFailure := true
		if spec.StickySessions.ChangeOnFailure != nil {
			changeOnFailure = *spec.StickySessions.ChangeOnFailure
		}
		p.StickySessions = &StickySessions{
			CookieName:      *spec.StickySessions.CookieName,
			CookiePath:      *spec.StickySessions.CookiePath,
			CookieTTL:       cookieTTL,
			ChangeOnFailure: changeOnFailure,
		}
	}

	return p, nil
}
