package k8s

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/sirupsen/logrus"
	"github.com/uswitch/yggdrasil/pkg/policy"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	networkingv1 "k8s.io/api/networking/v1"
	networkingv1beta1 "k8s.io/api/networking/v1beta1"
)

type SourceRoute struct {
	Namespace             string
	Name                  string
	Class                 *string
	Annotations           map[string]string
	RulesHosts            []string
	Upstreams             []string
	UpstreamEndpoints     []UpstreamEndpoint
	TLS                   map[string]*IngressTLS
	Maintenance           bool
	KubernetesClusterName string
	Source                RouteSource
	Policy *policy.RoutePolicy
	PolicySource *policy.Source
}

type IngressTLS struct {
	Host            string
	SecretNamespace string
	SecretName      string
}

type UpstreamEndpoint struct {
	Host string
	Port uint32
}

type RouteSource struct {
	Kind                  string
	Namespace             string
	Name                  string
	Class                 string
	KubernetesClusterName string
}

func (a *Aggregator) GetSourceRoutes() ([]*SourceRoute, error) {
	ing := make([]*SourceRoute, 0)
	for _, store := range a.ingressStores {
		ingresses := store.Store.List()
		for _, obj := range ingresses {
			genericIng, err := convertToGenericIngress(obj, store.Maintenance, store.KubernetesClusterName)
			if err != nil {
				return nil, err
			}
			ing = append(ing, genericIng)
		}
	}
	for _, store := range a.gatewayStores {
		result, err := ConvertGatewayResources(store)
		if err != nil {
			logrus.Warnf("gateway conversion failed: cluster=%s error=%s", store.ClusterName, err)
			continue
		}
		for _, d := range result.Diagnostics {
			logrus.Warnf("gateway diagnostic: cluster=%s host=%s source=%s/%s/%s reason=%s",
				store.ClusterName, d.Host, d.Source.Kind, d.Source.Namespace, d.Source.Name, d.Reason)
		}
		ing = append(ing, result.Routes...)
	}
	return ing, nil
}

func convertToGenericIngress(ing interface{}, maintenance bool, kubernetesClusterName string) (ingress *SourceRoute, err error) {
	switch t := ing.(type) {
	case *extensionsv1beta1.Ingress:
		i, ok := ing.(*extensionsv1beta1.Ingress)
		if !ok {
			return nil, fmt.Errorf("unexpected object in store: %+v", ing)
		}
		ingress = convertExtensionsv1beta1Ingress(i, maintenance, kubernetesClusterName)
	case *networkingv1beta1.Ingress:
		i, ok := ing.(*networkingv1beta1.Ingress)
		if !ok {
			return nil, fmt.Errorf("unexpected object in store: %+v", ing)
		}
		ingress = convertNetworkingv1beta1Ingress(i, maintenance, kubernetesClusterName)
	case *networkingv1.Ingress:
		i, ok := ing.(*networkingv1.Ingress)
		if !ok {
			return nil, fmt.Errorf("unexpected object in store: %+v", ing)
		}
		ingress = convertNetworkingv1Ingress(i, maintenance, kubernetesClusterName)
	default:
		err = fmt.Errorf("unrecognized type for: %T", t)
	}
	if ingress != nil {
		attachAnnotationPolicy(ingress)
	}
	return
}

func attachAnnotationPolicy(route *SourceRoute) {
	parsed, diagnostics := policy.ParseAnnotations(route.Annotations)
	for _, diagnostic := range diagnostics {
		logrus.Warnf("policy annotations for %s %s/%s: %s", route.Source.Kind, route.Namespace, route.Name, diagnostic)
	}
	route.Annotations = policy.StripAnnotations(route.Annotations)
	if parsed == nil {
		return
	}
	route.Policy = parsed
	route.PolicySource = &policy.Source{Kind: "Annotations", Namespace: route.Namespace, Name: route.Name}
}

func convertExtensionsv1beta1Ingress(i *extensionsv1beta1.Ingress, maintenance bool, kubernetesClusterName string) *SourceRoute {
	return &SourceRoute{
		Namespace:   i.Namespace,
		Name:        i.Name,
		Class:       i.Spec.IngressClassName,
		Annotations: i.Annotations,
		RulesHosts: func(rules *[]extensionsv1beta1.IngressRule) (hosts []string) {
			for _, rule := range *rules {
				hosts = append(hosts, rule.Host)
			}
			return
		}(&i.Spec.Rules),
		Upstreams: func(i *[]extensionsv1beta1.IngressLoadBalancerIngress) (upstreams []string) {
			for _, j := range *i {
				if j.Hostname != "" {
					upstreams = append(upstreams, j.Hostname)
				} else {
					upstreams = append(upstreams, j.IP)
				}
			}
			return
		}(&i.Status.LoadBalancer.Ingress),
		TLS: func(itls []extensionsv1beta1.IngressTLS) (tls map[string]*IngressTLS) {
			tls = make(map[string]*IngressTLS)
			for _, t := range itls {
				for _, h := range t.Hosts {
					tls[h] = &IngressTLS{
						Host:            h,
						SecretNamespace: i.Namespace,
						SecretName:      t.SecretName,
					}
				}
			}
			return
		}(i.Spec.TLS),
		Maintenance:           maintenance,
		KubernetesClusterName: kubernetesClusterName,
		Source: RouteSource{
			Kind:                  "Ingress",
			Namespace:             i.Namespace,
			Name:                  i.Name,
			Class:                 stringValue(i.Spec.IngressClassName),
			KubernetesClusterName: kubernetesClusterName,
		},
	}
}

func convertNetworkingv1beta1Ingress(i *networkingv1beta1.Ingress, maintenance bool, kubernetesClusterName string) *SourceRoute {
	return &SourceRoute{
		Namespace:   i.Namespace,
		Name:        i.Name,
		Class:       i.Spec.IngressClassName,
		Annotations: i.Annotations,
		RulesHosts: func(rules *[]networkingv1beta1.IngressRule) (hosts []string) {
			for _, rule := range *rules {
				hosts = append(hosts, rule.Host)
			}
			return
		}(&i.Spec.Rules),
		Upstreams: func(i *[]networkingv1beta1.IngressLoadBalancerIngress) (upstreams []string) {
			for _, j := range *i {
				if j.Hostname != "" {
					upstreams = append(upstreams, j.Hostname)
				} else {
					upstreams = append(upstreams, j.IP)
				}
			}
			return
		}(&i.Status.LoadBalancer.Ingress),
		TLS: func(itls []networkingv1beta1.IngressTLS) (tls map[string]*IngressTLS) {
			tls = make(map[string]*IngressTLS)
			for _, t := range itls {
				for _, h := range t.Hosts {
					tls[h] = &IngressTLS{
						Host:            h,
						SecretNamespace: i.Namespace,
						SecretName:      t.SecretName,
					}
				}
			}
			return
		}(i.Spec.TLS),
		Maintenance:           maintenance,
		KubernetesClusterName: kubernetesClusterName,
		Source: RouteSource{
			Kind:                  "Ingress",
			Namespace:             i.Namespace,
			Name:                  i.Name,
			Class:                 stringValue(i.Spec.IngressClassName),
			KubernetesClusterName: kubernetesClusterName,
		},
	}
}

func convertNetworkingv1Ingress(i *networkingv1.Ingress, maintenance bool, kubernetesClusterName string) *SourceRoute {
	return &SourceRoute{
		Namespace:   i.Namespace,
		Name:        i.Name,
		Class:       i.Spec.IngressClassName,
		Annotations: i.Annotations,
		RulesHosts: func(rules *[]networkingv1.IngressRule) (hosts []string) {
			for _, rule := range *rules {
				hosts = append(hosts, rule.Host)
			}
			return
		}(&i.Spec.Rules),
		Upstreams: func(i *[]networkingv1.IngressLoadBalancerIngress) (upstreams []string) {
			for _, j := range *i {
				if j.Hostname != "" {
					upstreams = append(upstreams, j.Hostname)
				} else {
					upstreams = append(upstreams, j.IP)
				}
			}
			return
		}(&i.Status.LoadBalancer.Ingress),
		TLS: func(itls []networkingv1.IngressTLS) (tls map[string]*IngressTLS) {
			tls = make(map[string]*IngressTLS)
			for _, t := range itls {
				for _, h := range t.Hosts {
					tls[h] = &IngressTLS{
						Host:            h,
						SecretNamespace: i.Namespace,
						SecretName:      t.SecretName,
					}
				}
			}
			return
		}(i.Spec.TLS),
		Maintenance:           maintenance,
		KubernetesClusterName: kubernetesClusterName,
		Source: RouteSource{
			Kind:                  "Ingress",
			Namespace:             i.Namespace,
			Name:                  i.Name,
			Class:                 stringValue(i.Spec.IngressClassName),
			KubernetesClusterName: kubernetesClusterName,
		},
	}
}

func SourceRouteEqual(a, b *SourceRoute) bool {
	if a.Name != b.Name ||
		a.Namespace != b.Namespace ||
		!deepStringEqualIgnoreOrder(a.RulesHosts, b.RulesHosts) ||
		!deepStringEqualIgnoreOrder(a.Upstreams, b.Upstreams) ||
		!reflect.DeepEqual(a.Annotations, b.Annotations) ||
		!reflect.DeepEqual(a.TLS, b.TLS) ||
		!reflect.DeepEqual(a.Policy, b.Policy) {
		return false
	}

	if a.getUsableIngressClass() != b.getUsableIngressClass() {
		return false
	}

	return true
}

func (ing *SourceRoute) getUsableIngressClass() string {
	if ing.Annotations["kubernetes.io/ingress.class"] != "" {
		return ing.Annotations["kubernetes.io/ingress.class"]
	}
	if ing.Class != nil {
		return *ing.Class
	}
	return ""
}

func deepStringEqualIgnoreOrder(a, b []string) bool {
	sort.Strings(a)
	sort.Strings(b)
	return reflect.DeepEqual(a, b)
}

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
