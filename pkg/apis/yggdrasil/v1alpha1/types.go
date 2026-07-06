package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// YggdrasilPolicy configures Yggdrasil route behavior for a same-namespace HTTPRoute.
type YggdrasilPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec YggdrasilPolicySpec `json:"spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// YggdrasilPolicyList is a list of YggdrasilPolicy resources.
type YggdrasilPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []YggdrasilPolicy `json:"items"`
}

// YggdrasilPolicySpec describes the desired route policy and its target.
type YggdrasilPolicySpec struct {
	TargetRef      TargetRef           `json:"targetRef"`
	HealthCheck    *HealthCheckSpec    `json:"healthCheck,omitempty"`
	Timeouts       *TimeoutsSpec       `json:"timeouts,omitempty"`
	Retry          *RetrySpec          `json:"retry,omitempty"`
	Upstream       *UpstreamSpec       `json:"upstream,omitempty"`
	Connection     *ConnectionSpec     `json:"connection,omitempty"`
	StickySessions *StickySessionsSpec `json:"stickySessions,omitempty"`
}

// TargetRef identifies the same-namespace HTTPRoute this policy applies to.
type TargetRef struct {
	Group string `json:"group"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
}

// HealthCheckSpec configures upstream health checking.
type HealthCheckSpec struct {
	Path *string `json:"path,omitempty"`
	// Host overrides the health check host, used with wildcard route hosts.
	Host *string `json:"host,omitempty"`
}

// TimeoutsSpec configures route and cluster timeouts as Go duration strings.
type TimeoutsSpec struct {
	// Default sets route, per-try and cluster timeouts at once.
	Default *string `json:"default,omitempty"`
	Route   *string `json:"route,omitempty"`
	PerTry  *string `json:"perTry,omitempty"`
	Cluster *string `json:"cluster,omitempty"`
}

// RetrySpec configures retry behavior.
type RetrySpec struct {
	// RetryOn is a list of Envoy retry-on conditions, e.g. "5xx", "connect-failure".
	RetryOn []string `json:"retryOn,omitempty"`
}

// UpstreamSpec configures upstream selection behavior.
type UpstreamSpec struct {
	// HTTPVersion forces the upstream protocol version ("1.1" or "2").
	HTTPVersion *string `json:"httpVersion,omitempty"`
	// Weight sets the load balancing weight of this route's upstreams.
	Weight *uint32 `json:"weight,omitempty"`
}

// ConnectionSpec configures upstream connection limits as Go duration strings.
type ConnectionSpec struct {
	IdleTimeout              *string `json:"idleTimeout,omitempty"`
	MaxConnectionDuration    *string `json:"maxConnectionDuration,omitempty"`
	MaxRequestsPerConnection *uint32 `json:"maxRequestsPerConnection,omitempty"`
}

// StickySessionsSpec configures cookie-based session affinity.
type StickySessionsSpec struct {
	Enabled    bool    `json:"enabled"`
	CookieName *string `json:"cookieName,omitempty"`
	CookiePath *string `json:"cookiePath,omitempty"`
	// CookieTTL is a Go duration string.
	CookieTTL *string `json:"cookieTTL,omitempty"`
	// ChangeOnFailure controls whether sessions move away from unhealthy backends.
	ChangeOnFailure *bool `json:"changeOnFailure,omitempty"`
}
