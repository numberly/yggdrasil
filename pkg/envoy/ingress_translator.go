package envoy

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/uswitch/yggdrasil/pkg/k8s"
	"github.com/uswitch/yggdrasil/pkg/policy"
	v1 "k8s.io/api/core/v1"
)

type UpstreamInfo struct {
	RuleHost    string
	Upstream    string
	Namespace   string
	Class       string
	ClusterName string
	SourceKind  string
	IngressName string
}

var previousUpstreams = make(map[string]UpstreamInfo)

func sortCluster(clusters []*cluster) {
	sort.Slice(clusters, func(i int, j int) bool {
		return clusters[i].identity() < clusters[j].identity()
	})
}

func ClustersEquals(a, b []*cluster) bool {
	if len(a) != len(b) {
		return false
	}

	sortCluster(a)
	sortCluster(b)

	for idx, cluster := range a {
		if !cluster.Equals(b[idx]) {
			return false
		}
	}

	return true
}

func sortVirtualHosts(hosts []*virtualHost) {
	sort.Slice(hosts, func(i int, j int) bool {
		return hosts[i].Host < hosts[j].Host
	})
}

func VirtualHostsEquals(a, b []*virtualHost) bool {
	if len(a) != len(b) {
		return false
	}

	sortVirtualHosts(a)
	sortVirtualHosts(b)

	for idx, hosts := range a {
		if !hosts.Equals(b[idx]) {
			return false
		}
	}

	return true
}

type envoyConfiguration struct {
	VirtualHosts []*virtualHost
	Clusters     []*cluster
	AccessLog    string
}

type virtualHost struct {
	Host            string
	UpstreamCluster string
	Timeout         time.Duration
	PerTryTimeout   time.Duration
	TlsKey          string
	TlsCert         string
	RetryOn         string

	StickySession           bool
	StickySessionCookieName string
	StickySessionCookiePath string
	StickySessionCookieTTL  time.Duration
}

func (v *virtualHost) Equals(other *virtualHost) bool {
	if other == nil {
		return false
	}

	return v.Host == other.Host &&
		v.Timeout == other.Timeout &&
		v.UpstreamCluster == other.UpstreamCluster &&
		v.PerTryTimeout == other.PerTryTimeout &&
		v.TlsKey == other.TlsKey &&
		v.TlsCert == other.TlsCert &&
		v.RetryOn == other.RetryOn &&
		v.StickySession == other.StickySession &&
		v.StickySessionCookieName == other.StickySessionCookieName &&
		v.StickySessionCookiePath == other.StickySessionCookiePath &&
		v.StickySessionCookieTTL == other.StickySessionCookieTTL
}

type LBHost struct {
	Host   string
	Weight uint32
	Port   uint32
}

type cluster struct {
	Name                         string
	VirtualHost                  string
	HealthCheckPath              string
	HealthCheckHost              string // with Wildcard, the HealthCheck host can be different than the VirtualHost
	HttpVersion                  string
	Timeout                      time.Duration
	Hosts                        []LBHost
	StickySessionChangeOnFailure *bool // nil = not set (sticky sessions disabled), false = persist to unhealthy backend
	IdleTimeout                  *time.Duration
	MaxConnectionDuration        *time.Duration
	MaxRequestsPerConnection     *uint32
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func durationPtrEqual(a, b *time.Duration) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func uint32PtrEqual(a, b *uint32) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func (c *cluster) identity() string {
	return c.Name
}

func (c *cluster) Equals(other *cluster) bool {
	if other == nil {
		return false
	}

	if c.Name != other.Name {
		return false
	}

	if c.Timeout != other.Timeout {
		return false
	}

	if c.VirtualHost != other.VirtualHost {
		return false
	}

	if c.HealthCheckHost != other.HealthCheckHost {
		return false
	}

	if c.HealthCheckPath != other.HealthCheckPath {
		return false
	}

	if len(c.Hosts) != len(other.Hosts) {
		return false
	}

	if c.HttpVersion != other.HttpVersion {
		return false
	}

	if !boolPtrEqual(c.StickySessionChangeOnFailure, other.StickySessionChangeOnFailure) {
		return false
	}

	if !durationPtrEqual(c.IdleTimeout, other.IdleTimeout) {
		return false
	}

	if !durationPtrEqual(c.MaxConnectionDuration, other.MaxConnectionDuration) {
		return false
	}

	if !uint32PtrEqual(c.MaxRequestsPerConnection, other.MaxRequestsPerConnection) {
		return false
	}

	sort.Slice(c.Hosts[:], func(i, j int) bool {
		if c.Hosts[i].Host == c.Hosts[j].Host {
			return c.Hosts[i].Port < c.Hosts[j].Port
		}
		return c.Hosts[i].Host < c.Hosts[j].Host
	})
	sort.Slice(other.Hosts[:], func(i, j int) bool {
		if other.Hosts[i].Host == other.Hosts[j].Host {
			return other.Hosts[i].Port < other.Hosts[j].Port
		}
		return other.Hosts[i].Host < other.Hosts[j].Host
	})

	for i, host := range c.Hosts {
		if host != other.Hosts[i] {
			return false
		}
	}

	return true
}

func (cfg *envoyConfiguration) equals(oldCfg *envoyConfiguration) (vmatch bool, cmatch bool) {
	if oldCfg == nil {
		return false, false
	}
	return VirtualHostsEquals(cfg.VirtualHosts, oldCfg.VirtualHosts), ClustersEquals(cfg.Clusters, oldCfg.Clusters)
}

func classFilter(ingresses []*k8s.SourceRoute, ingressClass []string) (is []*k8s.SourceRoute) {
	for _, i := range ingresses {
		if i.Source.Kind == "HTTPRoute" {
			is = append(is, i)
			continue
		}
		for _, class := range ingressClass {
			if i.Annotations["kubernetes.io/ingress.class"] == class ||
				(i.Class != nil && *i.Class == class) {
				is = append(is, i)
			}
		}
	}
	matchingIngresses.Set(float64(len(is)))
	return is
}

func validIngressFilter(ingresses []*k8s.SourceRoute) (vi []*k8s.SourceRoute) {
Ingress:
	for _, i := range ingresses {
		for _, u := range i.Upstreams {
			if u != "" {
				for _, h := range i.RulesHosts {
					if h != "" {
						vi = append(vi, i)
						continue Ingress
					}
				}
				logrus.Debugf("no host found in ingress config for: %+v in namespace: %+v", i.Name, i.Namespace)
				continue Ingress
			}
		}
		logrus.Debugf("no hostname or ip for loadbalancer found in ingress config for: %+v in namespace: %+v", i.Name, i.Namespace)
	}

	return vi
}

type envoyIngress struct {
	vhost   *virtualHost
	cluster *cluster
}

func newEnvoyIngress(host string, timeouts DefaultTimeouts) *envoyIngress {
	clusterName := strings.Replace(host, ".", "_", -1)
	return &envoyIngress{
		vhost: &virtualHost{
			Host:            host,
			UpstreamCluster: clusterName,
			Timeout:         timeouts.Route,
			PerTryTimeout:   timeouts.PerTry,
		},
		cluster: &cluster{
			Name:            clusterName,
			VirtualHost:     host,
			Hosts:           []LBHost{},
			Timeout:         timeouts.Cluster,
			HealthCheckPath: "",
			HealthCheckHost: host,
		},
	}
}

func (ing *envoyIngress) addUpstream(host string, port uint32, weight uint32) {
	// Check if the host is already in the list
	// If we wan't to avoid using a for loop, maybe we could implement a Map for a faster lookup.
	// time complexity O(1) vs 0(n) for each iteration.
	for _, h := range ing.cluster.Hosts {
		if h.Host == host && h.Port == port {
			// Host found, so we don't add the duplicate
			logrus.Debugf("Duplicate host found for upstream, not adding : %s:%d for cluster : %s", host, port, ing.cluster.Name)
			return
		}
	}

	// No duplicate found, append the new host
	ing.cluster.Hosts = append(ing.cluster.Hosts, LBHost{Host: host, Port: port, Weight: weight})
	logrus.Debugf("Host added on upstream list : %s:%d for cluster : %s", host, port, ing.cluster.Name)
}

func (ing *envoyIngress) addHealthCheckPath(path string) {
	ing.cluster.HealthCheckPath = path
}

func (ing *envoyIngress) removeHealthCheckPath() {
	ing.cluster.HealthCheckPath = ""
}

func (ing *envoyIngress) addHealthCheckHost(host string) {
	ing.cluster.HealthCheckHost = host
}

func (ing *envoyIngress) removeHealthCheckHost() {
	ing.cluster.HealthCheckHost = ""
}

func (ing *envoyIngress) addTimeout(timeout time.Duration) {
	ing.cluster.Timeout = timeout
	ing.vhost.Timeout = timeout
	ing.vhost.PerTryTimeout = timeout
}

func (ing *envoyIngress) setClusterTimeout(timeout time.Duration) {
	ing.cluster.Timeout = timeout
}

func (ing *envoyIngress) setRouteTimeout(timeout time.Duration) {
	ing.vhost.Timeout = timeout
}

func (ing *envoyIngress) setPerTryTimeout(timeout time.Duration) {
	ing.vhost.PerTryTimeout = timeout
}

func (ing *envoyIngress) setUpstreamHttpVersion(version string) {
	ing.cluster.HttpVersion = version
}

// hostMatch returns true if tlsHost and ruleHost match, with wildcard support
//
// *.a.b ruleHost accepts tlsHost *.a.b but not a.a.b or a.b or a.a.a.b
// a.a.b ruleHost accepts tlsHost a.a.b and *.a.b but not *.a.a.b
func hostMatch(ruleHost, tlsHost string) bool {
	// TODO maybe cache the results for speedup
	pattern := strings.ReplaceAll(strings.ReplaceAll(tlsHost, ".", "\\."), "*", "(?:\\*|[a-z0-9][a-z0-9-_]*)")
	matched, err := regexp.MatchString("^"+pattern+"$", ruleHost)
	if err != nil {
		logrus.Errorf("error in ingress hostname comparison: %s", err.Error())
		return false
	}
	return matched
}

// getHostTlsSecret returns the tls secret configured for a given ingress host
func getHostTlsSecret(ingress *k8s.SourceRoute, host string, secrets []*v1.Secret) (*v1.Secret, error) {
	for _, tls := range ingress.TLS {
		// TODO prefer a.a.b tls secret over *.a.b for host a.a.b when both are configured
		if hostMatch(host, tls.Host) {
			secretNamespace := tls.SecretNamespace
			if secretNamespace == "" {
				secretNamespace = ingress.Namespace
			}
			for _, secret := range secrets {
				if secret.Namespace == secretNamespace &&
					secret.Name == tls.SecretName {
					return secret, nil
				}
			}
			return nil, fmt.Errorf("secret %s/%s not found for host '%s'", secretNamespace, tls.SecretName, host)
		}
	}
	return nil, fmt.Errorf("ingress %s/%s - %s has no tls secret configured", ingress.Namespace, ingress.Name, host)
}

// validateTlsSecret checks that the given secret holds valid tls certificate and key
func validateTlsSecret(secret *v1.Secret) (bool, error) {
	tlsCert, certOk := secret.Data["tls.crt"]
	tlsKey, keyOk := secret.Data["tls.key"]

	if !certOk || !keyOk {
		logrus.Infof("skipping certificate %s/%s: missing 'tls.crt' or 'tls.key'", secret.Namespace, secret.Name)
		return false, nil
	}
	if len(tlsCert) == 0 || len(tlsKey) == 0 {
		logrus.Infof("skipping certificate %s/%s: empty 'tls.crt' or 'tls.key'", secret.Namespace, secret.Name)
		return false, nil
	}

	// discard > P-521 EC private keys
	// P-256, P-384 & P-521 are now supported (see https://github.com/envoyproxy/envoy/issues/10855)
	block, _ := pem.Decode(tlsCert)
	if block == nil {
		return false, fmt.Errorf("error parsing x509 certificate - no PEM block found")
	}
	x509crt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("error parsing x509 certificate: %s", err.Error())
	}
	if x509crt.PublicKeyAlgorithm == x509.ECDSA {
		ecdsaPub, ok := x509crt.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			return false, fmt.Errorf("error in *ecdsa.PublicKey type assertion")
		}
		if ecdsaPub.Curve.Params().BitSize > 521 {
			logrus.Infof("skipping ECDSA %s certificate %s/%s: only P-256, P-384 and P-521 certificates are supported", ecdsaPub.Curve.Params().Name, secret.Namespace, secret.Name)
			return false, nil
		}
	}
	return true, nil
}

func (envoyIng *envoyIngress) addStickySession(stickySessions *policy.StickySessions) {
	envoyIng.vhost.StickySession = true
	envoyIng.vhost.StickySessionCookieName = stickySessions.CookieName
	envoyIng.vhost.StickySessionCookiePath = stickySessions.CookiePath
	envoyIng.vhost.StickySessionCookieTTL = stickySessions.CookieTTL

	changeOnFailure := stickySessions.ChangeOnFailure
	envoyIng.cluster.StickySessionChangeOnFailure = &changeOnFailure
}

// isWildcard checks if the given host rule is a wildcard.
func isWildcard(ruleHost string) bool {
	// Check if the ruleHost starts with '*.'
	return strings.HasPrefix(ruleHost, "*.")
}

func validateSubdomain(ruleHost, host string) bool {
	if strings.HasPrefix(ruleHost, "*.") {
		ruleHost = ruleHost[2:]
	}
	return strings.HasSuffix(host, ruleHost)
}

func translateIngresses(ingresses []*k8s.SourceRoute, syncSecrets bool, secrets []*v1.Secret, timeouts DefaultTimeouts, accessLog string) *envoyConfiguration {
	cfg := &envoyConfiguration{}
	envoyIngresses := map[string]*envoyIngress{}
	ruleHostToIngresses := map[string][]*k8s.SourceRoute{}
	currentUpstreams := make(map[string]UpstreamInfo)

	for _, i := range ingresses {
		for _, ruleHost := range i.RulesHosts {
			ruleHostToIngresses[ruleHost] = append(ruleHostToIngresses[ruleHost], i)
		}
	}

	for ruleHost, ingressList := range ruleHostToIngresses {
		sort.SliceStable(ingressList, func(i, j int) bool {
			return sourceKindPriority(ingressList[i]) < sourceKindPriority(ingressList[j])
		})
		isWildcard := isWildcard(ruleHost)

		if _, ok := envoyIngresses[ruleHost]; !ok {
			envoyIngresses[ruleHost] = newEnvoyIngress(ruleHost, timeouts)
		}

		envoyIngress := envoyIngresses[ruleHost]

		// Determine if any ingress is not in maintenance mode
		hasNonMaintenance := false
		for _, ingress := range ingressList {
			if !ingress.Maintenance {
				hasNonMaintenance = true
				break
			}
		}

		// Add upstreams based on maintenance status
		for _, ingress := range ingressList {
			upstreams := ingress.UpstreamEndpoints
			if len(upstreams) == 0 {
				for _, upstream := range ingress.Upstreams {
					upstreams = append(upstreams, k8s.UpstreamEndpoint{Host: upstream})
				}
			}
			for _, upstream := range upstreams {
				j := upstream.Host
				// Skip this upstream if cluster is in maintenance but keep it if no other cluster can serve it
				if !hasNonMaintenance || !ingress.Maintenance {
					// Check if the upstream is already added
					exists := false
					for _, host := range envoyIngress.cluster.Hosts {
						if host.Host == j && host.Port == upstream.Port {
							exists = true
							break
						}
					}
					if exists {
						continue // skip if the upstream already exists
					}

					class := "none"
					if ingress.Class != nil {
						class = *ingress.Class
					}
					sourceKind := ingress.Source.Kind
					if sourceKind == "" {
						sourceKind = "Ingress"
					}

					// Add upstream; an explicit weight of 0 excludes the upstream
					weight := uint32(1)
					if ingress.Policy != nil && ingress.Policy.Weight != nil {
						weight = *ingress.Policy.Weight
					}
					if weight != 0 {
						envoyIngress.addUpstream(j, upstream.Port, weight)
					}
					upstreamKey := fmt.Sprintf("%s-%s-%d", ruleHost, j, upstream.Port)
					currentUpstreams[upstreamKey] = UpstreamInfo{
						RuleHost:    strings.ReplaceAll(ruleHost, ".", "_"),
						Upstream:    j,
						Namespace:   ingress.Namespace,
						Class:       class,
						ClusterName: ingress.KubernetesClusterName,
						SourceKind:  sourceKind,
						IngressName: ingress.Name,
					}

					EnvoyUpstreamInfo.WithLabelValues(strings.ReplaceAll(ruleHost, ".", "_"), j, ingress.Namespace, class, ingress.KubernetesClusterName, sourceKind, ingress.Name).Set(float64(1))
				} else {
					logrus.Warnf("Endpoint is in maintenance mode, upstream %s will not be added for host %s", j, ruleHost)
				}
			}

			if ingress.Maintenance && hasNonMaintenance {
				continue
			}

			applyRoutePolicy(envoyIngress, ingress.Policy, ruleHost, isWildcard)

			if syncSecrets && envoyIngress.vhost.TlsKey == "" && envoyIngress.vhost.TlsCert == "" {
				if hostTlsSecret, err := getHostTlsSecret(ingress, ruleHost, secrets); err != nil {
					logrus.Infof("%s", err.Error())
				} else {
					valid, err := validateTlsSecret(hostTlsSecret)
					if err != nil {
						logrus.Warnf("secret %s/%s is not valid: %s", hostTlsSecret.Namespace, hostTlsSecret.Name, err.Error())
					} else if valid {
						envoyIngress.vhost.TlsKey = string(hostTlsSecret.Data["tls.key"])
						envoyIngress.vhost.TlsCert = string(hostTlsSecret.Data["tls.crt"])
					}
				}
			}
		}
	}

	// Identify and remove upstreams that no longer exist
	for upstreamKey, info := range previousUpstreams {
		if _, exists := currentUpstreams[upstreamKey]; !exists {
			EnvoyUpstreamInfo.DeleteLabelValues(info.RuleHost, info.Upstream, info.Namespace, info.Class, info.ClusterName, info.SourceKind, info.IngressName)
		}
	}

	// Update the previous state
	previousUpstreams = currentUpstreams

	for _, ingress := range envoyIngresses {
		cfg.Clusters = append(cfg.Clusters, ingress.cluster)
		cfg.VirtualHosts = append(cfg.VirtualHosts, ingress.vhost)
		cfg.AccessLog = accessLog
	}

	numVhosts.Set(float64(len(cfg.VirtualHosts)))
	numClusters.Set(float64(len(cfg.Clusters)))

	return cfg
}

// applyRoutePolicy maps the shared RoutePolicy model onto the generated Envoy
// virtual host and cluster. It is the only place route policy influences
// Envoy generation, whatever Policy Source the policy came from.
func applyRoutePolicy(envoyIng *envoyIngress, routePolicy *policy.RoutePolicy, ruleHost string, isWildcard bool) {
	if isWildcard {
		if routePolicy != nil && routePolicy.HealthCheckHost != nil {
			envoyIng.addHealthCheckHost(*routePolicy.HealthCheckHost)
			if !validateSubdomain(ruleHost, envoyIng.cluster.HealthCheckHost) {
				logrus.Warnf("Healthcheck %s is not on the same subdomain for %s, it will be skipped", envoyIng.cluster.HealthCheckHost, ruleHost)
				envoyIng.cluster.HealthCheckHost = ruleHost
			}
		} else {
			logrus.Warnf("Be careful, healthcheck can't work for wildcard host : %s", envoyIng.cluster.HealthCheckHost)
		}
	}

	if routePolicy == nil {
		return
	}

	if routePolicy.HealthCheckPath != nil {
		envoyIng.addHealthCheckPath(*routePolicy.HealthCheckPath)
	}
	if routePolicy.Timeout != nil {
		envoyIng.addTimeout(*routePolicy.Timeout)
	}
	if routePolicy.ClusterTimeout != nil {
		envoyIng.setClusterTimeout(*routePolicy.ClusterTimeout)
	}
	if routePolicy.RouteTimeout != nil {
		envoyIng.setRouteTimeout(*routePolicy.RouteTimeout)
	}
	if routePolicy.PerTryTimeout != nil {
		envoyIng.setPerTryTimeout(*routePolicy.PerTryTimeout)
	}
	if routePolicy.UpstreamHTTPVersion != nil {
		envoyIng.setUpstreamHttpVersion(*routePolicy.UpstreamHTTPVersion)
	}
	if routePolicy.IdleTimeout != nil {
		timeout := *routePolicy.IdleTimeout
		envoyIng.cluster.IdleTimeout = &timeout
	}
	if routePolicy.MaxConnectionDuration != nil {
		duration := *routePolicy.MaxConnectionDuration
		envoyIng.cluster.MaxConnectionDuration = &duration
	}
	if routePolicy.MaxRequestsPerConnection != nil {
		maxRequests := *routePolicy.MaxRequestsPerConnection
		envoyIng.cluster.MaxRequestsPerConnection = &maxRequests
	}
	if len(routePolicy.RetryOn) > 0 {
		envoyIng.vhost.RetryOn = strings.Join(routePolicy.RetryOn, ",")
	}
	if routePolicy.StickySessions != nil {
		envoyIng.addStickySession(routePolicy.StickySessions)
	}
}

func sourceKindPriority(ingress *k8s.SourceRoute) int {
	// Gateway API policy is applied after Ingress policy so HTTPRoute annotations win on same-host migrations.
	if ingress.Source.Kind == "HTTPRoute" {
		return 1
	}
	return 0
}
