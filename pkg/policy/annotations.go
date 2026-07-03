package policy

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const annotationPrefix = "yggdrasil.uswitch.com/"

// ParseAnnotations parses legacy Yggdrasil annotations into a RoutePolicy.
// It preserves historical annotation behavior: invalid individual values are
// skipped with a diagnostic instead of rejecting the whole source. Returns
// nil when no Yggdrasil annotation is present.
func ParseAnnotations(annotations map[string]string) (*RoutePolicy, []string) {
	p := &RoutePolicy{}
	diagnostics := []string{}
	found := false

	parseDuration := func(key string, target **time.Duration) {
		value := annotations[annotationPrefix+key]
		if value == "" {
			return
		}
		found = true
		d, err := time.ParseDuration(value)
		if err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("invalid %s: %s", key, err))
			return
		}
		*target = &d
	}

	parseDuration("timeout", &p.Timeout)
	parseDuration("route-timeout", &p.RouteTimeout)
	parseDuration("per-try-timeout", &p.PerTryTimeout)
	parseDuration("cluster-timeout", &p.ClusterTimeout)
	parseDuration("idle-timeout", &p.IdleTimeout)
	parseDuration("max-connection-duration", &p.MaxConnectionDuration)

	if value := annotations[annotationPrefix+"healthcheck-path"]; value != "" {
		found = true
		p.HealthCheckPath = &value
	}
	if value := annotations[annotationPrefix+"healthcheck-host"]; value != "" {
		found = true
		p.HealthCheckHost = &value
	}

	if value := annotations[annotationPrefix+"retry-on"]; value != "" {
		found = true
		conditions := strings.Split(value, ",")
		if ValidRetryOn(conditions) {
			p.RetryOn = conditions
		} else {
			diagnostics = append(diagnostics, fmt.Sprintf("invalid retry-on parameter: %s", value))
		}
	}

	if value := annotations[annotationPrefix+"upstream-http-version"]; value != "" {
		found = true
		p.UpstreamHTTPVersion = &value
	}

	if value := annotations[annotationPrefix+"max-requests-per-connection"]; value != "" {
		found = true
		if parsed, err := strconv.ParseUint(value, 10, 32); err == nil {
			v := uint32(parsed)
			p.MaxRequestsPerConnection = &v
		} else {
			diagnostics = append(diagnostics, fmt.Sprintf("invalid max-requests-per-connection: %s", err))
		}
	}

	if value := annotations[annotationPrefix+"weight"]; value != "" {
		found = true
		if parsed, err := strconv.ParseUint(value, 10, 32); err == nil {
			v := uint32(parsed)
			p.Weight = &v
		}
		// historical behavior: an unparsable weight falls back to weight 1
	}

	if annotations[annotationPrefix+"sticky-sessions"] != "" {
		found = true
	}
	if annotations[annotationPrefix+"sticky-sessions"] == "true" {
		cookieName := annotations[annotationPrefix+"sticky-session-cookie-name"]
		cookiePath := annotations[annotationPrefix+"sticky-session-cookie-path"]
		cookieTTLStr := annotations[annotationPrefix+"sticky-session-cookie-ttl"]
		if cookieName == "" || cookiePath == "" || cookieTTLStr == "" {
			diagnostics = append(diagnostics, "sticky-sessions enabled but missing required annotations (cookie-name, cookie-path, cookie-ttl), skipping sticky sessions")
		} else if cookieTTL, err := time.ParseDuration(cookieTTLStr); err != nil {
			diagnostics = append(diagnostics, fmt.Sprintf("invalid sticky-session-cookie-ttl: %s", err))
		} else {
			p.StickySessions = &StickySessions{
				CookieName:      cookieName,
				CookiePath:      cookiePath,
				CookieTTL:       cookieTTL,
				ChangeOnFailure: annotations[annotationPrefix+"sticky-session-change-on-failure"] != "false",
			}
		}
	}

	if !found {
		return nil, diagnostics
	}
	return p, diagnostics
}

// StripAnnotations returns a copy of the annotation map without Yggdrasil
// policy annotations, so raw policy never travels on the shared route model.
func StripAnnotations(annotations map[string]string) map[string]string {
	if annotations == nil {
		return nil
	}
	out := map[string]string{}
	for key, value := range annotations {
		if strings.HasPrefix(key, annotationPrefix) {
			continue
		}
		out[key] = value
	}
	return out
}
