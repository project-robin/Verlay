package mesh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/project-robin/Verlay/src/comm"
	"github.com/project-robin/Verlay/src/models"
	"github.com/project-robin/Verlay/src/tcp"
)

const maxControlMessageSize = 1 << 20

var errPeerAuthentication = errors.New("peer authentication failed")

type ControlRequest struct {
	ProtocolVersion int             `json:"protocolVersion"`
	RequestID       string          `json:"requestId"`
	Type            string          `json:"type"`
	Payload         json.RawMessage `json:"payload"`
}

type ControlResponse struct {
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *APIError       `json:"error,omitempty"`
}

type ControlHandler func(context.Context, Device, ControlRequest) ControlResponse

type controlConnector func(context.Context, string, string) (net.Conn, error)

type ControlManager struct {
	store     *Store
	handler   ControlHandler
	connector controlConnector
	cert      tls.Certificate
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	listeners map[string]context.CancelFunc
	wg        sync.WaitGroup
}

func NewControlManager(store *Store, handler ControlHandler) (*ControlManager, error) {
	certificate, err := identityCertificate(store.Identity())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := &ControlManager{
		store:     store,
		handler:   handler,
		cert:      certificate,
		ctx:       ctx,
		cancel:    cancel,
		listeners: make(map[string]context.CancelFunc),
	}
	manager.connector = manager.connectRelay
	return manager, nil
}

func (m *ControlManager) Start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		m.reconcileListeners()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.reconcileListeners()
			}
		}
	}()
}

func (m *ControlManager) Close() {
	m.cancel()
	m.mu.Lock()
	for _, cancel := range m.listeners {
		cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *ControlManager) Send(ctx context.Context, peer Device, request ControlRequest) (ControlResponse, error) {
	if request.ProtocolVersion == 0 {
		request.ProtocolVersion = ProtocolVersion
	}
	room, err := controlRoom(peer.RelationshipKey, peer.DeviceID)
	if err != nil {
		return ControlResponse{}, err
	}
	raw, err := m.connector(ctx, room, m.store.Config().RelayAddress)
	if err != nil {
		return ControlResponse{}, &APIError{Code: "device_unavailable", Message: "could not reach the trusted device", Retryable: true, Details: map[string]any{"cause": sanitizeError(err.Error())}}
	}
	defer raw.Close()
	connection := tls.Client(raw, m.tlsConfig(peer, false))
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if err := connection.HandshakeContext(ctx); err != nil {
		if errors.Is(err, errPeerAuthentication) {
			return ControlResponse{}, &APIError{Code: "authentication_failed", Message: "trusted-device authentication failed", Retryable: false, Details: map[string]any{"cause": sanitizeError(err.Error())}}
		}
		return ControlResponse{}, &APIError{Code: "device_unavailable", Message: "trusted-device connection was interrupted", Retryable: true, Details: map[string]any{"cause": sanitizeError(err.Error())}}
	}
	if err := writeControlFrame(connection, request); err != nil {
		return ControlResponse{}, err
	}
	var response ControlResponse
	if err := readControlFrame(connection, &response); err != nil {
		return ControlResponse{}, err
	}
	return response, nil
}

func (m *ControlManager) reconcileListeners() {
	devices := m.store.Devices()
	wanted := make(map[string]Device, len(devices))
	for _, visible := range devices {
		device, ok := m.store.Device(visible.DeviceID)
		if ok {
			wanted[device.DeviceID] = device
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, cancel := range m.listeners {
		if _, ok := wanted[id]; !ok {
			cancel()
			delete(m.listeners, id)
		}
	}
	for id, device := range wanted {
		if _, ok := m.listeners[id]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(m.ctx)
		m.listeners[id] = cancel
		m.wg.Add(1)
		go func(peer Device) {
			defer m.wg.Done()
			m.listenForPeer(ctx, peer)
		}(device)
	}
}

func (m *ControlManager) listenForPeer(ctx context.Context, peer Device) {
	room, err := controlRoom(peer.RelationshipKey, m.store.Identity().DeviceID)
	if err != nil {
		return
	}
	backoff := time.Second
	for ctx.Err() == nil {
		raw, err := m.connector(ctx, room, m.store.Config().RelayAddress)
		if err != nil {
			if !sleepContext(ctx, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		m.handleIncomingConnection(ctx, peer, raw)
	}
}

func (m *ControlManager) handleIncomingConnection(ctx context.Context, peer Device, raw net.Conn) {
	defer raw.Close()
	connection := tls.Server(raw, m.tlsConfig(peer, true))
	handshakeCtx, cancel := context.WithTimeout(ctx, 3*time.Hour)
	defer cancel()
	if err := connection.HandshakeContext(handshakeCtx); err != nil {
		return
	}
	_ = connection.SetDeadline(time.Now().Add(10 * time.Minute))
	var request ControlRequest
	if err := readControlFrame(connection, &request); err != nil {
		return
	}
	if request.ProtocolVersion != ProtocolVersion {
		_ = writeControlFrame(connection, ControlResponse{OK: false, Error: &APIError{Code: "protocol_mismatch", Message: "peer protocol version is unsupported", Retryable: false}})
		return
	}
	response := m.handler(ctx, peer, request)
	_ = writeControlFrame(connection, response)
}

func (m *ControlManager) tlsConfig(peer Device, server bool) *tls.Config {
	config := &tls.Config{
		Certificates:       []tls.Certificate{m.cert},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // Replaced with exact public-key pinning below.
		NextProtos:         []string{"mesh-control/1"},
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) != 1 {
				return fmt.Errorf("%w: peer sent an invalid certificate chain", errPeerAuthentication)
			}
			certificate, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("%w: invalid peer certificate: %v", errPeerAuthentication, err)
			}
			publicKey, ok := certificate.PublicKey.(ed25519.PublicKey)
			if !ok {
				return fmt.Errorf("%w: peer certificate does not use Ed25519", errPeerAuthentication)
			}
			expected, err := base64.RawURLEncoding.DecodeString(peer.PublicKey)
			if err != nil || !bytes.Equal(publicKey, expected) {
				return fmt.Errorf("%w: peer certificate does not match the paired identity", errPeerAuthentication)
			}
			digest := sha256.Sum256(publicKey)
			if hex.EncodeToString(digest[:16]) != peer.DeviceID {
				return fmt.Errorf("%w: peer certificate device ID mismatch", errPeerAuthentication)
			}
			return nil
		},
	}
	if server {
		config.ClientAuth = tls.RequireAnyClientCert
	}
	return config
}

func (m *ControlManager) connectRelay(ctx context.Context, room, relay string) (net.Conn, error) {
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
		select {
		case resultCh <- result{connection: connection, err: err}:
		case <-ctx.Done():
			if connection != nil {
				connection.Close()
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			return nil, result.err
		}
		return newFramedRelayConn(result.connection), nil
	}
}

// framedRelayConn keeps Croc's message framing underneath TLS. The first peer
// in a relay room receives framed one-byte liveness messages until the other
// peer arrives; exposing the raw socket would make those bytes look like a
// malformed TLS record.
type framedRelayConn struct {
	connection *comm.Comm
	readMu     sync.Mutex
	writeMu    sync.Mutex
	pending    []byte
}

func newFramedRelayConn(connection *comm.Comm) net.Conn {
	return &framedRelayConn{connection: connection}
}

func (c *framedRelayConn) Read(destination []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for len(c.pending) == 0 {
		message, err := c.connection.Receive()
		if err != nil {
			return 0, err
		}
		if len(message) == 1 && message[0] == 1 {
			continue
		}
		c.pending = message
	}
	n := copy(destination, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *framedRelayConn) Write(source []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	copySource := append([]byte(nil), source...)
	if err := c.connection.Send(copySource); err != nil {
		return 0, err
	}
	return len(source), nil
}

func (c *framedRelayConn) Close() error {
	c.connection.Close()
	return nil
}

func (c *framedRelayConn) LocalAddr() net.Addr  { return c.connection.Connection().LocalAddr() }
func (c *framedRelayConn) RemoteAddr() net.Addr { return c.connection.Connection().RemoteAddr() }
func (c *framedRelayConn) SetDeadline(deadline time.Time) error {
	return c.connection.Connection().SetDeadline(deadline)
}
func (c *framedRelayConn) SetReadDeadline(deadline time.Time) error {
	return c.connection.Connection().SetReadDeadline(deadline)
}
func (c *framedRelayConn) SetWriteDeadline(deadline time.Time) error {
	return c.connection.Connection().SetWriteDeadline(deadline)
}

func identityCertificate(identity Identity) (tls.Certificate, error) {
	seed, err := base64.RawURLEncoding.DecodeString(identity.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return tls.Certificate{}, errors.New("identity seed is invalid")
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: identity.DeviceID},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, privateKey.Public(), privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey, Leaf: template}, nil
}

func controlRoom(encodedRelationshipKey, receiverID string) (string, error) {
	key, err := base64.RawURLEncoding.DecodeString(encodedRelationshipKey)
	if err != nil || len(key) != 32 {
		return "", errors.New("trusted device relationship key is invalid")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("mesh-control-room-v1\x00"))
	mac.Write([]byte(receiverID))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func writeControlFrame(writer io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(payload) > maxControlMessageSize {
		return errors.New("control message is too large")
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))
	if _, err := writer.Write(header); err != nil {
		return err
	}
	_, err = writer.Write(payload)
	return err
}

func readControlFrame(reader io.Reader, value any) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header)
	if size == 0 || size > maxControlMessageSize {
		return fmt.Errorf("invalid control message size %d", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func sanitizeRelayAddress(relay string) string {
	return strings.TrimSpace(relay)
}
