package mesh

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	return Paths{
		ConfigDir:     filepath.Join(root, "config"),
		DataDir:       filepath.Join(root, "data"),
		RuntimeDir:    filepath.Join(root, "run"),
		ConfigFile:    filepath.Join(root, "config", "config.json"),
		IdentityFile:  filepath.Join(root, "data", "identity.json"),
		DevicesFile:   filepath.Join(root, "data", "devices.json"),
		TransfersFile: filepath.Join(root, "data", "transfers.json"),
		AuditFile:     filepath.Join(root, "data", "audit.jsonl"),
		SocketFile:    filepath.Join(root, "run", "mesh.sock"),
		LockFile:      filepath.Join(root, "run", "mesh.lock"),
	}
}

func TestStorePersistsProtectedIdentity(t *testing.T) {
	paths := testPaths(t)
	store, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	first := store.Identity()
	if first.DeviceID == "" || first.PublicKey == "" || first.Seed == "" {
		t.Fatalf("incomplete identity: %#v", first)
	}
	if _, err := base64.RawURLEncoding.DecodeString(first.PublicKey); err != nil {
		t.Fatalf("invalid public key: %v", err)
	}
	assertMode(t, paths.DataDir, 0o700)
	assertMode(t, paths.IdentityFile, 0o600)
	assertMode(t, paths.ConfigFile, 0o600)
	assertMode(t, paths.DevicesFile, 0o600)

	reopened, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Identity().DeviceID != first.DeviceID {
		t.Fatalf("identity changed after reopen: %s != %s", reopened.Identity().DeviceID, first.DeviceID)
	}
}

func TestResolveOnlyDeviceWithoutAName(t *testing.T) {
	store, err := OpenStore(testPaths(t))
	if err != nil {
		t.Fatal(err)
	}
	device := Device{
		DeviceID:        "only-device",
		Name:            "Phone",
		PublicKey:       base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
		RelationshipKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
	}
	if err := store.PutDevice(device); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolveDevice("")
	if err != nil || resolved.DeviceID != device.DeviceID {
		t.Fatalf("resolved device = %#v, err=%v", resolved, err)
	}
}

func TestExternalIdentitySeedIsNotPersisted(t *testing.T) {
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i + 3)
	}
	t.Setenv("MESH_IDENTITY_SEED", base64.RawURLEncoding.EncodeToString(seed))
	paths := testPaths(t)
	store, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	if store.Identity().Seed == "" {
		t.Fatal("in-memory identity omitted externally supplied seed")
	}
	b, err := os.ReadFile(paths.IdentityFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(base64.RawURLEncoding.EncodeToString(seed))) {
		t.Fatal("externally managed identity seed was written to disk")
	}
	reopened, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Identity().DeviceID != store.Identity().DeviceID {
		t.Fatal("external identity changed after reopening")
	}
}

func TestConfiguredDeviceNamePersistsWithoutChangingIdentity(t *testing.T) {
	paths := testPaths(t)
	t.Setenv("VERLAY_DEVICE_NAME", "Map's Phone")
	store, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	first := store.Identity()
	if first.Name != "Map's Phone" {
		t.Fatalf("configured device name was not used: %q", first.Name)
	}

	t.Setenv("VERLAY_DEVICE_NAME", "Editing Phone")
	reopened, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Identity().Name != "Editing Phone" {
		t.Fatalf("configured device name was not updated: %q", reopened.Identity().Name)
	}
	if reopened.Identity().DeviceID != first.DeviceID {
		t.Fatal("renaming the device changed its stable identity")
	}
}

func TestStoreRedactsRelationshipKeyAndRevokes(t *testing.T) {
	paths := testPaths(t)
	store, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	relationshipKey := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	device := Device{
		DeviceID:        "phone-1",
		Name:            "Android phone",
		Platform:        "android/arm64",
		PublicKey:       "public",
		RelationshipKey: relationshipKey,
		Capabilities:    []string{"files.request"},
		PairedAt:        time.Now().UTC(),
	}
	if err := store.PutDevice(device); err != nil {
		t.Fatal(err)
	}
	listed := store.Devices()
	if len(listed) != 1 || listed[0].RelationshipKey != "" {
		t.Fatalf("relationship key leaked: %#v", listed)
	}
	stored, ok := store.Device(device.DeviceID)
	if !ok || stored.RelationshipKey != relationshipKey {
		t.Fatalf("internal trust record missing: %#v", stored)
	}
	renamed, err := store.RenameDevice(device.DeviceID, "My phone")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "My phone" || renamed.RelationshipKey != "" {
		t.Fatalf("unexpected renamed device: %#v", renamed)
	}
	stored, ok = store.Device(device.DeviceID)
	if !ok || stored.Name != "My phone" || stored.RelationshipKey != relationshipKey {
		t.Fatalf("rename did not preserve trust record: %#v", stored)
	}
	revoked, err := store.RevokeDevice(device.DeviceID)
	if err != nil || !revoked {
		t.Fatalf("revoke failed: revoked=%v err=%v", revoked, err)
	}
	if len(store.Devices()) != 0 {
		t.Fatal("revoked device remains visible")
	}
	revocations := store.RevokedDevices()
	if len(revocations) != 1 || revocations[0].RelationshipKey != "" || revocations[0].RevocationRoom == "" {
		t.Fatalf("invalid revocation tombstone: %#v", revocations)
	}
	reopened, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Devices()) != 0 || len(reopened.RevokedDevices()) != 1 {
		t.Fatal("revocation tombstone did not persist")
	}
	if err := reopened.PutDevice(device); err != nil {
		t.Fatal(err)
	}
	if len(reopened.Devices()) != 1 || len(reopened.RevokedDevices()) != 0 {
		t.Fatal("re-pairing did not replace the revocation tombstone")
	}
}

func TestReceiveAuthorizationIsExplicitAndPersists(t *testing.T) {
	paths := testPaths(t)
	store, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	device := Device{
		DeviceID:        "abcdef0123456789",
		Name:            "Editor One",
		PublicKey:       "public",
		RelationshipKey: "secret",
		Capabilities:    []string{"files.request"},
	}
	if err := store.PutDevice(device); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthorizeReceive(device.DeviceID, 0, true); err == nil {
		t.Fatal("auto-receive accepted an unlimited policy")
	}
	authorized, err := store.AuthorizeReceive(device.DeviceID, 1024, true)
	if err != nil {
		t.Fatal(err)
	}
	if authorized.ReceivePolicy == nil || !authorized.ReceivePolicy.AutoReceive ||
		!authorized.ReceivePolicy.AllowFolders || authorized.ReceivePolicy.MaxBytes != 1024 {
		t.Fatalf("unexpected receive policy: %#v", authorized.ReceivePolicy)
	}
	if filepath.Dir(authorized.ReceivePolicy.Inbox) != store.Config().ReceiveDir {
		t.Fatalf("inbox escaped receive root: %s", authorized.ReceivePolicy.Inbox)
	}
	reopened, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := reopened.Device(device.DeviceID)
	if !ok || stored.ReceivePolicy == nil || stored.ReceivePolicy.Inbox != authorized.ReceivePolicy.Inbox {
		t.Fatalf("receive policy did not persist: %#v", stored.ReceivePolicy)
	}
	required, err := reopened.RequireReceiveApproval(device.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if required.ReceivePolicy == nil || required.ReceivePolicy.AutoReceive {
		t.Fatalf("manual approval was not persisted: %#v", required.ReceivePolicy)
	}
	manual, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ = manual.Device(device.DeviceID)
	if stored.ReceivePolicy == nil || stored.ReceivePolicy.AutoReceive {
		t.Fatalf("manual approval did not survive restart: %#v", stored.ReceivePolicy)
	}
}

func TestLegacyPairedDeviceMigratesToTrustedInbox(t *testing.T) {
	paths := testPaths(t)
	store, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	device := Device{DeviceID: "legacy", PublicKey: "public", RelationshipKey: "secret"}
	if err := store.PutDevice(device); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	migrated, _ := reopened.Device(device.DeviceID)
	if migrated.ReceivePolicy == nil || !migrated.ReceivePolicy.AutoReceive {
		t.Fatalf("legacy device was not migrated: %#v", migrated.ReceivePolicy)
	}
}

func TestRelayConfigurationPersistsProtectedPassword(t *testing.T) {
	paths := testPaths(t)
	store, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.SetRelay("relay.example.test:9009", "private-password")
	if err != nil {
		t.Fatal(err)
	}
	if updated.RelayAddress != "relay.example.test:9009" || updated.RelayPassword != "private-password" {
		t.Fatalf("unexpected relay config: %#v", updated)
	}
	reopened, err := OpenStore(paths)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Config() != updated {
		t.Fatalf("relay config did not persist: %#v", reopened.Config())
	}
	assertMode(t, paths.ConfigFile, 0o600)
}

func assertMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual := info.Mode().Perm(); actual != expected {
		t.Fatalf("%s mode = %04o, want %04o", path, actual, expected)
	}
}
