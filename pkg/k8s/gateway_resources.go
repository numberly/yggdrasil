package k8s

import (
	"fmt"
	"sort"
	"strings"

	"github.com/uswitch/yggdrasil/pkg/apis/yggdrasil/v1alpha1"
	"github.com/uswitch/yggdrasil/pkg/policy"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type GatewayClassConfig struct {
	Name             string `json:"name"`
	ServiceNamespace string `json:"serviceNamespace"`
	ServiceName      string `json:"serviceName"`
}

type GatewayStores struct {
	GatewayClasses    cache.Store
	Gateways          cache.Store
	HTTPRoutes        cache.Store
	ReferenceGrants   cache.Store
	Services          cache.Store
	Namespaces        cache.Store
	YggdrasilPolicies cache.Store
	Maintenance       bool
	ClusterName       string
	ClassConfigs      []GatewayClassConfig
}

type GatewayDiagnostic struct {
	Host   string
	Source RouteSource
	Reason string
}

type GatewayConversionResult struct {
	Routes      []*SourceRoute
	Diagnostics []GatewayDiagnostic
}

func ConvertGatewayResources(stores GatewayStores) (GatewayConversionResult, error) {
	serviceByKey := map[string]*v1.Service{}
	for _, obj := range storeList(stores.Services) {
		service, ok := obj.(*v1.Service)
		if !ok {
			return GatewayConversionResult{}, fmt.Errorf("unexpected object in service store: %+v", obj)
		}
		serviceByKey[namespacedName(service.Namespace, service.Name)] = service
	}

	gatewayByClass := map[string][]*gatewayv1.Gateway{}
	for _, obj := range storeList(stores.Gateways) {
		gateway, ok := obj.(*gatewayv1.Gateway)
		if !ok {
			return GatewayConversionResult{}, fmt.Errorf("unexpected object in gateway store: %+v", obj)
		}
		gatewayByClass[string(gateway.Spec.GatewayClassName)] = append(gatewayByClass[string(gateway.Spec.GatewayClassName)], gateway)
	}

	knownGatewayClasses := map[string]bool{}
	for _, obj := range storeList(stores.GatewayClasses) {
		gatewayClass, ok := obj.(*gatewayv1.GatewayClass)
		if !ok {
			return GatewayConversionResult{}, fmt.Errorf("unexpected object in gatewayclass store: %+v", obj)
		}
		knownGatewayClasses[string(gatewayClass.Name)] = true
	}

	namespaceByName := map[string]*v1.Namespace{}
	for _, obj := range storeList(stores.Namespaces) {
		namespace, ok := obj.(*v1.Namespace)
		if !ok {
			return GatewayConversionResult{}, fmt.Errorf("unexpected object in namespace store: %+v", obj)
		}
		namespaceByName[namespace.Name] = namespace
	}

	result := GatewayConversionResult{}

	policiesByTarget, policyDiagnostics, err := indexPoliciesByTarget(stores.YggdrasilPolicies, stores.ClusterName)
	if err != nil {
		return GatewayConversionResult{}, err
	}
	result.Diagnostics = append(result.Diagnostics, policyDiagnostics...)

	for _, obj := range storeList(stores.HTTPRoutes) {
		route, ok := obj.(*gatewayv1.HTTPRoute)
		if !ok {
			return GatewayConversionResult{}, fmt.Errorf("unexpected object in HTTPRoute store: %+v", obj)
		}

		for _, classConfig := range stores.ClassConfigs {
			if !knownGatewayClasses[classConfig.Name] {
				continue
			}
			for _, gateway := range gatewayByClass[classConfig.Name] {
				for _, listener := range gateway.Spec.Listeners {
					if !httpRouteAttachesToListener(route, gateway, listener, namespaceByName) {
						continue
					}
					hosts := matchingHTTPRouteHosts(route, listener)
					if len(hosts) == 0 {
						continue
					}
					upstreams := gatewayServiceUpstreams(classConfig, listener, serviceByKey)
					if len(upstreams) == 0 {
						continue
					}
					if denyReason := listenerSecretRefDenyReason(gateway, listener, stores.ReferenceGrants); denyReason != "" {
						source := RouteSource{
							Kind:                  "HTTPRoute",
							Namespace:             route.Namespace,
							Name:                  route.Name,
							Class:                 classConfig.Name,
							KubernetesClusterName: stores.ClusterName,
						}
						for _, host := range hosts {
							result.Diagnostics = append(result.Diagnostics, GatewayDiagnostic{
								Host:   host,
								Source: source,
								Reason: denyReason,
							})
						}
						continue
					}
					source := RouteSource{
						Kind:                  "HTTPRoute",
						Namespace:             route.Namespace,
						Name:                  route.Name,
						Class:                 classConfig.Name,
						KubernetesClusterName: stores.ClusterName,
					}
					for _, diagnostic := range listenerCertificateRefDiagnostics(source, listener, hosts) {
						result.Diagnostics = append(result.Diagnostics, diagnostic)
					}
					sourceRoute := &SourceRoute{
						Namespace:             route.Namespace,
						Name:                  route.Name,
						Class:                 strPtr(classConfig.Name),
						Annotations:           route.Annotations,
						RulesHosts:            hosts,
						Upstreams:             upstreamHosts(upstreams),
						UpstreamEndpoints:     upstreams,
						TLS:                   gatewayListenerTLS(gateway, listener, hosts),
						Maintenance:           stores.Maintenance,
						KubernetesClusterName: stores.ClusterName,
						Source:                source,
					}
					result.Diagnostics = append(result.Diagnostics, attachPolicyToSourceRoute(sourceRoute, policiesByTarget[namespacedName(route.Namespace, route.Name)])...)
					result.Routes = append(result.Routes, sourceRoute)
				}
			}
		}
	}

	return resolvePolicyConflicts(result), nil
}

func httpRouteAttachesToListener(route *gatewayv1.HTTPRoute, gateway *gatewayv1.Gateway, listener gatewayv1.Listener, namespaceByName map[string]*v1.Namespace) bool {
	if len(route.Spec.ParentRefs) == 0 {
		return false
	}
	for _, parentRef := range route.Spec.ParentRefs {
		if parentRef.Group != nil && string(*parentRef.Group) != gatewayv1.GroupName {
			continue
		}
		if parentRef.Kind != nil && string(*parentRef.Kind) != "Gateway" {
			continue
		}
		namespace := route.Namespace
		if parentRef.Namespace != nil {
			namespace = string(*parentRef.Namespace)
		}
		if namespace != gateway.Namespace || string(parentRef.Name) != gateway.Name {
			continue
		}
		if parentRef.SectionName != nil && *parentRef.SectionName != listener.Name {
			continue
		}
		if !listenerAllowsRouteNamespace(listener, gateway.Namespace, route.Namespace, namespaceByName) {
			continue
		}
		return true
	}
	return false
}

func listenerAllowsRouteNamespace(listener gatewayv1.Listener, gatewayNamespace, routeNamespace string, namespaceByName map[string]*v1.Namespace) bool {
	from := gatewayv1.NamespacesFromSame
	if listener.AllowedRoutes != nil &&
		listener.AllowedRoutes.Namespaces != nil &&
		listener.AllowedRoutes.Namespaces.From != nil {
		from = *listener.AllowedRoutes.Namespaces.From
	}

	switch from {
	case gatewayv1.NamespacesFromAll:
		return true
	case gatewayv1.NamespacesFromSelector:
		if listener.AllowedRoutes == nil ||
			listener.AllowedRoutes.Namespaces == nil ||
			listener.AllowedRoutes.Namespaces.Selector == nil {
			return false
		}
		namespace := namespaceByName[routeNamespace]
		if namespace == nil {
			return false
		}
		selector, err := metav1.LabelSelectorAsSelector(listener.AllowedRoutes.Namespaces.Selector)
		if err != nil {
			return false
		}
		return selector.Matches(labels.Set(namespace.Labels))
	case gatewayv1.NamespacesFromSame:
		return routeNamespace == gatewayNamespace
	default:
		return false
	}
}

func matchingHTTPRouteHosts(route *gatewayv1.HTTPRoute, listener gatewayv1.Listener) []string {
	hosts := []string{}
	if len(route.Spec.Hostnames) == 0 {
		if listener.Hostname != nil {
			return []string{string(*listener.Hostname)}
		}
		return hosts
	}
	for _, hostname := range route.Spec.Hostnames {
		host := string(hostname)
		if listener.Hostname == nil || hostMatchesGatewayListener(host, string(*listener.Hostname)) {
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	return uniqueStrings(hosts)
}

func hostMatchesGatewayListener(routeHost, listenerHost string) bool {
	if listenerHost == "" || listenerHost == "*" || routeHost == listenerHost {
		return true
	}
	if strings.HasPrefix(listenerHost, "*.") {
		prefix, ok := strings.CutSuffix(routeHost, strings.TrimPrefix(listenerHost, "*"))
		return ok && prefix != "" && !strings.Contains(prefix, ".")
	}
	return false
}

func gatewayServiceUpstreams(classConfig GatewayClassConfig, listener gatewayv1.Listener, services map[string]*v1.Service) []UpstreamEndpoint {
	service := services[namespacedName(classConfig.ServiceNamespace, classConfig.ServiceName)]
	if service == nil {
		return nil
	}
	port := servicePortForListener(service, listener)
	if port == 0 {
		return nil
	}
	upstreams := []UpstreamEndpoint{}
	for _, externalIP := range service.Spec.ExternalIPs {
		upstreams = append(upstreams, UpstreamEndpoint{Host: externalIP, Port: port})
	}
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		if ingress.Hostname != "" {
			upstreams = append(upstreams, UpstreamEndpoint{Host: ingress.Hostname, Port: port})
			continue
		}
		if ingress.IP != "" {
			upstreams = append(upstreams, UpstreamEndpoint{Host: ingress.IP, Port: port})
		}
	}
	return uniqueUpstreams(upstreams)
}

func servicePortForListener(service *v1.Service, listener gatewayv1.Listener) uint32 {
	for _, p := range service.Spec.Ports {
		if p.Port == int32(listener.Port) {
			return uint32(p.Port)
		}
	}
	return 0
}

func listenerSecretRefDenyReason(gateway *gatewayv1.Gateway, listener gatewayv1.Listener, grants cache.Store) string {
	if listener.TLS == nil || len(listener.TLS.CertificateRefs) == 0 {
		return ""
	}
	certificateRef := listener.TLS.CertificateRefs[0]
	if !isCoreSecretRef(certificateRef) {
		return fmt.Sprintf("certificateRef %s is not a core Secret reference", certificateRef.Name)
	}
	if certificateRef.Namespace == nil || string(*certificateRef.Namespace) == gateway.Namespace {
		return ""
	}
	secretNamespace := string(*certificateRef.Namespace)
	secretName := string(certificateRef.Name)
	if isCrossNamespaceSecretRefPermitted(grants, gateway.Namespace, secretNamespace, secretName) {
		return ""
	}
	return fmt.Sprintf("cross-namespace certificateRef %s/%s denied: no matching ReferenceGrant", secretNamespace, secretName)
}

func isCrossNamespaceSecretRefPermitted(store cache.Store, gatewayNamespace, secretNamespace, secretName string) bool {
	for _, obj := range storeList(store) {
		grant, ok := obj.(*gatewayv1.ReferenceGrant)
		if !ok || grant.Namespace != secretNamespace {
			continue
		}
		if !referenceGrantMatchesFrom(grant, gatewayNamespace) {
			continue
		}
		if referenceGrantMatchesTo(grant, secretName) {
			return true
		}
	}
	return false
}

func referenceGrantMatchesFrom(grant *gatewayv1.ReferenceGrant, gatewayNamespace string) bool {
	for _, from := range grant.Spec.From {
		if string(from.Group) == gatewayv1.GroupName &&
			string(from.Kind) == "Gateway" &&
			string(from.Namespace) == gatewayNamespace {
			return true
		}
	}
	return false
}

func referenceGrantMatchesTo(grant *gatewayv1.ReferenceGrant, secretName string) bool {
	for _, to := range grant.Spec.To {
		if string(to.Group) != "" || string(to.Kind) != "Secret" {
			continue
		}
		if to.Name == nil || string(*to.Name) == secretName {
			return true
		}
	}
	return false
}

func isCoreSecretRef(ref gatewayv1.SecretObjectReference) bool {
	return (ref.Group == nil || string(*ref.Group) == "") &&
		(ref.Kind == nil || string(*ref.Kind) == "Secret")
}

func gatewayListenerTLS(gateway *gatewayv1.Gateway, listener gatewayv1.Listener, hosts []string) map[string]*IngressTLS {
	tls := map[string]*IngressTLS{}
	if listener.TLS == nil || len(listener.TLS.CertificateRefs) == 0 {
		return tls
	}
	certificateRef := listener.TLS.CertificateRefs[0]
	secretName := string(certificateRef.Name)
	secretNamespace := gateway.Namespace
	if certificateRef.Namespace != nil {
		secretNamespace = string(*certificateRef.Namespace)
	}
	for _, host := range hosts {
		tls[host] = &IngressTLS{Host: host, SecretNamespace: secretNamespace, SecretName: secretName}
	}
	return tls
}

func listenerCertificateRefDiagnostics(source RouteSource, listener gatewayv1.Listener, hosts []string) []GatewayDiagnostic {
	if listener.TLS == nil || len(listener.TLS.CertificateRefs) <= 1 {
		return nil
	}
	diagnostics := []GatewayDiagnostic{}
	for _, host := range hosts {
		diagnostics = append(diagnostics, GatewayDiagnostic{
			Host:   host,
			Source: source,
			Reason: "multiple certificateRefs configured; only the first certificateRef is used",
		})
	}
	return diagnostics
}

func indexPoliciesByTarget(store cache.Store, clusterName string) (map[string][]*v1alpha1.YggdrasilPolicy, []GatewayDiagnostic, error) {
	byTarget := map[string][]*v1alpha1.YggdrasilPolicy{}
	diagnostics := []GatewayDiagnostic{}
	for _, obj := range storeList(store) {
		yggdrasilPolicy, ok := obj.(*v1alpha1.YggdrasilPolicy)
		if !ok {
			return nil, nil, fmt.Errorf("unexpected object in yggdrasilpolicy store: %+v", obj)
		}
		if err := policy.ValidateTargetRef(yggdrasilPolicy.Spec.TargetRef); err != nil {
			diagnostics = append(diagnostics, GatewayDiagnostic{
				Source: RouteSource{
					Kind:                  "YggdrasilPolicy",
					Namespace:             yggdrasilPolicy.Namespace,
					Name:                  yggdrasilPolicy.Name,
					KubernetesClusterName: clusterName,
				},
				Reason: err.Error(),
			})
			continue
		}
		target := namespacedName(yggdrasilPolicy.Namespace, yggdrasilPolicy.Spec.TargetRef.Name)
		byTarget[target] = append(byTarget[target], yggdrasilPolicy)
	}
	return byTarget, diagnostics, nil
}

func attachPolicyToSourceRoute(route *SourceRoute, policies []*v1alpha1.YggdrasilPolicy) []GatewayDiagnostic {
	diagnostics := []GatewayDiagnostic{}
	diagnose := func(source RouteSource, reason string) {
		for _, host := range route.RulesHosts {
			diagnostics = append(diagnostics, GatewayDiagnostic{Host: host, Source: source, Reason: reason})
		}
	}

	if len(policies) == 0 {
		annotationPolicy, annotationDiagnostics := policy.ParseAnnotations(route.Annotations)
		for _, diagnostic := range annotationDiagnostics {
			diagnose(route.Source, diagnostic)
		}
		route.Annotations = policy.StripAnnotations(route.Annotations)
		if annotationPolicy != nil {
			route.Policy = annotationPolicy
			route.PolicySource = &policy.Source{Kind: "Annotations", Namespace: route.Namespace, Name: route.Name}
		}
		return diagnostics
	}

	route.Annotations = policy.StripAnnotations(route.Annotations)

	if len(policies) > 1 {
		names := make([]string, 0, len(policies))
		for _, duplicate := range policies {
			names = append(names, duplicate.Name)
		}
		sort.Strings(names)
		diagnose(route.Source, fmt.Sprintf("duplicate YggdrasilPolicies target this HTTPRoute (%s); all are ignored", strings.Join(names, ", ")))
		return diagnostics
	}

	yggdrasilPolicy := policies[0]
	policySource := RouteSource{
		Kind:                  "YggdrasilPolicy",
		Namespace:             yggdrasilPolicy.Namespace,
		Name:                  yggdrasilPolicy.Name,
		KubernetesClusterName: route.KubernetesClusterName,
	}
	parsed, err := policy.ParseSpec(yggdrasilPolicy.Spec)
	if err != nil {
		diagnose(policySource, fmt.Sprintf("invalid YggdrasilPolicy rejected as a whole: %s", err))
		return diagnostics
	}
	route.Policy = parsed
	route.PolicySource = &policy.Source{Kind: "YggdrasilPolicy", Namespace: yggdrasilPolicy.Namespace, Name: yggdrasilPolicy.Name}
	return diagnostics
}

func resolvePolicyConflicts(result GatewayConversionResult) GatewayConversionResult {
	byHost := map[string][]*SourceRoute{}
	for _, route := range result.Routes {
		for _, host := range route.RulesHosts {
			byHost[host] = append(byHost[host], route)
		}
	}

	conflictedHosts := map[*SourceRoute]map[string]bool{}
	markConflicted := func(route *SourceRoute, host string) {
		if conflictedHosts[route] == nil {
			conflictedHosts[route] = map[string]bool{}
		}
		conflictedHosts[route][host] = true
	}

	for host, routes := range byHost {
		policyBySourceKind := map[string]string{}
		for _, route := range routes {
			signature := route.Policy.Signature()
			if previousSignature, ok := policyBySourceKind[route.Source.Kind]; ok && previousSignature != signature {
				for _, conflicted := range routes {
					if conflicted.Source.Kind == route.Source.Kind {
						markConflicted(conflicted, host)
					}
				}
				result.Diagnostics = append(result.Diagnostics, GatewayDiagnostic{
					Host:   host,
					Source: route.Source,
					Reason: "same source kind policy conflict",
				})
			}
			policyBySourceKind[route.Source.Kind] = signature
		}
	}

	filtered := result.Routes[:0]
	for _, route := range result.Routes {
		keptHosts := route.RulesHosts[:0]
		for _, host := range route.RulesHosts {
			if !conflictedHosts[route][host] {
				keptHosts = append(keptHosts, host)
			}
		}
		route.RulesHosts = keptHosts
		if len(route.RulesHosts) > 0 {
			filtered = append(filtered, route)
		}
	}
	result.Routes = filtered
	return result
}

func upstreamHosts(upstreams []UpstreamEndpoint) []string {
	hosts := make([]string, 0, len(upstreams))
	for _, upstream := range upstreams {
		hosts = append(hosts, upstream.Host)
	}
	return hosts
}

func uniqueUpstreams(upstreams []UpstreamEndpoint) []UpstreamEndpoint {
	seen := map[UpstreamEndpoint]bool{}
	out := []UpstreamEndpoint{}
	for _, upstream := range upstreams {
		if upstream.Host == "" || seen[upstream] {
			continue
		}
		seen[upstream] = true
		out = append(out, upstream)
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func namespacedName(namespace, name string) string {
	return namespace + "/" + name
}

func strPtr(value string) *string {
	return &value
}

func storeList(store cache.Store) []interface{} {
	if store == nil {
		return nil
	}
	return store.List()
}
