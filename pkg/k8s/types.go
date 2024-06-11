package k8s

import kube "k8s.io/client-go/kubernetes"

type KubernetesConfig struct {
	source      *kube.Clientset
	maintenance bool
}

func NewKubernetesConfig(maintenance bool, clientset *kube.Clientset) *KubernetesConfig {
	return &KubernetesConfig{
		source:      clientset,
		maintenance: maintenance,
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
