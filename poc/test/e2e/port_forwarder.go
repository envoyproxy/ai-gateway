package e2e

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type portForwarder interface {
	Start() error

	Stop()

	WaitForStop()

	// Address returns the address of the local forwarded address.
	Address() string
}

type localForwarder struct {
	types.NamespacedName
	*client

	localPort int
	podPort   int

	stopCh chan struct{}
}

func newLocalPortForwarder(client *client, namespacedName types.NamespacedName, localPort, podPort int) (portForwarder, error) {
	f := &localForwarder{
		stopCh:         make(chan struct{}),
		client:         client,
		NamespacedName: namespacedName,
		localPort:      localPort,
		podPort:        podPort,
	}
	if f.localPort == 0 {
		// Get a random port.
		l, err := net.Listen("tcp", "localhost:0")
		if err != nil {
			return nil, fmt.Errorf("failed to get a local available port for Pod %q: %w", namespacedName, err)
		}
		err = l.Close()
		if err != nil {
			return nil, err
		}
		f.localPort = l.Addr().(*net.TCPAddr).Port
	}
	return f, nil
}

func (f *localForwarder) Start() error {
	errCh := make(chan error, 1)
	readyCh := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-f.stopCh:
				return
			default:
			}

			fw, err := f.buildKubernetesPortForwarder(readyCh)
			if err != nil {
				errCh <- err
				return
			}

			if err := fw.ForwardPorts(); err != nil {
				errCh <- err
				return
			}

			readyCh = nil
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("failed to start port forwarder: %w", err)
	case <-readyCh:
		return nil
	}
}

func (f *localForwarder) buildKubernetesPortForwarder(readyCh chan struct{}) (*portforward.PortForwarder, error) {
	restClient, err := rest.RESTClientFor(f.restConfig())
	if err != nil {
		return nil, err
	}

	req := restClient.Post().Resource("pods").Namespace(f.Namespace).Name(f.Name).SubResource("portforward")
	serverURL := req.URL()

	roundTripper, upgrader, err := spdy.RoundTripperFor(f.restConfig())
	if err != nil {
		return nil, fmt.Errorf("failure creating roundtripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, serverURL)
	fw, err := portforward.NewOnAddresses(dialer,
		[]string{"localhost"},
		[]string{fmt.Sprintf("%d:%d", f.localPort, f.podPort)},
		f.stopCh,
		readyCh,
		io.Discard,
		os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("failed establishing portforward: %w", err)
	}

	return fw, nil
}

func (f *localForwarder) Stop() {
	close(f.stopCh)
}

func (f *localForwarder) WaitForStop() {
	<-f.stopCh
}

func (f *localForwarder) Address() string {
	return fmt.Sprintf("localhost:%d", f.localPort)
}
