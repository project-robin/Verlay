package mesh

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDaemonStatusDoctorAndDevices(t *testing.T) {
	paths := testPaths(t)
	store, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	daemon, err := NewDaemon(store, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemon stopped with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	waitForSocket(t, paths.SocketFile)

	client := NewClient(paths.SocketFile, 2*time.Second)
	status, err := client.Call(context.Background(), http.MethodGet, "/v1/status", nil)
	if err != nil || !status.OK {
		t.Fatalf("status failed: envelope=%#v err=%v", status, err)
	}
	doctor, err := client.Call(context.Background(), http.MethodGet, "/v1/doctor", nil)
	if err != nil || !doctor.OK {
		t.Fatalf("doctor failed: envelope=%#v err=%v", doctor, err)
	}
	devices, err := client.Call(context.Background(), http.MethodGet, "/v1/devices", nil)
	if err != nil || !devices.OK {
		t.Fatalf("devices failed: envelope=%#v err=%v", devices, err)
	}
}

func TestSecondDaemonCannotTakeLock(t *testing.T) {
	paths := testPaths(t)
	store, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewDaemon(store, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.acquireLock(); err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewDaemon(store, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := second.acquireLock(); err == nil {
		t.Fatal("second daemon unexpectedly acquired the lock")
	}
}

func TestReceivePolicyAndRelayAPIsDoNotExposeSecrets(t *testing.T) {
	store, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	device := Device{
		DeviceID:        "editor-two",
		Name:            "Editor Two",
		PublicKey:       base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
		RelationshipKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
		Capabilities:    []string{"files.request"},
	}
	if err := store.PutDevice(device); err != nil {
		t.Fatal(err)
	}
	daemon := &Daemon{store: store}

	policy := callHandler(t, daemon.routes(), http.MethodPost, "/v1/devices/Editor%20Two/receive-policy",
		map[string]any{"autoReceive": true, "maxBytes": 4096, "allowFolders": true})
	if !policy.OK {
		t.Fatalf("receive policy failed: %#v", policy.Error)
	}
	stored, ok := store.Device(device.DeviceID)
	if !ok || stored.ReceivePolicy == nil || stored.ReceivePolicy.MaxBytes != 4096 || !stored.ReceivePolicy.AllowFolders {
		t.Fatalf("receive policy was not persisted: %#v", stored.ReceivePolicy)
	}

	relayPassword := "private-relay-password"
	updated := callHandler(t, daemon.routes(), http.MethodPost, "/v1/relay",
		map[string]any{"address": "relay.example.test:9009", "password": relayPassword})
	if !updated.OK {
		t.Fatalf("relay update failed: %#v", updated.Error)
	}
	shown := callHandler(t, daemon.routes(), http.MethodGet, "/v1/relay", nil)
	encoded, err := json.Marshal(shown)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), relayPassword) {
		t.Fatal("relay API exposed its password")
	}
}

func callHandler(t *testing.T, handler http.Handler, method, path string, body any) Envelope {
	t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var envelope Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode handler response: %v; body=%s", err, recorder.Body.String())
	}
	return envelope
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		client := NewClient(path, 100*time.Millisecond)
		if _, err := client.Call(context.Background(), http.MethodGet, "/v1/status", nil); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("daemon socket %s did not become ready", path)
}
