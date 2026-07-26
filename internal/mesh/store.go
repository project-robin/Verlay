package mesh

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/project-robin/Verlay/src/models"
)

type Store struct {
	paths    Paths
	mu       sync.RWMutex
	identity Identity
	config   Config
	devices  map[string]Device
}

func OpenStore(paths Paths) (*Store, error) {
	if err := paths.Ensure(); err != nil {
		return nil, err
	}
	s := &Store{paths: paths, devices: make(map[string]Device)}
	if err := s.loadOrCreateIdentity(); err != nil {
		return nil, err
	}
	if err := s.loadConfig(); err != nil {
		return nil, err
	}
	if err := s.loadDevices(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Paths() Paths { return s.paths }

func (s *Store) Identity() Identity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.identity
}

func (s *Store) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *Store) SetRelay(address, password string) (Config, error) {
	address = strings.TrimSpace(address)
	password = strings.TrimSpace(password)
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		return Config{}, &APIError{Code: "invalid_relay", Message: "relay address must include a host and port", Retryable: false}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 || password == "" {
		return Config{}, &APIError{Code: "invalid_relay", Message: "relay port and password must be valid", Retryable: false}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.RelayAddress = address
	s.config.RelayPassword = password
	if err := writeJSONAtomic(s.paths.ConfigFile, s.config, 0o600); err != nil {
		return Config{}, err
	}
	return s.config, nil
}

func (s *Store) Devices() []Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	devices := make([]Device, 0, len(s.devices))
	for _, device := range s.devices {
		copy := device
		copy.RelationshipKey = ""
		devices = append(devices, copy)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	return devices
}

func (s *Store) Device(id string) (Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.devices[id]
	return d, ok
}

func (s *Store) ResolveDevice(query string) (Device, error) {
	query = strings.TrimSpace(query)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if device, ok := s.devices[query]; ok {
		return device, nil
	}
	var match *Device
	for _, device := range s.devices {
		if !strings.EqualFold(device.Name, query) {
			continue
		}
		if match != nil {
			return Device{}, &APIError{Code: "ambiguous_device", Message: "more than one trusted device has that name; use its device ID", Retryable: false}
		}
		copy := device
		match = &copy
	}
	if match == nil {
		return Device{}, &APIError{Code: "device_not_found", Message: "trusted device was not found", Retryable: false}
	}
	return *match, nil
}

func (s *Store) PutDevice(device Device) error {
	if device.DeviceID == "" || device.PublicKey == "" || device.RelationshipKey == "" {
		return errors.New("device identity and relationship key are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices[device.DeviceID] = device
	return s.writeDevicesLocked()
}

func (s *Store) RevokeDevice(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[id]; !ok {
		return false, nil
	}
	delete(s.devices, id)
	return true, s.writeDevicesLocked()
}

func (s *Store) RenameDevice(id, name string) (Device, error) {
	name = sanitizeDeviceName(name)
	if name == "Unnamed device" {
		return Device{}, &APIError{Code: "invalid_device_name", Message: "device name cannot be empty", Retryable: false}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[id]
	if !ok {
		return Device{}, &APIError{Code: "device_not_found", Message: "trusted device was not found", Retryable: false}
	}
	device.Name = name
	s.devices[id] = device
	if err := s.writeDevicesLocked(); err != nil {
		return Device{}, err
	}
	visible := device
	visible.RelationshipKey = ""
	return visible, nil
}

func (s *Store) AuthorizeReceive(id string, maxBytes int64, allowFolders bool) (Device, error) {
	if maxBytes <= 0 {
		return Device{}, &APIError{Code: "invalid_policy", Message: "auto-receive requires a positive maximum byte size", Retryable: false}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[id]
	if !ok {
		return Device{}, &APIError{Code: "device_not_found", Message: "trusted device was not found", Retryable: false}
	}
	device.ReceivePolicy = &ReceivePolicy{
		AutoReceive:  true,
		Inbox:        filepath.Join(s.config.ReceiveDir, receiveInboxName(device)),
		MaxBytes:     maxBytes,
		AllowFolders: allowFolders,
	}
	s.devices[id] = device
	if err := s.writeDevicesLocked(); err != nil {
		return Device{}, err
	}
	visible := device
	visible.RelationshipKey = ""
	return visible, nil
}

func (s *Store) RequireReceiveApproval(id string) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[id]
	if !ok {
		return Device{}, &APIError{Code: "device_not_found", Message: "trusted device was not found", Retryable: false}
	}
	device.ReceivePolicy = &ReceivePolicy{AutoReceive: false}
	s.devices[id] = device
	if err := s.writeDevicesLocked(); err != nil {
		return Device{}, err
	}
	visible := device
	visible.RelationshipKey = ""
	return visible, nil
}

func (s *Store) defaultReceivePolicy(device Device) *ReceivePolicy {
	return &ReceivePolicy{
		AutoReceive:  true,
		Inbox:        filepath.Join(s.config.ReceiveDir, receiveInboxName(device)),
		MaxBytes:     DefaultTrustedTransferLimit,
		AllowFolders: true,
	}
}

func receiveInboxName(device Device) string {
	name := strings.ToLower(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, device.Name))
	name = strings.Trim(strings.Join(strings.FieldsFunc(name, func(r rune) bool { return r == '-' }), "-"), "-")
	if name == "" {
		name = "device"
	}
	if len(name) > 40 {
		name = name[:40]
	}
	id := device.DeviceID
	if len(id) > 8 {
		id = id[:8]
	}
	return name + "-" + id
}

func (s *Store) Audit(event AuditEvent) error {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.paths.AuditFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (s *Store) loadOrCreateIdentity() error {
	externalSeed, err := identitySeedFromEnvironment()
	if err != nil {
		return err
	}
	var identity Identity
	err = readJSON(s.paths.IdentityFile, &identity)
	if err == nil {
		seed := identity.Seed
		if len(externalSeed) > 0 {
			seed = base64.RawURLEncoding.EncodeToString(externalSeed)
		}
		identity.Seed = seed
		nameChanged := false
		if configuredName := sanitizeDeviceName(os.Getenv("VERLAY_DEVICE_NAME")); configuredName != "Unnamed device" && configuredName != identity.Name {
			identity.Name = configuredName
			nameChanged = true
		}
		if err := validateIdentity(identity); err != nil {
			return fmt.Errorf("invalid identity file: %w", err)
		}
		if nameChanged {
			persistedIdentity := identity
			if len(externalSeed) > 0 {
				persistedIdentity.Seed = ""
			}
			if err := writeJSONAtomic(s.paths.IdentityFile, persistedIdentity, 0o600); err != nil {
				return fmt.Errorf("update device name: %w", err)
			}
		}
		s.identity = identity
		return os.Chmod(s.paths.IdentityFile, 0o600)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read identity: %w", err)
	}
	var publicKey ed25519.PublicKey
	var privateKey ed25519.PrivateKey
	if len(externalSeed) > 0 {
		privateKey = ed25519.NewKeyFromSeed(externalSeed)
		publicKey = privateKey.Public().(ed25519.PublicKey)
	} else {
		publicKey, privateKey, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return fmt.Errorf("generate device identity: %w", err)
		}
	}
	hostname := sanitizeDeviceName(os.Getenv("VERLAY_DEVICE_NAME"))
	if hostname == "Unnamed device" {
		hostname, _ = os.Hostname()
	}
	if hostname == "" {
		hostname = "Linux device"
	}
	digest := sha256.Sum256(publicKey)
	identity = Identity{
		DeviceID:  hex.EncodeToString(digest[:16]),
		Name:      hostname,
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		PublicKey: base64.RawURLEncoding.EncodeToString(publicKey),
		Seed:      base64.RawURLEncoding.EncodeToString(privateKey.Seed()),
		CreatedAt: time.Now().UTC(),
	}
	persistedIdentity := identity
	if len(externalSeed) > 0 {
		persistedIdentity.Seed = ""
	}
	if err := writeJSONAtomic(s.paths.IdentityFile, persistedIdentity, 0o600); err != nil {
		return fmt.Errorf("write identity: %w", err)
	}
	s.identity = identity
	return nil
}

func identitySeedFromEnvironment() ([]byte, error) {
	encoded := strings.TrimSpace(os.Getenv("MESH_IDENTITY_SEED"))
	if encoded == "" {
		return nil, nil
	}
	seed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("MESH_IDENTITY_SEED must be a base64url Ed25519 seed")
	}
	return seed, nil
}

func validateIdentity(identity Identity) error {
	publicKey, err := base64.RawURLEncoding.DecodeString(identity.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("invalid public key")
	}
	seed, err := base64.RawURLEncoding.DecodeString(identity.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return errors.New("invalid identity seed")
	}
	derived := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if !derived.Equal(ed25519.PublicKey(publicKey)) {
		return errors.New("public key does not match seed")
	}
	digest := sha256.Sum256(publicKey)
	if identity.DeviceID != hex.EncodeToString(digest[:16]) {
		return errors.New("device ID does not match public key")
	}
	return nil
}

func (s *Store) loadConfig() error {
	var config Config
	err := readJSON(s.paths.ConfigFile, &config)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config: %w", err)
	}
	if config.RelayAddress == "" {
		config.RelayAddress = DefaultRelay
	}
	if config.RelayPassword == "" {
		config.RelayPassword = models.DEFAULT_PASSPHRASE
	}
	if config.ReceiveDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		config.ReceiveDir = filepath.Join(home, "Downloads", "Verlay")
	}
	if errors.Is(err, os.ErrNotExist) {
		if err := writeJSONAtomic(s.paths.ConfigFile, config, 0o600); err != nil {
			return err
		}
	} else if err := os.Chmod(s.paths.ConfigFile, 0o600); err != nil {
		return err
	}
	s.config = config
	return nil
}

func (s *Store) loadDevices() error {
	var devices []Device
	err := readJSON(s.paths.DevicesFile, &devices)
	if errors.Is(err, os.ErrNotExist) {
		return writeJSONAtomic(s.paths.DevicesFile, devices, 0o600)
	}
	if err != nil {
		return fmt.Errorf("read devices: %w", err)
	}
	migrated := false
	for _, device := range devices {
		if device.DeviceID == "" || device.RelationshipKey == "" {
			return errors.New("devices file contains an incomplete trust record")
		}
		if device.ReceivePolicy == nil {
			device.ReceivePolicy = s.defaultReceivePolicy(device)
			migrated = true
		}
		s.devices[device.DeviceID] = device
	}
	if migrated {
		return s.writeDevicesLocked()
	}
	return os.Chmod(s.paths.DevicesFile, 0o600)
}

func (s *Store) writeDevicesLocked() error {
	devices := make([]Device, 0, len(s.devices))
	for _, device := range s.devices {
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].DeviceID < devices[j].DeviceID })
	return writeJSONAtomic(s.paths.DevicesFile, devices, 0o600)
}

func readJSON(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mesh-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}
