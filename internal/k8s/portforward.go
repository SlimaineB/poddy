package k8s

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

func (m *Manager) PortForward(podName string, remotePort int) (int, func(), error) {
	request := m.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(m.Namespace).
		Name(podName).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(m.Rest)
	if err != nil {
		return 0, nil, err
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, request.URL())
	stop := make(chan struct{})
	ready := make(chan struct{})

	forwarder, err := portforward.New(
		dialer,
		[]string{fmt.Sprintf("0:%d", remotePort)},
		stop,
		ready,
		io.Discard,
		io.Discard,
	)
	if err != nil {
		return 0, nil, err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- forwarder.ForwardPorts()
	}()

	select {
	case <-ready:
	case err := <-errCh:
		return 0, nil, err
	case <-time.After(15 * time.Second):
		close(stop)
		return 0, nil, fmt.Errorf("port-forward timed out")
	}

	ports, err := forwarder.GetPorts()
	if err != nil {
		close(stop)
		return 0, nil, err
	}
	if len(ports) == 0 {
		close(stop)
		return 0, nil, fmt.Errorf("port-forward returned no ports")
	}

	var once sync.Once
	stopFn := func() {
		once.Do(func() { close(stop) })
	}
	return int(ports[0].Local), stopFn, nil
}
