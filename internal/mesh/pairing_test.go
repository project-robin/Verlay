package mesh

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/project-robin/Verlay/src/comm"
)

func TestPairingCreatesMatchingPersistentTrust(t *testing.T) {
	storeA, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	managerA := NewPairingManager(storeA)
	managerB := NewPairingManager(storeB)
	defer managerA.Close()
	defer managerB.Close()
	connA, connB := net.Pipe()
	managerA.connector = func(context.Context, string, string) (pairingMessageConn, error) {
		return comm.New(connA), nil
	}
	managerB.connector = func(context.Context, string, string) (pairingMessageConn, error) {
		return comm.New(connB), nil
	}

	created, err := managerA.Create()
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := managerB.Accept(created.Code)
	if err != nil {
		t.Fatal(err)
	}
	waitForPairingStatus(t, managerA, created.PairingID, "paired")
	waitForPairingStatus(t, managerB, accepted.PairingID, "paired")

	trustedB, ok := storeA.Device(storeB.Identity().DeviceID)
	if !ok {
		t.Fatal("creator did not persist acceptor trust")
	}
	trustedA, ok := storeB.Device(storeA.Identity().DeviceID)
	if !ok {
		t.Fatal("acceptor did not persist creator trust")
	}
	if trustedA.RelationshipKey == "" || trustedA.RelationshipKey != trustedB.RelationshipKey {
		t.Fatal("paired devices derived different relationship keys")
	}
	if len(trustedA.Capabilities) != 1 || trustedA.Capabilities[0] != "files.request" {
		t.Fatalf("unexpected default capabilities: %v", trustedA.Capabilities)
	}
}

func TestPairingRequiresFourWordCode(t *testing.T) {
	store, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewPairingManager(store)
	defer manager.Close()
	if _, err := manager.Accept("too-short"); err == nil {
		t.Fatal("invalid pairing code was accepted")
	}
}

func waitForPairingStatus(t *testing.T, manager *PairingManager, id, wanted string) PairingView {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, view := range manager.List() {
			if view.PairingID != id {
				continue
			}
			if view.Status == wanted {
				return view
			}
			if view.Status == "failed" {
				t.Fatalf("pairing failed: %s: %s", view.ErrorCode, view.ErrorMessage)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pairing %s did not reach %s", id, wanted)
	return PairingView{}
}
