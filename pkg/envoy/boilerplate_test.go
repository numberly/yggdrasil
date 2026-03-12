package envoy

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	eal "github.com/envoyproxy/go-control-plane/envoy/extensions/access_loggers/file/v3"
	auth "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	envoy_extension_http "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/golang/protobuf/ptypes/duration"
)

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

func TestMakeClusterSNISetForNonWildcard(t *testing.T) {
	caBytes := []byte("fake-ca-cert")
	c := cluster{
		Name:        "example_com",
		VirtualHost: "example.com",
		Hosts:       []LBHost{{Host: "10.0.0.1", Weight: 1}},
		Timeout:     5 * time.Second,
	}
	addresses := makeAddresses(c.Hosts, 443)
	result := makeCluster(c, caBytes, UpstreamHealthCheck{}, -1, addresses)

	if result.TransportSocket == nil {
		t.Fatal("Expected TransportSocket to be set when caBytes provided")
	}

	tlsContext := &auth.UpstreamTlsContext{}
	if err := result.TransportSocket.GetTypedConfig().UnmarshalTo(tlsContext); err != nil {
		t.Fatalf("Failed to unmarshal UpstreamTlsContext: %s", err)
	}

	if tlsContext.Sni != "example.com" {
		t.Errorf("Expected SNI to be 'example.com', got '%s'", tlsContext.Sni)
	}
}

func TestMakeClusterSNINotSetForWildcard(t *testing.T) {
	caBytes := []byte("fake-ca-cert")
	c := cluster{
		Name:        "wildcard_example_com",
		VirtualHost: "*.example.com",
		Hosts:       []LBHost{{Host: "10.0.0.1", Weight: 1}},
		Timeout:     5 * time.Second,
	}
	addresses := makeAddresses(c.Hosts, 443)
	result := makeCluster(c, caBytes, UpstreamHealthCheck{}, -1, addresses)

	tlsContext := &auth.UpstreamTlsContext{}
	if err := result.TransportSocket.GetTypedConfig().UnmarshalTo(tlsContext); err != nil {
		t.Fatalf("Failed to unmarshal UpstreamTlsContext: %s", err)
	}

	if tlsContext.Sni != "" {
		t.Errorf("Expected SNI to be empty for wildcard host, got '%s'", tlsContext.Sni)
	}
}

func TestMakeClusterNoTLSWithoutCA(t *testing.T) {
	c := cluster{
		Name:        "example_com",
		VirtualHost: "example.com",
		Hosts:       []LBHost{{Host: "10.0.0.1", Weight: 1}},
		Timeout:     5 * time.Second,
	}
	addresses := makeAddresses(c.Hosts, 443)
	result := makeCluster(c, nil, UpstreamHealthCheck{}, -1, addresses)

	if result.TransportSocket != nil {
		t.Error("Expected TransportSocket to be nil when no caBytes provided")
	}
}

func TestMakeClusterAutoSniEnabled(t *testing.T) {
	c := cluster{
		Name:        "example_com",
		VirtualHost: "example.com",
		Hosts:       []LBHost{{Host: "10.0.0.1", Weight: 1}},
		Timeout:     5 * time.Second,
	}
	addresses := makeAddresses(c.Hosts, 443)
	result := makeCluster(c, []byte("fake-ca"), UpstreamHealthCheck{}, -1, addresses)

	httpOptionsPb, ok := result.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
	if !ok {
		t.Fatal("Expected HttpProtocolOptions in TypedExtensionProtocolOptions")
	}

	httpOptions := &envoy_extension_http.HttpProtocolOptions{}
	if err := httpOptionsPb.UnmarshalTo(httpOptions); err != nil {
		t.Fatalf("Failed to unmarshal HttpProtocolOptions: %s", err)
	}

	if httpOptions.UpstreamHttpProtocolOptions == nil {
		t.Fatal("Expected UpstreamHttpProtocolOptions to be set")
	}
	if !httpOptions.UpstreamHttpProtocolOptions.AutoSni {
		t.Error("Expected AutoSni to be true")
	}
	if !httpOptions.UpstreamHttpProtocolOptions.AutoSanValidation {
		t.Error("Expected AutoSanValidation to be true")
	}
}

func TestMakeClusterAutoSniWithHTTP2(t *testing.T) {
	c := cluster{
		Name:        "example_com",
		VirtualHost: "example.com",
		HttpVersion: "2",
		Hosts:       []LBHost{{Host: "10.0.0.1", Weight: 1}},
		Timeout:     5 * time.Second,
	}
	addresses := makeAddresses(c.Hosts, 443)
	result := makeCluster(c, []byte("fake-ca"), UpstreamHealthCheck{}, -1, addresses)

	httpOptionsPb := result.TypedExtensionProtocolOptions["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"]
	httpOptions := &envoy_extension_http.HttpProtocolOptions{}
	if err := httpOptionsPb.UnmarshalTo(httpOptions); err != nil {
		t.Fatalf("Failed to unmarshal HttpProtocolOptions: %s", err)
	}

	if httpOptions.UpstreamHttpProtocolOptions == nil {
		t.Fatal("Expected UpstreamHttpProtocolOptions to be set")
	}
	if !httpOptions.UpstreamHttpProtocolOptions.AutoSni {
		t.Error("Expected AutoSni to be true")
	}
	if !httpOptions.UpstreamHttpProtocolOptions.AutoSanValidation {
		t.Error("Expected AutoSanValidation to be true")
	}
}

func mustParseDuration(dur string) time.Duration {
	d, err := time.ParseDuration(dur)
	if err != nil {
		panic(fmt.Sprintf("Failed test setup: %s", err))
	}
	return d
}
