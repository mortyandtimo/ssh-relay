package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestServeSOCKS5Connect(t *testing.T) {
	targetLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetLn.Close()

	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := targetLn.Accept()
		if err == nil {
			accepted <- struct{}{}
			_ = conn.Close()
		}
	}()

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		_ = serveSOCKS5(ctx, serverConn)
	}()

	port := targetLn.Addr().(*net.TCPAddr).Port
	request := []byte{0x05, 0x01, 0x00, 0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, byte(port >> 8), byte(port)}
	if _, err := clientConn.Write(request); err != nil {
		t.Fatal(err)
	}
	resp := make([]byte, 12)
	if _, err := io.ReadFull(clientConn, resp); err != nil {
		t.Fatal(err)
	}
	if resp[1] != 0x00 {
		t.Fatalf("expected success response, got %d", resp[1])
	}
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("expected target connection")
	}
}

func TestServeSOCKS5RejectsNonConnect(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		_ = serveSOCKS5(ctx, serverConn)
	}()

	reader := bufio.NewReader(clientConn)
	if _, err := clientConn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	negotiation := make([]byte, 2)
	if _, err := io.ReadFull(reader, negotiation); err != nil {
		t.Fatal(err)
	}
	if _, err := clientConn.Write([]byte{0x05, 0x03, 0x00, 0x01, 127, 0, 0, 1, 0, 80}); err != nil {
		t.Fatal(err)
	}
	resp := make([]byte, 10)
	if _, err := io.ReadFull(reader, resp); err != nil {
		t.Fatal(err)
	}
	if resp[1] != 0x07 {
		t.Fatalf("expected command not supported, got %d", resp[1])
	}
}
