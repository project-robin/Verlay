package mesh

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/project-robin/Verlay/src/comm"
)

func TestFramedRelayConnSkipsWaitingRoomKeepalive(t *testing.T) {
	left, right := net.Pipe()
	reader := newFramedRelayConn(comm.New(left))
	writer := comm.New(right)
	done := make(chan error, 1)
	go func() {
		if err := writer.Send([]byte{1}); err != nil {
			done <- err
			return
		}
		done <- writer.Send([]byte("hello"))
	}()
	buffer := make([]byte, 5)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buffer, []byte("hello")) {
		t.Fatalf("read %q", buffer)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestControlChannelPinsPairedIdentity(t *testing.T) {
	storeA, storeB, deviceA, deviceB := pairedTestStores(t)
	payload, _ := json.Marshal(map[string]string{"answer": "pong"})
	managerB, err := NewControlManager(storeB, func(_ context.Context, peer Device, request ControlRequest) ControlResponse {
		if peer.DeviceID != deviceA.DeviceID {
			t.Errorf("handler peer = %s, want %s", peer.DeviceID, deviceA.DeviceID)
		}
		if request.Type != "ping" {
			t.Errorf("request type = %s", request.Type)
		}
		return ControlResponse{OK: true, Payload: payload}
	})
	if err != nil {
		t.Fatal(err)
	}
	managerA, err := NewControlManager(storeA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer managerA.Close()
	defer managerB.Close()
	clientConnection, serverConnection := net.Pipe()
	serverReady := make(chan struct{})
	var serverCalls atomic.Int32
	managerB.connector = func(ctx context.Context, _, _ string) (net.Conn, error) {
		if serverCalls.Add(1) == 1 {
			close(serverReady)
			return serverConnection, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	managerA.connector = func(context.Context, string, string) (net.Conn, error) {
		return clientConnection, nil
	}
	managerB.Start()
	select {
	case <-serverReady:
	case <-time.After(2 * time.Second):
		t.Fatal("control listener did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := managerA.Send(ctx, deviceB, ControlRequest{RequestID: "request-1", Type: "ping", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("control response failed: %#v", response)
	}
}

func TestControlChannelReconcilesUnilateralRevocation(t *testing.T) {
	storeA, storeB, deviceA, deviceB := pairedTestStores(t)
	if revoked, err := storeB.RevokeDevice(deviceA.DeviceID); err != nil || !revoked {
		t.Fatalf("revoke device: revoked=%v err=%v", revoked, err)
	}
	managerB, err := NewControlManager(storeB, func(context.Context, Device, ControlRequest) ControlResponse {
		t.Fatal("revoked peer reached the control handler")
		return ControlResponse{}
	})
	if err != nil {
		t.Fatal(err)
	}
	managerA, err := NewControlManager(storeA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer managerA.Close()
	defer managerB.Close()
	clientConnection, serverConnection := net.Pipe()
	serverReady := make(chan struct{})
	var serverCalls atomic.Int32
	managerB.connector = func(ctx context.Context, _, _ string) (net.Conn, error) {
		if serverCalls.Add(1) == 1 {
			close(serverReady)
			return serverConnection, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	managerA.connector = func(context.Context, string, string) (net.Conn, error) {
		return clientConnection, nil
	}
	managerB.Start()
	select {
	case <-serverReady:
	case <-time.After(2 * time.Second):
		t.Fatal("revoked peer listener did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := managerA.Send(ctx, deviceB, ControlRequest{RequestID: "request-1", Type: "ping", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error == nil || response.Error.Code != "device_revoked" || response.Error.Retryable {
		t.Fatalf("response = %#v, want non-retryable device_revoked", response)
	}
	if _, ok := storeA.Device(deviceB.DeviceID); ok {
		t.Fatal("sender retained a device after authenticated revocation")
	}
}

func TestControlHandshakeDisconnectIsRetryable(t *testing.T) {
	storeA, _, _, deviceB := pairedTestStores(t)
	managerA, err := NewControlManager(storeA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer managerA.Close()
	managerA.connector = func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go server.Close()
		return client, nil
	}

	_, err = managerA.Send(context.Background(), deviceB, ControlRequest{Type: "ping"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want APIError", err)
	}
	if apiErr.Code != "device_unavailable" || !apiErr.Retryable {
		t.Fatalf("error = %#v, want retryable device_unavailable", apiErr)
	}
}

func TestControlHandshakeRejectsWrongIdentity(t *testing.T) {
	storeA, _, _, deviceB := pairedTestStores(t)
	untrustedStore, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	managerA, err := NewControlManager(storeA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer managerA.Close()
	untrustedManager, err := NewControlManager(untrustedStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer untrustedManager.Close()
	managerA.connector = func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			connection := tls.Server(server, &tls.Config{
				Certificates: []tls.Certificate{untrustedManager.cert},
				MinVersion:   tls.VersionTLS13,
				NextProtos:   []string{"mesh-control/1"},
			})
			_ = connection.Handshake()
			_ = connection.Close()
		}()
		return client, nil
	}

	_, err = managerA.Send(context.Background(), deviceB, ControlRequest{Type: "ping"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want APIError", err)
	}
	if apiErr.Code != "authentication_failed" || apiErr.Retryable {
		t.Fatalf("error = %#v, want non-retryable authentication_failed", apiErr)
	}
}

func pairedTestStores(t *testing.T) (*Store, *Store, Device, Device) {
	t.Helper()
	storeA, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	relationship := make([]byte, 32)
	for i := range relationship {
		relationship[i] = byte(i + 1)
	}
	encoded := base64.RawURLEncoding.EncodeToString(relationship)
	identityA := storeA.Identity()
	identityB := storeB.Identity()
	now := time.Now().UTC()
	deviceA := Device{DeviceID: identityA.DeviceID, Name: identityA.Name, Platform: identityA.Platform, PublicKey: identityA.PublicKey, RelationshipKey: encoded, Capabilities: []string{"files.request"}, PairedAt: now}
	deviceB := Device{DeviceID: identityB.DeviceID, Name: identityB.Name, Platform: identityB.Platform, PublicKey: identityB.PublicKey, RelationshipKey: encoded, Capabilities: []string{"files.request"}, PairedAt: now}
	if err := storeA.PutDevice(deviceB); err != nil {
		t.Fatal(err)
	}
	if err := storeB.PutDevice(deviceA); err != nil {
		t.Fatal(err)
	}
	return storeA, storeB, deviceA, deviceB
}
