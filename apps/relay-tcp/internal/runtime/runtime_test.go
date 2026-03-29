package runtime

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTCPRelayServiceForwardsTraffic(t *testing.T) {
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetListener.Close()

	go func() {
		for {
			conn, err := targetListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				reader := bufio.NewReader(c)
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				_, _ = io.WriteString(c, "echo:"+line)
			}(conn)
		}
	}()

	publicPort := freePort(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/routes/tcp" {
			http.NotFound(w, r)
			return
		}
		_, targetPort, _ := net.SplitHostPort(targetListener.Addr().String())
		fmt.Fprintf(w, `{"items":[{"id":"t-1","name":"echo","type":"tcp","transportPolicy":"relay_only","targetHost":"127.0.0.1","targetPort":%s,"publicPort":%d,"status":"active","metadata":{"nodeId":"node-1"}}]}`, targetPort, publicPort)
	}))
	defer api.Close()

	service := NewService(api.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = service.Run(ctx)
	}()

	requireEventuallyDial(t, publicPort)

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", publicPort)), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "hello\n")
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "echo:hello\n" {
		t.Fatalf("unexpected relay response: %q", string(buf[:n]))
	}
}

func TestHandleConnectionForwardsPayloadInMemory(t *testing.T) {
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetListener.Close()

	go func() {
		conn, err := targetListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("echo:" + string(buf[:n])))
	}()

	clientSide, relaySide := net.Pipe()
	defer clientSide.Close()
	defer relaySide.Close()

	_, targetPort, err := net.SplitHostPort(targetListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(targetPort, "%d", &port); err != nil {
		t.Fatal(err)
	}

	go handleConnection(relaySide, types.TunnelSpec{
		PublicPort: 20099,
		TargetHost: "127.0.0.1",
		TargetPort: port,
	})

	_, _ = clientSide.Write([]byte("payload"))
	buffer := make([]byte, 1024)
	n, err := clientSide.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "echo:payload" {
		t.Fatalf("unexpected payload relay response: %q", string(buffer[:n]))
	}
}

func requireEventuallyDial(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("listener on port %d did not become ready", port)
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var out int
	if _, err := fmt.Sscanf(port, "%d", &out); err != nil {
		t.Fatal(err)
	}
	return out
}
