package envoy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tcache "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/uswitch/yggdrasil/pkg/k8s"
	v1 "k8s.io/api/core/v1"
)

func createTempCAFile(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "ca-*.pem")
	if err != nil {
		t.Fatalf("failed to create temp CA file: %v", err)
	}
	if _, err := f.WriteString("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----\n"); err != nil {
		t.Fatalf("failed to write temp CA file: %v", err)
	}
	f.Close()
	return f.Name()
}

func assertNumberOfVirtualHosts(t *testing.T, filterChain *listener.FilterChain, expected int) {
	filter, err := filterChain.Filters[0].GetTypedConfig().UnmarshalNew()
	if err != nil {
		t.Fatal(err)
	}

	connManager, ok := filter.(*hcm.HttpConnectionManager)
	if !ok {
		t.Fatal(err)
	}

	routeSpecifier := connManager.RouteSpecifier.(*hcm.HttpConnectionManager_RouteConfig)
	virtualHosts := routeSpecifier.RouteConfig.VirtualHosts

	if len(virtualHosts) != expected {
		t.Fatalf("Num virtual hosts: %d expected %d", len(virtualHosts), expected)
	}

}

func assertServerNames(t *testing.T, filterChain *listener.FilterChain, expectedServerNames []string) {
	serverNames := filterChain.FilterChainMatch.ServerNames

	if len(serverNames) != len(expectedServerNames) {
		t.Fatalf("not the same number of server names: '%d' expected '%d'", len(serverNames), len(expectedServerNames))
	}

	for idx, expectedServerName := range expectedServerNames {
		if serverNames[idx] != expectedServerName {
			t.Errorf("server names do not match: '%v' expected '%v'", serverNames[idx], expectedServerName)
		}
	}
}

func TestGenerate(t *testing.T) {
	caFile := createTempCAFile(t)
	ingresses := []*k8s.Ingress{
		newGenericIngress("wibble", "bibble"),
	}

	configurator, err := NewKubernetesConfigurator("a", []Certificate{
		{Hosts: []string{"*"}, Cert: "b", Key: "c"},
	}, caFile, []string{"bar"}, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })
	if err != nil {
		t.Fatal(err)
	}

	snapshot, _ := configurator.Generate(ingresses, []*v1.Secret{})

	if len(snapshot.Resources[tcache.Listener].Items) != 1 {
		t.Fatalf("Num listeners: %d", len(snapshot.Resources[tcache.Listener].Items))
	}
	if len(snapshot.Resources[tcache.Cluster].Items) != 1 {
		t.Fatalf("Num clusters: %d", len(snapshot.Resources[tcache.Cluster].Items))
	}
}

func TestGenerateMultipleCerts(t *testing.T) {
	caFile := createTempCAFile(t)
	ingresses := []*k8s.Ingress{
		newGenericIngress("foo.internal.api.com", "bibble"),
		newGenericIngress("foo.internal.api.co.uk", "bibble"),
	}

	configurator, err := NewKubernetesConfigurator("a", []Certificate{
		{Hosts: []string{"*.internal.api.com"}, Cert: "com", Key: "com"},
		{Hosts: []string{"*.internal.api.co.uk"}, Cert: "couk", Key: "couk"},
	}, caFile, []string{"bar"}, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := configurator.Generate(ingresses, []*v1.Secret{})
	if err != nil {
		t.Fatalf("Error generating snapshot %v", err)
	}

	listener := snapshot.Resources[tcache.Listener].Items["listener_0"].Resource.(*listener.Listener)

	if len(listener.FilterChains) != 2 {
		t.Fatalf("Num filter chains: %d expected %d", len(listener.FilterChains), 2)
	}

	assertNumberOfVirtualHosts(t, listener.FilterChains[0], 1)
	assertNumberOfVirtualHosts(t, listener.FilterChains[1], 1)
}

func TestGenerateMultipleHosts(t *testing.T) {
	caFile := createTempCAFile(t)
	ingresses := []*k8s.Ingress{
		newGenericIngress("foo.internal.api.com", "bibble"),
		newGenericIngress("foo.internal.api.co.uk", "bibble"),
	}

	configurator, err := NewKubernetesConfigurator("a", []Certificate{
		{Hosts: []string{"*.internal.api.com", "*.internal.api.co.uk"}, Cert: "com", Key: "com"},
	}, caFile, []string{"bar"}, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := configurator.Generate(ingresses, []*v1.Secret{})
	if err != nil {
		t.Fatalf("Error generating snapshot %v", err)
	}

	listener := snapshot.Resources[tcache.Listener].Items["listener_0"].Resource.(*listener.Listener)

	if len(listener.FilterChains) != 1 {
		t.Fatalf("Num filter chains: %d expected %d", len(listener.FilterChains), 1)
	}

	// there should be two virtual hosts on the filter chain
	assertNumberOfVirtualHosts(t, listener.FilterChains[0], 2)
}

func TestGenerateNoMatchingCert(t *testing.T) {
	caFile := createTempCAFile(t)
	ingresses := []*k8s.Ingress{
		newGenericIngress("foo.internal.api.com", "bibble"),
		newGenericIngress("foo.internal.api.co.uk", "bibble"),
	}

	configurator, err := NewKubernetesConfigurator("a", []Certificate{
		{Hosts: []string{"*.internal.api.com"}, Cert: "com", Key: "com"},
	}, caFile, []string{"bar"}, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := configurator.Generate(ingresses, []*v1.Secret{})
	if err != nil {
		t.Fatalf("Error generating snapshot %v", err)
	}

	listener := snapshot.Resources[tcache.Listener].Items["listener_0"].Resource.(*listener.Listener)

	if len(listener.FilterChains) != 1 {
		t.Fatalf("Num filter chains: %d expected %d", len(listener.FilterChains), 1)
	}
}

func TestGenerateIntoTwoCerts(t *testing.T) {
	caFile := createTempCAFile(t)
	ingresses := []*k8s.Ingress{
		newGenericIngress("foo.internal.api.com", "bibble"),
	}

	configurator, err := NewKubernetesConfigurator("a", []Certificate{
		{Hosts: []string{"*.internal.api.com"}, Cert: "com", Key: "com"},
		{Hosts: []string{"*"}, Cert: "all", Key: "all"},
	}, caFile, []string{"bar"}, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := configurator.Generate(ingresses, []*v1.Secret{})
	if err != nil {
		t.Fatalf("Error generating snapshot %v", err)
	}

	listener := snapshot.Resources[tcache.Listener].Items["listener_0"].Resource.(*listener.Listener)

	if len(listener.FilterChains) != 2 {
		t.Fatalf("Num filter chains: %d expected %d", len(listener.FilterChains), 2)
	}

	assertNumberOfVirtualHosts(t, listener.FilterChains[0], 1)
	assertServerNames(t, listener.FilterChains[0], []string{"*.internal.api.com"})

	assertNumberOfVirtualHosts(t, listener.FilterChains[1], 1)
	assertServerNames(t, listener.FilterChains[1], nil)
}

func TestGenerateListeners(t *testing.T) {
	testcases := []struct {
		name        string
		certs       []Certificate
		virtualHost []*virtualHost
		serverNames []string
	}{
		{
			name:  "http",
			certs: nil,
			virtualHost: []*virtualHost{
				{Host: "foo", Timeout: 1 * time.Second, PerTryTimeout: 500 * time.Millisecond},
				{Host: "bar", Timeout: 1 * time.Second, PerTryTimeout: 500 * time.Millisecond},
			},
			serverNames: []string{"foo", "bar"},
		},
		{
			name: "https",
			certs: []Certificate{
				{
					Hosts: []string{"foo", "bar"},
					Cert:  "cert",
					Key:   "key",
				},
			},
			virtualHost: []*virtualHost{
				{Host: "foo", Timeout: 1 * time.Second, PerTryTimeout: 500 * time.Millisecond},
				{Host: "bar", Timeout: 1 * time.Second, PerTryTimeout: 500 * time.Millisecond},
			},
			serverNames: []string{"foo", "bar"},
		},
		{
			name: "more-certs-than-hosts",
			certs: []Certificate{
				{
					Hosts: []string{"foo", "bar"},
					Cert:  "cert",
					Key:   "key",
				}, {
					Hosts: []string{"baz"},
					Cert:  "cert",
					Key:   "key",
				},
			},
			virtualHost: []*virtualHost{
				{Host: "foo", Timeout: 1 * time.Second, PerTryTimeout: 500 * time.Millisecond},
				{Host: "bar", Timeout: 1 * time.Second, PerTryTimeout: 500 * time.Millisecond},
			},
			serverNames: []string{"foo", "bar"},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			configurator, err := NewKubernetesConfigurator("a", tc.certs, "", nil, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })
			if err != nil {
				t.Fatal(err)
			}
			ret, err := configurator.generateListeners(&envoyConfiguration{VirtualHosts: tc.virtualHost})
			if err != nil {
				t.Fatalf("Error generating listeners %v", err)
			}
			listener := ret[0].(*listener.Listener)
			if len(listener.FilterChains) != 1 {
				t.Fatalf("filterchain number missmatch")
			}
			assertNumberOfVirtualHosts(t, listener.FilterChains[0], 2)
			if len(tc.certs) > 0 {
				if listener.FilterChains[0].FilterChainMatch == nil {
					t.Fatalf("Expected filter chain")
				}
			}
		})
	}
}

func TestReadCABytes(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T) string
		wantErr   string
		checkData func(t *testing.T, data []byte)
	}{
		{
			name: "directory with pem and crt files",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				os.WriteFile(filepath.Join(dir, "ca1.pem"), []byte("PEM1"), 0644)
				os.WriteFile(filepath.Join(dir, "ca2.crt"), []byte("CRT2"), 0644)
				os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("skip me"), 0644)
				os.MkdirAll(filepath.Join(dir, "subdir"), 0755)
				return dir
			},
			checkData: func(t *testing.T, data []byte) {
				s := string(data)
				if !strings.Contains(s, "PEM1") || !strings.Contains(s, "CRT2") {
					t.Errorf("expected concatenated cert contents, got %q", s)
				}
				if strings.Contains(s, "skip me") {
					t.Error("non-cert file content should not be included")
				}
			},
		},
		{
			name: "invalid path",
			setup: func(t *testing.T) string {
				return "/nonexistent/path/to/ca"
			},
			wantErr: "failed to read CA path",
		},
		{
			name: "empty directory",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a cert"), 0644)
				return dir
			},
			wantErr: "no .pem or .crt files found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup(t)
			data, err := readCABytes(path)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkData != nil {
				tt.checkData(t, data)
			}
		})
	}
}
