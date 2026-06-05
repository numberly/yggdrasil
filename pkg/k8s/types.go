package k8s

import kube "k8s.io/client-go/kubernetes"
import "k8s.io/client-go/rest"

type KubernetesConfig struct {
	source                *kube.Clientset
	restConfig            *rest.Config
	maintenance           bool
	kubernetesClusterName string
	gatewayClasses        []GatewayClassConfig
}

func NewKubernetesConfig(maintenance bool, clientset *kube.Clientset, restConfig *rest.Config, kubernetesClusterName string, gatewayClasses ...GatewayClassConfig) *KubernetesConfig {
	return &KubernetesConfig{
		source:                clientset,
		restConfig:            restConfig,
		maintenance:           maintenance,
		kubernetesClusterName: kubernetesClusterName,
		gatewayClasses:        gatewayClasses,
	}
}

// SyncType represents the type of k8s received message
type SyncType string

// SyncDataEvent represents converted k8s received message
type SyncDataEvent struct {
	_ [0]int
	SyncType
	Data interface{}
}

const (
	COMMAND SyncType = "COMMAND"
	INGRESS SyncType = "INGRESS"
	SECRET  SyncType = "SECRET"
)
