// Command poddy-mesh-bootstrap prepares Headscale credentials for a Poddy
// install. It runs as a sidecar in the Headscale pod, creates reusable
// preauth keys through the Headscale CLI, and writes them to the mesh Secret.
// The portal uses --wait-for-files as an init container so it starts with the
// keys already mounted.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("poddy-mesh-bootstrap: ")

	waitForFiles := flag.Bool("wait-for-files", false, "wait until the mesh key files are populated")
	flag.Parse()

	if *waitForFiles {
		if err := waitForKeyFiles(); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	if err := bootstrap(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
	// This container runs as a sidecar in the Headscale Deployment. A
	// Deployment restarts containers that exit, even successfully, so stay
	// alive after the keys are ready and exit cleanly on SIGTERM.
	log.Print("bootstrap complete; sidecar idle")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}

func waitForKeyFiles() error {
	authFile := strings.TrimSpace(os.Getenv("PODDY_MESH_AUTH_KEY_FILE"))
	clientFile := strings.TrimSpace(os.Getenv("PODDY_MESH_CLIENT_AUTH_KEY_FILE"))
	if authFile == "" || clientFile == "" {
		return errors.New("PODDY_MESH_AUTH_KEY_FILE and PODDY_MESH_CLIENT_AUTH_KEY_FILE are required")
	}
	timeout := durationEnv("PODDY_MESH_WAIT_TIMEOUT", 10*time.Minute)
	deadline := time.Now().Add(timeout)
	for {
		if fileHasContent(authFile) && fileHasContent(clientFile) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for mesh keys in %s and %s", timeout, authFile, clientFile)
		}
		time.Sleep(2 * time.Second)
	}
}

func bootstrap() error {
	namespace := strings.TrimSpace(os.Getenv("PODDY_NAMESPACE"))
	if namespace == "" {
		data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err != nil {
			return fmt.Errorf("PODDY_NAMESPACE is required: %w", err)
		}
		namespace = strings.TrimSpace(string(data))
	}
	secretName := strings.TrimSpace(os.Getenv("PODDY_MESH_SECRET_NAME"))
	if secretName == "" {
		return errors.New("PODDY_MESH_SECRET_NAME is required")
	}
	authKeyKey := envDefault("PODDY_MESH_AUTH_KEY_KEY", "auth-key")
	clientKeyKey := envDefault("PODDY_MESH_CLIENT_AUTH_KEY_KEY", "client-auth-key")
	headscaleContainer := envDefault("PODDY_HEADSCALE_CONTAINER", "headscale")
	headscaleConfig := envDefault("PODDY_HEADSCALE_CONFIG", "/etc/headscale/config.yaml")
	headscaleUser := envDefault("PODDY_HEADSCALE_USER", "poddy")
	expiration := envDefault("PODDY_HEADSCALE_EXPIRATION", "720h")
	podName := strings.TrimSpace(os.Getenv("PODDY_HEADSCALE_POD"))
	selector := strings.TrimSpace(os.Getenv("PODDY_HEADSCALE_SELECTOR"))
	if podName == "" && selector == "" {
		selector = "poddy.io/component=headscale"
	}
	waitTimeout := durationEnv("PODDY_MESH_BOOTSTRAP_WAIT", 5*time.Minute)

	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout+2*time.Minute)
	defer cancel()

	authKey, clientKey, err := readSecretKeys(ctx, client, namespace, secretName, authKeyKey, clientKeyKey)
	if err != nil {
		return err
	}
	if authKey != "" && clientKey != "" {
		log.Printf("mesh keys already present in secret %s/%s", namespace, secretName)
		return nil
	}

	pod, err := waitForHeadscalePod(ctx, client, namespace, podName, selector, headscaleContainer, waitTimeout)
	if err != nil {
		return err
	}
	log.Printf("using Headscale pod %s/%s", namespace, pod.Name)

	if _, err := execHeadscale(ctx, client, config, namespace, pod.Name, headscaleContainer, headscaleConfig, "users", "create", headscaleUser); err != nil {
		log.Printf("ensure Headscale user %q: %v", headscaleUser, err)
	}
	if authKey == "" {
		authKey, err = createPreauthKey(ctx, client, config, namespace, pod.Name, headscaleContainer, headscaleConfig, headscaleUser, expiration, false)
		if err != nil {
			return fmt.Errorf("create workspace preauth key: %w", err)
		}
	}
	if clientKey == "" {
		clientKey, err = createPreauthKey(ctx, client, config, namespace, pod.Name, headscaleContainer, headscaleConfig, headscaleUser, expiration, true)
		if err != nil {
			return fmt.Errorf("create desktop preauth key: %w", err)
		}
	}
	if err := upsertSecret(ctx, client, namespace, secretName, authKeyKey, authKey, clientKeyKey, clientKey); err != nil {
		return fmt.Errorf("write mesh secret: %w", err)
	}
	log.Printf("mesh keys written to secret %s/%s", namespace, secretName)
	return nil
}

func readSecretKeys(ctx context.Context, client kubernetes.Interface, namespace, name, authKeyKey, clientKeyKey string) (string, string, error) {
	secret, err := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("read mesh secret: %w", err)
	}
	return strings.TrimSpace(string(secret.Data[authKeyKey])), strings.TrimSpace(string(secret.Data[clientKeyKey])), nil
}

func waitForHeadscalePod(ctx context.Context, client kubernetes.Interface, namespace, podName, selector, container string, timeout time.Duration) (*corev1.Pod, error) {
	deadline := time.Now().Add(timeout)
	for {
		pod, err := findHeadscalePod(ctx, client, namespace, podName, selector, container)
		if err != nil {
			return nil, err
		}
		if pod != nil {
			return pod, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting for Headscale pod", timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func findHeadscalePod(ctx context.Context, client kubernetes.Interface, namespace, podName, selector, container string) (*corev1.Pod, error) {
	if podName != "" {
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read Headscale pod: %w", err)
		}
		if podReady(pod, container) {
			return pod, nil
		}
		return nil, nil
	}
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("list Headscale pods: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if podReady(pod, container) {
			return pod, nil
		}
	}
	return nil, nil
}

func podReady(pod *corev1.Pod, container string) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == container {
			return status.Ready
		}
	}
	return false
}

func createPreauthKey(ctx context.Context, client kubernetes.Interface, config *rest.Config, namespace, podName, container, headscaleConfig, user, expiration string, ephemeral bool) (string, error) {
	args := []string{"preauthkeys", "create", "--user", user, "--reusable", "--expiration", expiration, "-o", "json"}
	if ephemeral {
		args = append(args, "--ephemeral")
	}
	out, err := execHeadscale(ctx, client, config, namespace, podName, container, headscaleConfig, args...)
	if err != nil {
		return "", err
	}
	var payload struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &payload); err != nil {
		return "", fmt.Errorf("parse preauth key: %w (output %q)", err, strings.TrimSpace(string(out)))
	}
	if payload.Key == "" {
		return "", fmt.Errorf("Headscale returned an empty preauth key")
	}
	return payload.Key, nil
}

func execHeadscale(ctx context.Context, client kubernetes.Interface, config *rest.Config, namespace, podName, container, headscaleConfig string, args ...string) ([]byte, error) {
	command := append([]string{"headscale", "--config", headscaleConfig}, args...)
	request := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(config, "POST", request.URL())
	if err != nil {
		return nil, fmt.Errorf("exec Headscale: %w", err)
	}
	var stdout, stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr}); err != nil {
		return nil, fmt.Errorf("headscale %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func upsertSecret(ctx context.Context, client kubernetes.Interface, namespace, name, authKeyKey, authKey, clientKeyKey, clientKey string) error {
	secrets := client.CoreV1().Secrets(namespace)
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Type:       corev1.SecretTypeOpaque,
			StringData: map[string]string{
				authKeyKey:   authKey,
				clientKeyKey: clientKey,
			},
		}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[authKeyKey] = []byte(authKey)
	secret.Data[clientKeyKey] = []byte(clientKey)
	_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	return err
}

func fileHasContent(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(data)) != ""
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
