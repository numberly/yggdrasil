package envoy

import (
	"testing"
	"time"

	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	auth "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	tcache "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/uswitch/yggdrasil/pkg/k8s"
	v1 "k8s.io/api/core/v1"
)

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
	ingresses := []*k8s.Ingress{
		newGenericIngress("wibble", "bibble"),
	}

	configurator := NewKubernetesConfigurator("a", []Certificate{
		{Hosts: []string{"*"}, Cert: "b", Key: "c"},
	}, "d", []string{"bar"}, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })

	snapshot, _ := configurator.Generate(ingresses, []*v1.Secret{})

	if len(snapshot.Resources[tcache.Listener].Items) != 1 {
		t.Fatalf("Num listeners: %d", len(snapshot.Resources[tcache.Listener].Items))
	}
	if len(snapshot.Resources[tcache.Cluster].Items) != 1 {
		t.Fatalf("Num clusters: %d", len(snapshot.Resources[tcache.Cluster].Items))
	}
}

func TestGenerateMultipleCerts(t *testing.T) {
	ingresses := []*k8s.Ingress{
		newGenericIngress("foo.internal.api.com", "bibble"),
		newGenericIngress("foo.internal.api.co.uk", "bibble"),
	}

	configurator := NewKubernetesConfigurator("a", []Certificate{
		{Hosts: []string{"*.internal.api.com"}, Cert: "com", Key: "com"},
		{Hosts: []string{"*.internal.api.co.uk"}, Cert: "couk", Key: "couk"},
	}, "d", []string{"bar"}, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })

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
	ingresses := []*k8s.Ingress{
		newGenericIngress("foo.internal.api.com", "bibble"),
		newGenericIngress("foo.internal.api.co.uk", "bibble"),
	}

	configurator := NewKubernetesConfigurator("a", []Certificate{
		{Hosts: []string{"*.internal.api.com", "*.internal.api.co.uk"}, Cert: "com", Key: "com"},
	}, "d", []string{"bar"}, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })

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
	ingresses := []*k8s.Ingress{
		newGenericIngress("foo.internal.api.com", "bibble"),
		newGenericIngress("foo.internal.api.co.uk", "bibble"),
	}

	configurator := NewKubernetesConfigurator("a", []Certificate{
		{Hosts: []string{"*.internal.api.com"}, Cert: "com", Key: "com"},
	}, "d", []string{"bar"}, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })

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
	ingresses := []*k8s.Ingress{
		newGenericIngress("foo.internal.api.com", "bibble"),
	}

	configurator := NewKubernetesConfigurator("a", []Certificate{
		{Hosts: []string{"*.internal.api.com"}, Cert: "com", Key: "com"},
		{Hosts: []string{"*"}, Cert: "all", Key: "all"},
	}, "d", []string{"bar"}, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })

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

func TestValidateMTLSIngressesRejectsUnsafeConfiguration(t *testing.T) {
	mtlsIngress := newGenericIngress("mtls.example.com", "backend")
	mtlsIngress.Namespace = "default"
	mtlsIngress.Annotations["yggdrasil.uswitch.com/auth-tls-verify-client"] = "true"
	mtlsIngress.Annotations["yggdrasil.uswitch.com/auth-tls-secret"] = "mtls-secret"
	secret := &v1.Secret{Data: map[string][]byte{"ca.crt": []byte("ca"), "tls.crt": []byte("cert"), "tls.key": []byte("key")}}
	secret.Namespace = "default"
	secret.Name = "mtls-secret"

	if err := validateMTLSIngresses([]*k8s.Ingress{mtlsIngress}, []*v1.Secret{secret}, false); err == nil {
		t.Fatal("expected mTLS without secret synchronization to fail")
	}
	if err := validateMTLSIngresses([]*k8s.Ingress{mtlsIngress}, nil, true); err == nil {
		t.Fatal("expected mTLS without its secret to fail")
	}

	publicIngress := newGenericIngress("mtls.example.com", "other-backend")
	if err := validateMTLSIngresses([]*k8s.Ingress{mtlsIngress, publicIngress}, []*v1.Secret{secret}, true); err == nil {
		t.Fatal("expected conflicting mTLS policies for one host to fail")
	}
}

func TestStaticTLSRequiresClientCertificate(t *testing.T) {
	configurator := NewKubernetesConfigurator("a", []Certificate{{Hosts: []string{"*"}, Cert: "cert", Key: "key"}}, "", nil, "/var/log/envoy/", func(c *KubernetesConfigurator) {
		c.envoyListenerIpv4Address = []string{"1.1.1.1"}
	})

	resources, err := configurator.generateTLSFilterChains(&envoyConfiguration{VirtualHosts: []*virtualHost{
		{Host: "mtls.example.com", UpstreamCluster: "mtls", Timeout: time.Second, PerTryTimeout: time.Second, TrustedCa: "client-ca"},
		{Host: "public.example.com", UpstreamCluster: "public", Timeout: time.Second, PerTryTimeout: time.Second},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected two filter chains, got %d", len(resources))
	}

	foundMTLS := false
	foundDefault := false
	for _, filterChain := range resources {
		if len(filterChain.FilterChainMatch.ServerNames) == 1 && filterChain.FilterChainMatch.ServerNames[0] == "mtls.example.com" {
			config, err := filterChain.TransportSocket.GetTypedConfig().UnmarshalNew()
			if err != nil {
				t.Fatal(err)
			}
			tls, ok := config.(*auth.DownstreamTlsContext)
			if !ok || !tls.GetRequireClientCertificate().GetValue() || tls.GetCommonTlsContext().GetValidationContext().GetTrustedCa().GetInlineString() != "client-ca" {
				t.Fatal("mTLS filter chain must require a client certificate")
			}
			assertNumberOfVirtualHosts(t, filterChain, 1)
			foundMTLS = true
			continue
		}
		if len(filterChain.FilterChainMatch.ServerNames) == 0 {
			assertNumberOfVirtualHosts(t, filterChain, 1)
			foundDefault = true
			continue
		}
		t.Fatalf("unexpected filter chain match: %v", filterChain.FilterChainMatch.ServerNames)
	}
	if !foundMTLS || !foundDefault {
		t.Fatal("mTLS and default filter chains must both be present")
	}
}

func TestDynamicTLSFallbackRetainsMTLS(t *testing.T) {
	configurator := NewKubernetesConfigurator("a", []Certificate{{Hosts: []string{"*"}, Cert: "cert", Key: "key"}}, "", nil, "/var/log/envoy/", WithSyncSecrets(true), func(c *KubernetesConfigurator) {
		c.envoyListenerIpv4Address = []string{"1.1.1.1"}
	})

	resources, err := configurator.generateDynamicTLSFilterChains(&envoyConfiguration{VirtualHosts: []*virtualHost{{
		Host: "app.example.com", UpstreamCluster: "app", Timeout: time.Second, PerTryTimeout: time.Second, TrustedCa: "client-ca",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected one mTLS filter chain, got %d", len(resources))
	}

	for _, filterChain := range resources {
		if len(filterChain.FilterChainMatch.ServerNames) != 1 || filterChain.FilterChainMatch.ServerNames[0] != "app.example.com" {
			continue
		}
		config, err := filterChain.TransportSocket.GetTypedConfig().UnmarshalNew()
		if err != nil {
			t.Fatal(err)
		}
		tls, ok := config.(*auth.DownstreamTlsContext)
		if !ok || !tls.GetRequireClientCertificate().GetValue() || tls.GetCommonTlsContext().GetValidationContext().GetTrustedCa().GetInlineString() != "client-ca" {
			t.Fatal("default certificate fallback must retain client certificate validation")
		}
		return
	}
	t.Fatal("expected a host-specific fallback mTLS filter chain")
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
			configurator := NewKubernetesConfigurator("a", tc.certs, "", nil, "/var/log/envoy/", func(c *KubernetesConfigurator) { c.envoyListenerIpv4Address = []string{"1.1.1.1"} })
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
