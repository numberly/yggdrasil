package envoy

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	eal "github.com/envoyproxy/go-control-plane/envoy/extensions/access_loggers/file/v3"
	stateful_session "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/stateful_session/v3"
	cookie_session "github.com/envoyproxy/go-control-plane/envoy/extensions/http/stateful_session/cookie/v3"
	auth "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	envoy_extension_http "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/golang/protobuf/ptypes/duration"
)

func TestMakeClusterUpstreamTLSHasNoSNI(t *testing.T) {
	c := cluster{VirtualHost: "test.example.com", Hosts: []LBHost{{Host: "host1", Weight: 1}}}
	addresses := []*core.Address{
		{Address: &core.Address_SocketAddress{SocketAddress: &core.SocketAddress{Address: "host1", PortSpecifier: &core.SocketAddress_PortValue{PortValue: 443}}}},
	}
	result := makeCluster(c, "/etc/ssl/ca.pem", UpstreamHealthCheck{}, -1, addresses)

	if result.TransportSocket == nil {
		t.Fatal("expected TransportSocket to be set")
	}
	var tlsCtx auth.UpstreamTlsContext
	if err := result.TransportSocket.GetTypedConfig().UnmarshalTo(&tlsCtx); err != nil {
		t.Fatalf("failed to unmarshal UpstreamTlsContext: %s", err)
	}
	if tlsCtx.Sni != "" {
		t.Errorf("expected empty SNI, got %q", tlsCtx.Sni)
	}
}

func TestMakeHealthChecksEmptyPath(t *testing.T) {
	healthChecks := makeHealthChecks("example.com", "", UpstreamHealthCheck{})

	if len(healthChecks) != 0 {
		t.Error("Expected healthchecks to be empty")
	}
}

func TestMakeHealthChecksValidPath(t *testing.T) {
	host, path := "foo", "/bobba"
	cfg := UpstreamHealthCheck{
		Timeout:            mustParseDuration("5s"),
		Interval:           mustParseDuration("10s"),
		UnhealthyThreshold: 3,
		HealthyThreshold:   3,
	}
	healthChecks := makeHealthChecks(host, path, cfg)
	timeout := healthChecks[0].Timeout
	interval := healthChecks[0].Interval

	cfgTimeout := &duration.Duration{Seconds: int64(cfg.Timeout.Seconds())}
	cfgInterval := &duration.Duration{Seconds: int64(cfg.Interval.Seconds())}

	if len(healthChecks) != 1 {
		t.Error("Expected healthcheck to exist")
	}

	if cfgTimeout.Seconds != timeout.Seconds {
		t.Errorf("Expected timeout to be %s, but got %s", cfgTimeout, timeout)
	}

	if cfgInterval.Seconds != interval.Seconds {
		t.Errorf("Expected interval to be %s, but got %s", cfgInterval, interval)
	}

	httpCheck := healthChecks[0].HealthChecker.(*core.HealthCheck_HttpHealthCheck_)

	if httpCheck.HttpHealthCheck.Host != host {
		t.Errorf("Expect health check host to be %s, but got %s", host, httpCheck.HttpHealthCheck.Host)
	}

	if httpCheck.HttpHealthCheck.Path != path {
		t.Errorf("Expect health check path to be %s, but got %s", path, httpCheck.HttpHealthCheck.Path)
	}

}

type accessLoggerTestCase struct {
	name   string
	format map[string]interface{}
	custom bool
}

func TestAccessLoggerConfig(t *testing.T) {
	testCases := []accessLoggerTestCase{
		{name: "default log format", format: DefaultAccessLogFormat, custom: false},
		{name: "custom log format", format: map[string]interface{}{"a-key": "a-format-specifier"}, custom: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := AccessLogger{}
			if tc.custom {
				cfg.Format = tc.format
			}

			fileAccessLog := makeFileAccessLog(cfg, "/var/log/envoy/")
			if fileAccessLog.Path != "/var/log/envoy/access.log" {
				t.Errorf("Expected access log to use default path but was, %s", fileAccessLog.Path)
			}

			alf, ok := fileAccessLog.AccessLogFormat.(*eal.FileAccessLog_LogFormat)
			if !ok {
				t.Fatalf("File Access Log Format had incorrect type, should be FileAccessLog_LogFormat")
			}

			lf, ok := alf.LogFormat.Format.(*core.SubstitutionFormatString_JsonFormat)
			if !ok {
				t.Fatalf("LogFormat had incorrect type, should be SubstitutionFormatString_JsonFormat")
			}

			format := lf.JsonFormat.AsMap()
			if !reflect.DeepEqual(format, tc.format) {
				t.Errorf("Log format map should match configuration")
			}
		})
	}
}

func mustParseDuration(dur string) time.Duration {
	d, err := time.ParseDuration(dur)
	if err != nil {
		panic(fmt.Sprintf("Failed test setup: %s", err))
	}
	return d
}

func TestMakeVirtualHostWithStickySession(t *testing.T) {
	vhost := &virtualHost{
		Host:                    "app.example.com",
		UpstreamCluster:         "app_example_com",
		Timeout:                 15 * time.Second,
		PerTryTimeout:           5 * time.Second,
		StickySession:           true,
		StickySessionCookieName: "my-session",
		StickySessionCookiePath: "/",
		StickySessionCookieTTL:  3600 * time.Second,
	}

	vh, err := makeVirtualHost(vhost, -1, "5xx")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if vh.TypedPerFilterConfig == nil {
		t.Fatal("expected TypedPerFilterConfig to be set")
	}

	anyConfig, ok := vh.TypedPerFilterConfig["envoy.filters.http.stateful_session"]
	if !ok {
		t.Fatal("expected stateful_session key in TypedPerFilterConfig")
	}

	perRoute := &stateful_session.StatefulSessionPerRoute{}
	if err := anyConfig.UnmarshalTo(perRoute); err != nil {
		t.Fatalf("failed to unmarshal per-route config: %s", err)
	}

	ss := perRoute.GetStatefulSession()
	if ss == nil {
		t.Fatal("expected StatefulSession override, got nil")
	}

	if ss.SessionState.Name != "envoy.http.stateful_session.cookie" {
		t.Errorf("expected session state name 'envoy.http.stateful_session.cookie', got '%s'", ss.SessionState.Name)
	}

	cookieState := &cookie_session.CookieBasedSessionState{}
	if err := ss.SessionState.TypedConfig.UnmarshalTo(cookieState); err != nil {
		t.Fatalf("failed to unmarshal cookie config: %s", err)
	}

	if cookieState.Cookie.Name != "my-session" {
		t.Errorf("expected cookie name 'my-session', got '%s'", cookieState.Cookie.Name)
	}
	if cookieState.Cookie.Ttl.Seconds != 3600 {
		t.Errorf("expected cookie TTL 3600s, got %d", cookieState.Cookie.Ttl.Seconds)
	}
	if cookieState.Cookie.Path != "/" {
		t.Errorf("expected cookie path '/', got '%s'", cookieState.Cookie.Path)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestMakeClusterWithStickySessionOverrideHost(t *testing.T) {
	c := cluster{
		Name:                         "test_cluster",
		VirtualHost:                  "test.example.com",
		Timeout:                      5 * time.Second,
		Hosts:                        []LBHost{{Host: "host1", Weight: 1}},
		StickySessionChangeOnFailure: boolPtr(false),
	}
	addresses := []*core.Address{
		{Address: &core.Address_SocketAddress{SocketAddress: &core.SocketAddress{Address: "host1", PortSpecifier: &core.SocketAddress_PortValue{PortValue: 443}}}},
	}
	result := makeCluster(c, "", UpstreamHealthCheck{}, -1, addresses)

	if result.CommonLbConfig == nil {
		t.Fatal("expected CommonLbConfig to be set")
	}
	if result.CommonLbConfig.OverrideHostStatus == nil {
		t.Fatal("expected OverrideHostStatus to be set")
	}
	statuses := result.CommonLbConfig.OverrideHostStatus.Statuses
	if len(statuses) != 4 {
		t.Fatalf("expected 4 health statuses, got %d", len(statuses))
	}
	expected := []core.HealthStatus{core.HealthStatus_UNKNOWN, core.HealthStatus_HEALTHY, core.HealthStatus_UNHEALTHY, core.HealthStatus_DEGRADED}
	for i, s := range statuses {
		if s != expected[i] {
			t.Errorf("expected status %v at index %d, got %v", expected[i], i, s)
		}
	}
}

func TestMakeClusterWithoutStickySessionOverrideHost(t *testing.T) {
	c := cluster{
		Name:                         "test_cluster",
		VirtualHost:                  "test.example.com",
		Timeout:                      5 * time.Second,
		Hosts:                        []LBHost{{Host: "host1", Weight: 1}},
		StickySessionChangeOnFailure: boolPtr(true),
	}
	addresses := []*core.Address{
		{Address: &core.Address_SocketAddress{SocketAddress: &core.SocketAddress{Address: "host1", PortSpecifier: &core.SocketAddress_PortValue{PortValue: 443}}}},
	}
	result := makeCluster(c, "", UpstreamHealthCheck{}, -1, addresses)

	if result.CommonLbConfig != nil {
		t.Error("expected CommonLbConfig to be nil when StickySessionChangeOnFailure is true")
	}
}

func TestMakeClusterWithStickySessionNil(t *testing.T) {
	c := cluster{
		Name:        "test_cluster",
		VirtualHost: "test.example.com",
		Timeout:     5 * time.Second,
		Hosts:       []LBHost{{Host: "host1", Weight: 1}},
	}
	addresses := []*core.Address{
		{Address: &core.Address_SocketAddress{SocketAddress: &core.SocketAddress{Address: "host1", PortSpecifier: &core.SocketAddress_PortValue{PortValue: 443}}}},
	}
	result := makeCluster(c, "", UpstreamHealthCheck{}, -1, addresses)

	if result.CommonLbConfig != nil {
		t.Error("expected CommonLbConfig to be nil when StickySessionChangeOnFailure is nil (sticky sessions disabled)")
	}
}

func TestMakeVirtualHostWithoutStickySession(t *testing.T) {
	vhost := &virtualHost{
		Host:            "app.example.com",
		UpstreamCluster: "app_example_com",
		Timeout:         15 * time.Second,
		PerTryTimeout:   5 * time.Second,
	}

	vh, err := makeVirtualHost(vhost, -1, "5xx")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	if vh.TypedPerFilterConfig != nil {
		t.Error("expected TypedPerFilterConfig to be nil when sticky session is disabled")
	}
}

func TestMakeClusterDefaultHttpProtocolOptions(t *testing.T) {
	c := cluster{
		Name:        "test_cluster",
		VirtualHost: "test.example.com",
		Timeout:     5 * time.Second,
		Hosts:       []LBHost{{Host: "host1", Weight: 1}},
	}
	addresses := []*core.Address{
		{Address: &core.Address_SocketAddress{SocketAddress: &core.SocketAddress{Address: "host1", PortSpecifier: &core.SocketAddress_PortValue{PortValue: 443}}}},
	}
	result := makeCluster(c, "", UpstreamHealthCheck{}, -1, addresses)

	anyOpts := result.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
	if anyOpts == nil {
		t.Fatal("expected HttpProtocolOptions to be set")
	}

	opts := &envoy_extension_http.HttpProtocolOptions{}
	if err := anyOpts.UnmarshalTo(opts); err != nil {
		t.Fatalf("failed to unmarshal HttpProtocolOptions: %s", err)
	}

	common := opts.CommonHttpProtocolOptions
	if common == nil {
		t.Fatal("expected CommonHttpProtocolOptions to be set")
	}

	if common.IdleTimeout.Seconds != 60 {
		t.Errorf("expected default idle_timeout=60s, got %ds", common.IdleTimeout.Seconds)
	}
	if common.MaxConnectionDuration.Seconds != 0 {
		t.Errorf("expected default max_connection_duration=0s (disabled), got %ds", common.MaxConnectionDuration.Seconds)
	}
	if common.MaxRequestsPerConnection.Value != 10000 {
		t.Errorf("expected default max_requests_per_connection=10000, got %d", common.MaxRequestsPerConnection.Value)
	}
}

func TestMakeClusterCustomHttpProtocolOptions(t *testing.T) {
	idleTimeout := 30 * time.Second
	maxConnDur := 3600 * time.Second
	maxReqs := uint32(5000)

	c := cluster{
		Name:                     "test_cluster",
		VirtualHost:              "test.example.com",
		Timeout:                  5 * time.Second,
		Hosts:                    []LBHost{{Host: "host1", Weight: 1}},
		IdleTimeout:              &idleTimeout,
		MaxConnectionDuration:    &maxConnDur,
		MaxRequestsPerConnection: &maxReqs,
	}
	addresses := []*core.Address{
		{Address: &core.Address_SocketAddress{SocketAddress: &core.SocketAddress{Address: "host1", PortSpecifier: &core.SocketAddress_PortValue{PortValue: 443}}}},
	}
	result := makeCluster(c, "", UpstreamHealthCheck{}, -1, addresses)

	anyOpts := result.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
	opts := &envoy_extension_http.HttpProtocolOptions{}
	if err := anyOpts.UnmarshalTo(opts); err != nil {
		t.Fatalf("failed to unmarshal HttpProtocolOptions: %s", err)
	}

	common := opts.CommonHttpProtocolOptions

	if common.IdleTimeout.Seconds != 30 {
		t.Errorf("expected idle_timeout=30s, got %ds", common.IdleTimeout.Seconds)
	}
	if common.MaxConnectionDuration.Seconds != 3600 {
		t.Errorf("expected max_connection_duration=3600s, got %ds", common.MaxConnectionDuration.Seconds)
	}
	if common.MaxRequestsPerConnection.Value != 5000 {
		t.Errorf("expected max_requests_per_connection=5000, got %d", common.MaxRequestsPerConnection.Value)
	}
}

func TestClusterEqualsWithHttpProtocolOptions(t *testing.T) {
	idle1 := 30 * time.Second
	idle2 := 60 * time.Second
	maxReqs1 := uint32(5000)

	c1 := &cluster{Name: "test", VirtualHost: "test.com", Hosts: []LBHost{}, IdleTimeout: &idle1}
	c2 := &cluster{Name: "test", VirtualHost: "test.com", Hosts: []LBHost{}, IdleTimeout: &idle1}
	c3 := &cluster{Name: "test", VirtualHost: "test.com", Hosts: []LBHost{}, IdleTimeout: &idle2}
	c4 := &cluster{Name: "test", VirtualHost: "test.com", Hosts: []LBHost{}, MaxRequestsPerConnection: &maxReqs1}

	if !c1.Equals(c2) {
		t.Error("clusters with same IdleTimeout should be equal")
	}
	if c1.Equals(c3) {
		t.Error("clusters with different IdleTimeout should not be equal")
	}
	if c1.Equals(c4) {
		t.Error("clusters with different fields set should not be equal")
	}
}
