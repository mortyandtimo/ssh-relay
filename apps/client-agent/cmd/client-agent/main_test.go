package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/25743/cloud-relay-platform/packages/protocol/types"
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
		_ = serveSOCKS5(ctx, serverConn, "socks-test", nil)
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
		_ = serveSOCKS5(ctx, serverConn, "socks-test", nil)
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

func TestServeUDPRelayRoundTrip(t *testing.T) {
	udpTarget, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udpTarget.Close()
	go func() {
		buf := make([]byte, 1024)
		n, addr, err := udpTarget.ReadFrom(buf)
		if err == nil {
			_, _ = udpTarget.WriteTo(buf[:n], addr)
		}
	}()
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		_ = serveUDPRelay(ctx, serverConn, "udp-test", 12054, "127.0.0.1", udpTarget.LocalAddr().(*net.UDPAddr).Port, 2*time.Second, nil)
	}()
	go func() {
		_ = writeUDPFrame(clientConn, types.UDPDatagramFrame{SessionID: "s1", Payload: []byte("ping")})
	}()
	frame, err := readUDPFrame(clientConn)
	if err != nil {
		t.Fatal(err)
	}
	if string(frame.Payload) != "ping" {
		t.Fatalf("expected udp echo payload ping, got %q", string(frame.Payload))
	}
}

func TestWaitForStartWithKeepaliveStopsAfterStart(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- waitForStartWithKeepalive(ctx, serverConn, 10*time.Millisecond)
	}()

	buf := make([]byte, 1)
	if _, err := io.ReadFull(clientConn, buf); err != nil {
		t.Fatalf("read keepalive: %v", err)
	}
	if buf[0] != types.AgentRelayKeepaliveByte {
		t.Fatalf("expected keepalive byte %d, got %d", types.AgentRelayKeepaliveByte, buf[0])
	}

	if _, err := clientConn.Write([]byte{types.AgentRelayStartByte}); err != nil {
		t.Fatalf("write start byte: %v", err)
	}
	if err := <-waitDone; err != nil {
		t.Fatalf("waitForStartWithKeepalive returned error: %v", err)
	}

	if err := clientConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, err := clientConn.Read(buf)
	if err == nil {
		t.Fatal("expected no extra keepalive bytes after start")
	}
	if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("expected timeout after start, got %v", err)
	}
}

func TestPlanWebWorkerAdjustmentsSpawnsForStandbyDeficit(t *testing.T) {
	now := time.Now()
	workers := []workerPhaseSnapshot{
		{slot: 1, phase: workerPhaseStandby, phaseSince: now.Add(-time.Minute)},
		{slot: 2, phase: workerPhaseStandby, phaseSince: now.Add(-50 * time.Second)},
		{slot: 3, phase: workerPhaseDialing, phaseSince: now.Add(-10 * time.Second)},
		{slot: 4, phase: workerPhaseProxying, phaseSince: now.Add(-20 * time.Second)},
		{slot: 5, phase: workerPhaseProxying, phaseSince: now.Add(-15 * time.Second)},
	}

	plan := planWebWorkerAdjustments(workers, 4, 8, 5*time.Minute, 2, now)
	if plan.spawn != 1 {
		t.Fatalf("spawn = %d, want 1", plan.spawn)
	}
	if len(plan.trimStandbySlots) != 0 {
		t.Fatalf("trimStandbySlots = %v, want none", plan.trimStandbySlots)
	}
	if len(plan.recycleActiveSlots) != 0 {
		t.Fatalf("recycleActiveSlots = %v, want none", plan.recycleActiveSlots)
	}
}

func TestPlanWebWorkerAdjustmentsTrimsOldestStandby(t *testing.T) {
	now := time.Now()
	workers := []workerPhaseSnapshot{
		{slot: 11, phase: workerPhaseStandby, phaseSince: now.Add(-5 * time.Minute)},
		{slot: 12, phase: workerPhaseStandby, phaseSince: now.Add(-4 * time.Minute)},
		{slot: 13, phase: workerPhaseStandby, phaseSince: now.Add(-3 * time.Minute)},
		{slot: 14, phase: workerPhaseStandby, phaseSince: now.Add(-2 * time.Minute)},
	}

	plan := planWebWorkerAdjustments(workers, 2, 8, 5*time.Minute, 2, now)
	if plan.spawn != 0 {
		t.Fatalf("spawn = %d, want 0", plan.spawn)
	}
	if len(plan.trimStandbySlots) != 2 {
		t.Fatalf("trimStandbySlots len = %d, want 2", len(plan.trimStandbySlots))
	}
	if plan.trimStandbySlots[0] != 11 || plan.trimStandbySlots[1] != 12 {
		t.Fatalf("trimStandbySlots = %v, want [11 12]", plan.trimStandbySlots)
	}
}

func TestPlanWebWorkerAdjustmentsRecyclesOldActiveAtBurstCap(t *testing.T) {
	now := time.Now()
	workers := []workerPhaseSnapshot{
		{slot: 21, phase: workerPhaseProxying, phaseSince: now.Add(-12 * time.Minute)},
		{slot: 22, phase: workerPhaseProxying, phaseSince: now.Add(-11 * time.Minute)},
		{slot: 23, phase: workerPhaseProxying, phaseSince: now.Add(-2 * time.Minute)},
		{slot: 24, phase: workerPhaseProxying, phaseSince: now.Add(-90 * time.Second)},
	}

	plan := planWebWorkerAdjustments(workers, 2, 4, 10*time.Minute, 2, now)
	if plan.spawn != 0 {
		t.Fatalf("spawn = %d, want 0 while capped", plan.spawn)
	}
	if len(plan.recycleActiveSlots) != 2 {
		t.Fatalf("recycleActiveSlots len = %d, want 2", len(plan.recycleActiveSlots))
	}
	if plan.recycleActiveSlots[0] != 21 || plan.recycleActiveSlots[1] != 22 {
		t.Fatalf("recycleActiveSlots = %v, want [21 22]", plan.recycleActiveSlots)
	}
}

func TestPlanWebWorkerAdjustmentsDoesNotRecycleFreshActive(t *testing.T) {
	now := time.Now()
	workers := []workerPhaseSnapshot{
		{slot: 31, phase: workerPhaseProxying, phaseSince: now.Add(-2 * time.Minute)},
		{slot: 32, phase: workerPhaseProxying, phaseSince: now.Add(-90 * time.Second)},
		{slot: 33, phase: workerPhaseProxying, phaseSince: now.Add(-80 * time.Second)},
		{slot: 34, phase: workerPhaseProxying, phaseSince: now.Add(-70 * time.Second)},
	}

	plan := planWebWorkerAdjustments(workers, 2, 4, 5*time.Minute, 2, now)
	if len(plan.recycleActiveSlots) != 0 {
		t.Fatalf("recycleActiveSlots = %v, want none", plan.recycleActiveSlots)
	}
}
