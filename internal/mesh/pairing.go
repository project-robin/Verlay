package mesh

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/project-robin/Verlay/src/comm"
	"github.com/project-robin/Verlay/src/crypt"
	"github.com/project-robin/Verlay/src/mnemonicode"
	"github.com/project-robin/Verlay/src/models"
	"github.com/project-robin/Verlay/src/tcp"
	"github.com/schollz/pake/v3"
)

const (
	pairingTTL             = 10 * time.Minute
	pairingProtocolVersion = 4
)

type PairingView struct {
	PairingID       string    `json:"pairingId"`
	Status          string    `json:"status"`
	Direction       string    `json:"direction"`
	PeerDeviceID    string    `json:"peerDeviceId,omitempty"`
	PeerName        string    `json:"peerName,omitempty"`
	PeerPlatform    string    `json:"peerPlatform,omitempty"`
	PeerFingerprint string    `json:"peerFingerprint,omitempty"`
	ExpiresAt       time.Time `json:"expiresAt"`
	ErrorCode       string    `json:"errorCode,omitempty"`
	ErrorMessage    string    `json:"errorMessage,omitempty"`
}

type PairingCreated struct {
	PairingView
	Code string `json:"code"`
}

type pairingMessageConn interface {
	Send([]byte) error
	Receive() ([]byte, error)
	Close()
}

type pairingConnector func(context.Context, string, string) (pairingMessageConn, error)

type pairingSession struct {
	mu      sync.RWMutex
	view    PairingView
	code    string
	creator bool
}

type PairingManager struct {
	store     *Store
	ctx       context.Context
	cancel    context.CancelFunc
	connector pairingConnector
	operation sync.Mutex
	mu        sync.RWMutex
	sessions  map[string]*pairingSession
}

type persistedPairing struct {
	View    PairingView `json:"view"`
	Code    string      `json:"code"`
	Creator bool        `json:"creator"`
}

func NewPairingManager(store *Store) *PairingManager {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &PairingManager{
		store:    store,
		ctx:      ctx,
		cancel:   cancel,
		sessions: make(map[string]*pairingSession),
	}
	manager.connector = manager.connectRelay
	return manager
}

func (m *PairingManager) Close() { m.cancel() }

func (m *PairingManager) Create() (PairingCreated, error) {
	m.operation.Lock()
	defer m.operation.Unlock()
	if active := m.activeSession("", true); active != nil {
		return PairingCreated{PairingView: active.snapshot(), Code: active.code}, nil
	}
	code, err := newPairingCode()
	if err != nil {
		return PairingCreated{}, err
	}
	session, err := m.newSession(code, true)
	if err != nil {
		return PairingCreated{}, err
	}
	go m.run(session)
	return PairingCreated{PairingView: session.snapshot(), Code: code}, nil
}

func (m *PairingManager) Accept(code string) (PairingView, error) {
	m.operation.Lock()
	defer m.operation.Unlock()
	code = normalizePairingCode(code)
	if err := validatePairingCode(code); err != nil {
		return PairingView{}, err
	}
	if active := m.activeSession(code, false); active != nil {
		return active.snapshot(), nil
	}
	session, err := m.newSession(code, false)
	if err != nil {
		return PairingView{}, err
	}
	go m.run(session)
	return session.snapshot(), nil
}

func (m *PairingManager) activeSession(code string, creator bool) *pairingSession {
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, session := range m.sessions {
		view := session.snapshot()
		if session.creator == creator && view.Status == "waiting_for_peer" && view.ExpiresAt.After(now) &&
			(code == "" || session.code == code) {
			return session
		}
	}
	return nil
}

func (m *PairingManager) Resume() error {
	var records []persistedPairing
	path := filepath.Join(m.store.Paths().DataDir, "pairings.json")
	if err := readJSON(path, &records); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read active pairings: %w", err)
	}
	now := time.Now()
	for _, record := range records {
		if record.View.PairingID == "" || record.View.Status != "waiting_for_peer" ||
			!record.View.ExpiresAt.After(now) || validatePairingCode(record.Code) != nil {
			continue
		}
		m.sessions[record.View.PairingID] = &pairingSession{
			view: record.View, code: record.Code, creator: record.Creator,
		}
	}
	if err := m.persist(); err != nil {
		return err
	}
	for _, session := range m.sessions {
		go m.run(session)
	}
	return nil
}

func (m *PairingManager) List() []PairingView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	views := make([]PairingView, 0, len(m.sessions))
	for _, session := range m.sessions {
		views = append(views, session.snapshot())
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].ExpiresAt.Equal(views[j].ExpiresAt) {
			return views[i].PairingID < views[j].PairingID
		}
		return views[i].ExpiresAt.Before(views[j].ExpiresAt)
	})
	return views
}

func (m *PairingManager) newSession(code string, creator bool) (*pairingSession, error) {
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	direction := "accept"
	if creator {
		direction = "create"
	}
	session := &pairingSession{
		view: PairingView{
			PairingID: id,
			Status:    "waiting_for_peer",
			Direction: direction,
			ExpiresAt: time.Now().UTC().Add(pairingTTL),
		},
		code:    code,
		creator: creator,
	}
	m.mu.Lock()
	m.sessions[id] = session
	m.mu.Unlock()
	if err := m.persist(); err != nil {
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
		return nil, err
	}
	_ = m.store.Audit(AuditEvent{Action: "pairing." + direction, Outcome: "started", RequestID: id})
	return session, nil
}

func (m *PairingManager) run(session *pairingSession) {
	ctx, cancel := context.WithDeadline(m.ctx, session.snapshot().ExpiresAt)
	defer cancel()
	room := pairingRoom(session.code)
	connection, err := m.connector(ctx, room, m.store.Config().RelayAddress)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		m.fail(session, "relay_unreachable", err.Error(), true)
		return
	}
	defer connection.Close()
	go func() {
		<-ctx.Done()
		connection.Close()
	}()

	peer, relationshipKey, err := m.exchangeIdentity(connection, session.code, session.creator)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		code := "authentication_failed"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = "pairing_expired"
		}
		m.fail(session, code, err.Error(), false)
		return
	}
	session.update(func(view *PairingView) {
		view.PeerDeviceID = peer.DeviceID
		view.PeerName = peer.Name
		view.PeerPlatform = peer.Platform
		view.PeerFingerprint = publicKeyFingerprint(peer.PublicKey)
	})
	peer.RelationshipKey = base64.RawURLEncoding.EncodeToString(relationshipKey)
	peer.Capabilities = []string{"files.request"}
	peer.ReceivePolicy = m.store.defaultReceivePolicy(peer)
	peer.PairedAt = time.Now().UTC()
	peer.LastSeenAt = peer.PairedAt
	if err := m.store.PutDevice(peer); err != nil {
		m.fail(session, "state_write_failed", "could not save trusted device", true)
		return
	}
	session.update(func(view *PairingView) { view.Status = "paired" })
	_ = m.persist()
	_ = m.store.Audit(AuditEvent{Action: "pairing.complete", Outcome: "success", RequestID: session.snapshot().PairingID, DeviceID: peer.DeviceID})
}

type pairHello struct {
	ProtocolVersion int    `json:"protocolVersion"`
	DeviceID        string `json:"deviceId"`
	Name            string `json:"name"`
	Platform        string `json:"platform"`
	PublicKey       string `json:"publicKey"`
	Contribution    string `json:"contribution"`
}

func (m *PairingManager) exchangeIdentity(connection pairingMessageConn, code string, creator bool) (Device, []byte, error) {
	sessionKey, err := exchangePAKE(connection, []byte(code), creator)
	if err != nil {
		return Device{}, nil, fmt.Errorf("pairing PAKE failed: %w", err)
	}
	channelDigest := sha256.Sum256(append(append([]byte(nil), sessionKey...), []byte("mesh-pair-channel-v1")...))
	channelKey := channelDigest[:]
	contribution := make([]byte, 32)
	if _, err := rand.Read(contribution); err != nil {
		return Device{}, nil, err
	}
	identity := m.store.Identity()
	local := pairHello{
		ProtocolVersion: pairingProtocolVersion,
		DeviceID:        identity.DeviceID,
		Name:            sanitizeDeviceName(identity.Name),
		Platform:        identity.Platform,
		PublicKey:       identity.PublicKey,
		Contribution:    base64.RawURLEncoding.EncodeToString(contribution),
	}
	var remote pairHello
	if creator {
		if err := sendEncryptedJSON(connection, channelKey, local); err != nil {
			return Device{}, nil, err
		}
		if err := receiveEncryptedJSON(connection, channelKey, &remote); err != nil {
			return Device{}, nil, err
		}
	} else {
		if err := receiveEncryptedJSON(connection, channelKey, &remote); err != nil {
			return Device{}, nil, err
		}
		if err := sendEncryptedJSON(connection, channelKey, local); err != nil {
			return Device{}, nil, err
		}
	}
	remoteContribution, err := validatePairHello(remote)
	if err != nil {
		return Device{}, nil, err
	}
	creatorContribution, acceptorContribution := contribution, remoteContribution
	creatorID, acceptorID := local.DeviceID, remote.DeviceID
	if !creator {
		creatorContribution, acceptorContribution = remoteContribution, contribution
		creatorID, acceptorID = remote.DeviceID, local.DeviceID
	}
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte("mesh-relationship-v1"))
	mac.Write(creatorContribution)
	mac.Write(acceptorContribution)
	mac.Write([]byte(creatorID))
	mac.Write([]byte(acceptorID))
	return Device{DeviceID: remote.DeviceID, Name: remote.Name, Platform: remote.Platform, PublicKey: remote.PublicKey}, mac.Sum(nil), nil
}

func exchangePAKE(connection pairingMessageConn, password []byte, creator bool) ([]byte, error) {
	role := 0
	if !creator {
		role = 1
	}
	instance, err := pake.InitCurve(password, role, "p256")
	if err != nil {
		return nil, err
	}
	if creator {
		if err := connection.Send(instance.Bytes()); err != nil {
			return nil, err
		}
		peerBytes, err := receivePairingMessage(connection)
		if err != nil {
			return nil, err
		}
		if err := instance.Update(peerBytes); err != nil {
			return nil, err
		}
	} else {
		peerBytes, err := receivePairingMessage(connection)
		if err != nil {
			return nil, err
		}
		if err := instance.Update(peerBytes); err != nil {
			return nil, err
		}
		if err := connection.Send(instance.Bytes()); err != nil {
			return nil, err
		}
	}
	return instance.SessionKey()
}

func validatePairHello(hello pairHello) ([]byte, error) {
	if hello.ProtocolVersion != pairingProtocolVersion {
		return nil, fmt.Errorf("unsupported peer protocol version %d", hello.ProtocolVersion)
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(hello.PublicKey)
	if err != nil || len(publicKey) != 32 {
		return nil, errors.New("peer sent an invalid identity key")
	}
	digest := sha256.Sum256(publicKey)
	if hello.DeviceID != hex.EncodeToString(digest[:16]) {
		return nil, errors.New("peer device ID does not match its public key")
	}
	contribution, err := base64.RawURLEncoding.DecodeString(hello.Contribution)
	if err != nil || len(contribution) != 32 {
		return nil, errors.New("peer sent an invalid pairing contribution")
	}
	if strings.TrimSpace(hello.Name) == "" || len(hello.Name) > 80 || len(hello.Platform) > 80 {
		return nil, errors.New("peer sent invalid device metadata")
	}
	return contribution, nil
}

func sendEncryptedJSON(connection pairingMessageConn, key []byte, value any) error {
	plaintext, err := json.Marshal(value)
	if err != nil {
		return err
	}
	ciphertext, err := crypt.Encrypt(plaintext, key)
	if err != nil {
		return err
	}
	return connection.Send(ciphertext)
}

func receiveEncryptedJSON(connection pairingMessageConn, key []byte, value any) error {
	ciphertext, err := receivePairingMessage(connection)
	if err != nil {
		return err
	}
	plaintext, err := crypt.Decrypt(ciphertext, key)
	if err != nil {
		return err
	}
	return json.Unmarshal(plaintext, value)
}

// The Croc relay sends a framed single byte with value 1 while a room has only
// one participant. It is relay liveness traffic, not peer protocol data.
func receivePairingMessage(connection pairingMessageConn) ([]byte, error) {
	for {
		message, err := connection.Receive()
		if err != nil {
			return nil, err
		}
		if len(message) == 1 && message[0] == 1 {
			continue
		}
		return message, nil
	}
}

func (m *PairingManager) connectRelay(ctx context.Context, room, relay string) (pairingMessageConn, error) {
	if strings.TrimSpace(relay) == "" {
		return nil, errors.New("relay address is empty")
	}
	type result struct {
		connection *comm.Comm
		err        error
	}
	resultCh := make(chan result, 1)
	go func() {
		password := m.store.Config().RelayPassword
		if password == "" {
			password = models.DEFAULT_PASSPHRASE
		}
		connection, _, _, err := tcp.ConnectToTCPServer(relay, password, room, 15*time.Second)
		resultCh <- result{connection: connection, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			return nil, result.err
		}
		return result.connection, nil
	}
}

func (m *PairingManager) fail(session *pairingSession, code, message string, retryable bool) {
	session.update(func(view *PairingView) {
		view.Status = "failed"
		view.ErrorCode = code
		view.ErrorMessage = sanitizeError(message)
	})
	_ = m.persist()
	view := session.snapshot()
	_ = m.store.Audit(AuditEvent{Action: "pairing.failed", Outcome: "failure", RequestID: view.PairingID, DeviceID: view.PeerDeviceID, Details: map[string]any{"errorCode": code, "retryable": retryable}})
}

func (m *PairingManager) persist() error {
	now := time.Now()
	m.mu.RLock()
	records := make([]persistedPairing, 0, len(m.sessions))
	for _, session := range m.sessions {
		view := session.snapshot()
		if view.Status == "waiting_for_peer" && view.ExpiresAt.After(now) {
			records = append(records, persistedPairing{View: view, Code: session.code, Creator: session.creator})
		}
	}
	m.mu.RUnlock()
	sort.Slice(records, func(i, j int) bool { return records[i].View.ExpiresAt.Before(records[j].View.ExpiresAt) })
	return writeJSONAtomic(filepath.Join(m.store.Paths().DataDir, "pairings.json"), records, 0o600)
}

func (s *pairingSession) snapshot() PairingView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.view
}

func (s *pairingSession) update(update func(*PairingView)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	update(&s.view)
}

func newPairingCode() (string, error) {
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return strings.Join(mnemonicode.EncodeWordList(nil, random), "-"), nil
}

func normalizePairingCode(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}

func validatePairingCode(code string) error {
	parts := strings.Split(code, "-")
	if len(parts) != 4 {
		return &APIError{Code: "invalid_pairing_code", Message: "pairing code must contain four words", Retryable: false}
	}
	for _, part := range parts {
		if len(part) < 1 || len(part) > 24 {
			return &APIError{Code: "invalid_pairing_code", Message: "pairing code is invalid", Retryable: false}
		}
		for _, char := range part {
			if char < 'a' || char > 'z' {
				return &APIError{Code: "invalid_pairing_code", Message: "pairing code is invalid", Retryable: false}
			}
		}
	}
	return nil
}

func pairingRoom(code string) string {
	digest := sha256.Sum256([]byte("mesh-pair-room-v1\x00" + normalizePairingCode(code)))
	return hex.EncodeToString(digest[:])
}

func randomHex(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func publicKeyFingerprint(encoded string) string {
	publicKey, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "invalid"
	}
	digest := sha256.Sum256(publicKey)
	return formatVerificationBytes(digest[:10])
}

func formatVerificationBytes(value []byte) string {
	hexValue := strings.ToUpper(hex.EncodeToString(value))
	parts := make([]string, 0, len(hexValue)/4)
	for i := 0; i < len(hexValue); i += 4 {
		parts = append(parts, hexValue[i:i+4])
	}
	return strings.Join(parts, "-")
}

func sanitizeDeviceName(name string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name))
	if len(name) > 80 {
		name = name[:80]
	}
	if name == "" {
		return "Unnamed device"
	}
	return name
}

func sanitizeError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 240 {
		message = message[:240]
	}
	return message
}
