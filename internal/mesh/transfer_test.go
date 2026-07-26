package mesh

import (
	"context"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type fakeEngine struct {
	content  []byte
	fileName string
	sends    atomic.Int32
	receives atomic.Int32
}

func (e *fakeEngine) Send(context.Context, string, string, string, string, []string) error {
	e.sends.Add(1)
	return nil
}

func (e *fakeEngine) Receive(_ context.Context, _, _, _, destination string) error {
	e.receives.Add(1)
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, e.fileName), e.content, 0o600)
}

func TestApprovedTransferCrossesAuthenticatedControlChannel(t *testing.T) {
	storeA, storeB, _, deviceB := pairedTestStores(t)
	engineA := &fakeEngine{}
	engineB := &fakeEngine{content: []byte("hello mesh"), fileName: "payload.txt"}
	transfersA := NewTransferManager(storeA, engineA)
	transfersB := NewTransferManager(storeB, engineB)
	defer transfersA.Close()
	defer transfersB.Close()
	controlA, err := NewControlManager(storeA, transfersA.HandleControl)
	if err != nil {
		t.Fatal(err)
	}
	controlB, err := NewControlManager(storeB, transfersB.HandleControl)
	if err != nil {
		t.Fatal(err)
	}
	transfersA.SetControl(controlA)
	transfersB.SetControl(controlB)
	defer controlA.Close()
	defer controlB.Close()
	clientConnection, serverConnection := net.Pipe()
	serverReady := make(chan struct{})
	var serverCalls atomic.Int32
	controlB.connector = func(ctx context.Context, _, _ string) (net.Conn, error) {
		if serverCalls.Add(1) == 1 {
			close(serverReady)
			return serverConnection, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	controlA.connector = func(context.Context, string, string) (net.Conn, error) { return clientConnection, nil }
	controlB.Start()
	select {
	case <-serverReady:
	case <-time.After(2 * time.Second):
		t.Fatal("receiver control listener did not start")
	}
	source := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(source, []byte("hello mesh"), 0o600); err != nil {
		t.Fatal(err)
	}
	outgoing, err := transfersA.Send(deviceB.DeviceID, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	incoming := waitForIncomingTransfer(t, transfersB)
	outDir := t.TempDir()
	if _, err := transfersB.Decide(incoming.TransferID, true, outDir); err != nil {
		t.Fatal(err)
	}
	waitForTransferStatus(t, transfersA, outgoing.TransferID, "completed")
	completed := waitForTransferStatus(t, transfersB, incoming.TransferID, "completed")
	received, err := os.ReadFile(completed.SavedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(received) != "hello mesh" {
		t.Fatalf("received content = %q", received)
	}
	if engineA.sends.Load() != 1 || engineB.receives.Load() != 1 {
		t.Fatalf("engine calls: sends=%d receives=%d", engineA.sends.Load(), engineB.receives.Load())
	}
}

func TestUnsafeFileOfferIsRejected(t *testing.T) {
	offer := fileOffer{TransferID: "id", Kind: "file", FileName: "../secret", Size: 1, Secret: "1234567890123456"}
	if err := validateFileOffer(offer, "id"); err == nil {
		t.Fatal("unsafe file name was accepted")
	}
}

func TestSecondActiveTransferForDeviceIsRejected(t *testing.T) {
	store, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewTransferManager(store, &fakeEngine{})
	peer := Device{DeviceID: "phone-1", Name: "Phone"}
	first := fileOffer{
		TransferID: "first",
		Kind:       "file",
		FileName:   "first.txt",
		Size:       1,
		MIMEType:   "text/plain",
		Secret:     "1234567890123456",
	}
	if _, err := manager.addIncoming(peer, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.TransferID = "second"
	second.FileName = "second.txt"
	if _, err := manager.addIncoming(peer, second); err == nil || apiErrorFrom(err).Code != "device_busy" {
		t.Fatalf("second active transfer error = %v, want device_busy", err)
	}
}

func TestTransferListIsStable(t *testing.T) {
	store, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewTransferManager(store, &fakeEngine{})
	peerA := Device{DeviceID: "a", Name: "A"}
	peerB := Device{DeviceID: "b", Name: "B"}
	for index, item := range []struct {
		peer Device
		id   string
	}{{peerB, "later"}, {peerA, "earlier"}} {
		offer := fileOffer{TransferID: item.id, Kind: "file", FileName: item.id + ".txt", Size: 1, Secret: "1234567890123456"}
		session, err := manager.addIncoming(item.peer, offer)
		if err != nil {
			t.Fatal(err)
		}
		session.update(func(view *TransferView) {
			view.CreatedAt = time.Unix(int64(20-index*10), 0)
		})
	}
	listed := manager.List()
	if len(listed) != 2 || listed[0].TransferID != "earlier" || listed[1].TransferID != "later" {
		t.Fatalf("unstable transfer order: %#v", listed)
	}
}

func TestFolderInspectionRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := inspectTransferSource(root, info, nil); err == nil || apiErrorFrom(err).Code != "unsafe_folder" {
		t.Fatalf("folder symlink error = %v, want unsafe_folder", err)
	}
}

func TestFolderInspectionAndPublishingPreserveTree(t *testing.T) {
	staging := t.TempDir()
	root := filepath.Join(staging, "project")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	kind, size, count, err := inspectTransferSource(root, info, nil)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "folder" || size != int64(len("package main\n")) || count != 1 {
		t.Fatalf("folder inspection = kind:%s size:%d count:%d", kind, size, count)
	}
	out := t.TempDir()
	destination, err := publishReceivedPath(root, out, "project", "folder")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "src", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "package main\n" {
		t.Fatalf("published content = %q", content)
	}
	if info, err := os.Stat(filepath.Join(destination, "empty")); err != nil || !info.IsDir() {
		t.Fatal("empty folder was not preserved")
	}
}

func TestFolderInspectionAppliesSafeExactExclusions(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "large.bin"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	_, size, count, err := inspectTransferSource(root, info, []string{"node_modules"})
	if err != nil {
		t.Fatal(err)
	}
	if size != 4 || count != 1 {
		t.Fatalf("excluded inspection = size:%d count:%d", size, count)
	}
	if _, err := validateExclusions([]string{"../outside"}); err == nil {
		t.Fatal("unsafe exclusion was accepted")
	}
}

func TestSourceFingerprintIgnoresExcludedSubtrees(t *testing.T) {
	root := t.TempDir()
	excluded := filepath.Join(root, "node_modules")
	if err := os.MkdirAll(excluded, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/not/read", filepath.Join(excluded, "dependency")); err != nil {
		t.Fatal(err)
	}
	before, err := fingerprintSource(root, []string{"node_modules"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(excluded, "dependency")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(excluded, "changed"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := fingerprintSource(root, []string{"node_modules"})
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("excluded subtree changed the queued source fingerprint")
	}
}

func TestReceivePolicyAutomaticallyApprovesOnlyWithinScope(t *testing.T) {
	store, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewTransferManager(store, &fakeEngine{})
	peer := Device{
		DeviceID: "peer",
		ReceivePolicy: &ReceivePolicy{
			AutoReceive:  true,
			Inbox:        filepath.Join(t.TempDir(), "inbox"),
			MaxBytes:     10,
			AllowFolders: false,
		},
	}
	decision, automatic, err := manager.automaticDecision(peer, fileOffer{TransferID: "one", Kind: "file", Size: 10})
	if err != nil || !automatic || !decision.approve {
		t.Fatalf("file was not auto-approved: decision=%#v automatic=%v err=%v", decision, automatic, err)
	}
	if _, automatic, err := manager.automaticDecision(peer, fileOffer{TransferID: "two", Kind: "file", Size: 11}); err != nil || automatic {
		t.Fatalf("oversized file auto-approved: automatic=%v err=%v", automatic, err)
	}
	if _, automatic, err := manager.automaticDecision(peer, fileOffer{TransferID: "three", Kind: "folder", Size: 1}); err != nil || automatic {
		t.Fatalf("folder auto-approved without permission: automatic=%v err=%v", automatic, err)
	}
}

func TestTransferQueuePersistsAcrossManagerRestart(t *testing.T) {
	storeA, _, _, deviceB := pairedTestStores(t)
	source := filepath.Join(t.TempDir(), "queued.txt")
	if err := os.WriteFile(source, []byte("queued"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := NewTransferManager(storeA, &fakeEngine{})
	queued, err := first.Send(deviceB.DeviceID, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != "queued" {
		t.Fatalf("new transfer status = %s, want queued", queued.Status)
	}
	first.Close()

	second := NewTransferManager(storeA, &fakeEngine{})
	if err := second.Load(); err != nil {
		t.Fatal(err)
	}
	recovered, ok := second.Get(queued.TransferID)
	if !ok || recovered.Status != "queued" {
		t.Fatalf("recovered transfer = %#v, found=%v", recovered, ok)
	}
}

func TestTransferCapacityIsTwoGloballyAndOnePerPeer(t *testing.T) {
	store, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewTransferManager(store, &fakeEngine{})
	defer manager.Close()
	if !manager.acquirePeerSlot(context.Background(), context.Background(), "a") {
		t.Fatal("first slot was unavailable")
	}
	if !manager.acquirePeerSlot(context.Background(), context.Background(), "b") {
		t.Fatal("second slot was unavailable")
	}
	blocked, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if manager.acquirePeerSlot(blocked, context.Background(), "c") {
		t.Fatal("third concurrent slot was granted")
	}
	manager.releaseSlot("a")
	if !manager.acquirePeerSlot(context.Background(), context.Background(), "c") {
		t.Fatal("released slot was not reused")
	}
	manager.releaseSlot("b")
	manager.releaseSlot("c")

	if !manager.acquirePeerSlot(context.Background(), context.Background(), "same") {
		t.Fatal("peer slot was unavailable")
	}
	blockedPeer, cancelPeer := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelPeer()
	if manager.acquirePeerSlot(blockedPeer, context.Background(), "same") {
		t.Fatal("same peer received two concurrent slots")
	}
	manager.releaseSlot("same")
}

func TestDerivedTransferSecretIsStableAndPerTransfer(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	first, err := deriveTransferSecret(key, "transfer-one")
	if err != nil {
		t.Fatal(err)
	}
	again, err := deriveTransferSecret(key, "transfer-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := deriveTransferSecret(key, "transfer-two")
	if err != nil {
		t.Fatal(err)
	}
	if first != again || first == second || len(first) < 16 {
		t.Fatalf("unexpected derived secrets: first=%q again=%q second=%q", first, again, second)
	}
}

func waitForIncomingTransfer(t *testing.T, manager *TransferManager) TransferView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, transfer := range manager.List() {
			if transfer.Direction == "incoming" && transfer.Status == "pending_approval" {
				return transfer
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("incoming transfer did not become pending")
	return TransferView{}
}

func waitForTransferStatus(t *testing.T, manager *TransferManager, id, wanted string) TransferView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, ok := manager.Get(id)
		if ok && view.Status == wanted {
			return view
		}
		if ok && view.Status == "failed" {
			t.Fatalf("transfer failed: %s %s", view.ErrorCode, view.Message)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("transfer %s did not reach %s", id, wanted)
	return TransferView{}
}
