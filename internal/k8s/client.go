package k8s

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func New(kubeconfig, context string, qps float64, burst int) (kubernetes.Interface, *rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		if kubeconfig != "" {
			rules.ExplicitPath = kubeconfig
		}
		overrides := &clientcmd.ConfigOverrides{CurrentContext: context}
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
		if err != nil {
			return nil, nil, err
		}
	}
	if qps > 0 {
		cfg.QPS = float32(qps)
	}
	if burst > 0 {
		cfg.Burst = burst
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	return client, cfg, nil
}
