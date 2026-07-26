package mesh

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type TransferView struct {
	TransferID    string    `json:"transferId"`
	Direction     string    `json:"direction"`
	DeviceID      string    `json:"deviceId"`
	DeviceName    string    `json:"deviceName"`
	Kind          string    `json:"kind"`
	FileName      string    `json:"fileName"`
	Size          int64     `json:"size"`
	FileCount     int       `json:"fileCount,omitempty"`
	MIMEType      string    `json:"mimeType"`
	Status        string    `json:"status"`
	SavedPath     string    `json:"savedPath,omitempty"`
	ErrorCode     string    `json:"errorCode,omitempty"`
	Message       string    `json:"message,omitempty"`
	Attempts      int       `json:"attempts,omitempty"`
	NextAttemptAt time.Time `json:"nextAttemptAt,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (t TransferView) Terminal() bool {
	return t.Status == "completed" || t.Status == "failed" || t.Status == "rejected" || t.Status == "cancelled"
}

type fileOffer struct {
	TransferID string `json:"transferId"`
	Kind       string `json:"kind,omitempty"`
	FileName   string `json:"fileName"`
	Size       int64  `json:"size"`
	FileCount  int    `json:"fileCount,omitempty"`
	MIMEType   string `json:"mimeType"`
	Secret     string `json:"secret"`
}

type fileOfferResponse struct {
	Approved bool `json:"approved"`
}

type transferDecision struct {
	approve bool
	outDir  string
}

type transferSession struct {
	mu                sync.RWMutex
	view              TransferView
	sourcePath        string
	sourceFingerprint string
	exclusions        []string
	secret            string
	decision          chan transferDecision
	ctx               context.Context
	cancel            context.CancelFunc
}

type TransferManager struct {
	store         *Store
	engine        Engine
	control       *ControlManager
	mu            sync.RWMutex
	items         map[string]*transferSession
	persistMu     sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	wake          chan struct{}
	slots         chan struct{}
	activePeers   map[string]bool
	schedulerOnce sync.Once
	workers       sync.WaitGroup
}

func NewTransferManager(store *Store, engine Engine) *TransferManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &TransferManager{
		store: store, engine: engine, items: make(map[string]*transferSession),
		ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1),
		slots: make(chan struct{}, 2), activePeers: make(map[string]bool),
	}
}

func (m *TransferManager) SetControl(control *ControlManager) {
	m.control = control
	m.schedulerOnce.Do(func() {
		m.workers.Add(1)
		go func() {
			defer m.workers.Done()
			m.scheduleLoop()
		}()
	})
	m.signal()
}

func (m *TransferManager) Close() {
	m.cancel()
	m.mu.RLock()
	for _, session := range m.items {
		if !session.snapshot().Terminal() {
			session.cancel()
		}
	}
	m.mu.RUnlock()
	m.workers.Wait()
}

type persistedTransfer struct {
	View              TransferView `json:"view"`
	SourcePath        string       `json:"sourcePath,omitempty"`
	SourceFingerprint string       `json:"sourceFingerprint,omitempty"`
	Exclusions        []string     `json:"exclusions,omitempty"`
}

func (m *TransferManager) Load() error {
	var records []persistedTransfer
	if err := readJSON(m.store.Paths().TransfersFile, &records); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read transfers: %w", err)
	}
	for _, record := range records {
		view := record.View
		if view.TransferID == "" {
			continue
		}
		if view.Direction == "outgoing" && !view.Terminal() {
			view.Status = "queued"
			view.Message = "Recovered after daemon restart"
			view.NextAttemptAt = time.Time{}
		} else if view.Direction == "incoming" && !view.Terminal() {
			view.Status = "failed"
			view.ErrorCode = "daemon_restarted"
			view.Message = "Sender will retry the interrupted transfer"
		}
		ctx, cancel := context.WithCancel(context.Background())
		session := &transferSession{
			view: view, sourcePath: record.SourcePath, sourceFingerprint: record.SourceFingerprint,
			exclusions: append([]string(nil), record.Exclusions...),
			decision:   make(chan transferDecision, 1), ctx: ctx, cancel: cancel,
		}
		m.items[view.TransferID] = session
	}
	return nil
}

func (m *TransferManager) persist() error {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.mu.RLock()
	records := make([]persistedTransfer, 0, len(m.items))
	for _, session := range m.items {
		view := session.snapshot()
		records = append(records, persistedTransfer{
			View: view, SourcePath: session.sourcePath, SourceFingerprint: session.sourceFingerprint,
			Exclusions: append([]string(nil), session.exclusions...),
		})
	}
	m.mu.RUnlock()
	sort.Slice(records, func(i, j int) bool { return records[i].View.CreatedAt.Before(records[j].View.CreatedAt) })
	if len(records) > 200 {
		records = records[len(records)-200:]
	}
	return writeJSONAtomic(m.store.Paths().TransfersFile, records, 0o600)
}

func (m *TransferManager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *TransferManager) scheduleLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
		m.dispatch()
	}
}

func (m *TransferManager) dispatch() {
	for {
		now := time.Now()
		m.mu.Lock()
		var candidate *transferSession
		for _, session := range m.items {
			view := session.snapshot()
			if view.Direction != "outgoing" || view.Status != "queued" || view.NextAttemptAt.After(now) || m.activePeers[view.DeviceID] {
				continue
			}
			if candidate == nil || view.CreatedAt.Before(candidate.snapshot().CreatedAt) {
				candidate = session
			}
		}
		if candidate == nil {
			m.mu.Unlock()
			return
		}
		select {
		case m.slots <- struct{}{}:
		default:
			m.mu.Unlock()
			return
		}
		view := candidate.snapshot()
		m.activePeers[view.DeviceID] = true
		candidate.setStatus("connecting", "Connecting to trusted device")
		m.mu.Unlock()
		_ = m.persist()
		m.workers.Add(1)
		go func() {
			defer m.workers.Done()
			m.runScheduled(candidate)
		}()
	}
}

func (m *TransferManager) runScheduled(session *transferSession) {
	view := session.snapshot()
	defer m.releaseSlot(view.DeviceID)
	device, err := m.store.ResolveDevice(view.DeviceID)
	if err != nil {
		session.fail(apiErrorFrom(err).Code, apiErrorFrom(err).Message)
		_ = m.persist()
		return
	}
	fingerprint, err := fingerprintSource(session.sourcePath, session.exclusions)
	if err != nil || fingerprint != session.sourceFingerprint {
		session.fail("source_changed", "Source was deleted or changed after it was queued")
		_ = m.persist()
		return
	}
	secret, err := deriveTransferSecret(device.RelationshipKey, view.TransferID)
	if err != nil {
		session.fail("security_failure", "Could not derive the transfer secret")
		_ = m.persist()
		return
	}
	session.secret = secret
	m.runOutgoing(session, device)
	_ = m.persist()
}

func (m *TransferManager) releaseSlot(deviceID string) {
	m.mu.Lock()
	delete(m.activePeers, deviceID)
	m.mu.Unlock()
	<-m.slots
	m.signal()
}

func (m *TransferManager) acquirePeerSlot(controlCtx, transferCtx context.Context, deviceID string) bool {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		if !m.activePeers[deviceID] {
			select {
			case m.slots <- struct{}{}:
				m.activePeers[deviceID] = true
				m.mu.Unlock()
				return true
			default:
			}
		}
		m.mu.Unlock()
		select {
		case <-m.ctx.Done():
			return false
		case <-controlCtx.Done():
			return false
		case <-transferCtx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (m *TransferManager) queueRetry(session *transferSession, code, message string) {
	session.update(func(view *TransferView) {
		view.Status = "queued"
		view.ErrorCode = code
		view.Message = message
		view.Attempts++
		delay := time.Second << min(view.Attempts-1, 6)
		view.NextAttemptAt = time.Now().Add(delay)
		view.UpdatedAt = time.Now().UTC()
	})
	_ = m.store.Audit(AuditEvent{Action: "transfer.retry", Outcome: "queued", RequestID: session.snapshot().TransferID, DeviceID: session.snapshot().DeviceID})
}

func (m *TransferManager) Send(deviceQuery, sourcePath string, exclusions []string) (TransferView, error) {
	device, err := m.resolveDevice(deviceQuery)
	if err != nil {
		return TransferView{}, err
	}
	sourcePath, err = filepath.Abs(filepath.Clean(sourcePath))
	if err != nil {
		return TransferView{}, err
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return TransferView{}, &APIError{Code: "invalid_file", Message: "source file does not exist", Retryable: false}
	}
	exclusions, err = validateExclusions(exclusions)
	if err != nil {
		return TransferView{}, err
	}
	kind, size, fileCount, err := inspectTransferSource(sourcePath, info, exclusions)
	if err != nil {
		return TransferView{}, err
	}
	if kind == "file" && len(exclusions) > 0 {
		return TransferView{}, &APIError{Code: "invalid_exclusion", Message: "exclusions apply only to directory transfers", Retryable: false}
	}
	id, err := randomHex(16)
	if err != nil {
		return TransferView{}, err
	}
	secret, err := deriveTransferSecret(device.RelationshipKey, id)
	if err != nil {
		return TransferView{}, err
	}
	fingerprint, err := fingerprintSource(sourcePath, exclusions)
	if err != nil {
		return TransferView{}, err
	}
	mimeType := "application/x-directory"
	if kind == "file" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(info.Name())))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC()
	session := &transferSession{
		view: TransferView{
			TransferID: id,
			Direction:  "outgoing",
			DeviceID:   device.DeviceID,
			DeviceName: device.Name,
			Kind:       kind,
			FileName:   info.Name(),
			Size:       size,
			FileCount:  fileCount,
			MIMEType:   mimeType,
			Status:     "queued",
			Message:    "Waiting for the trusted device",
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		sourcePath:        sourcePath,
		sourceFingerprint: fingerprint,
		exclusions:        append([]string(nil), exclusions...),
		secret:            secret,
		decision:          make(chan transferDecision, 1),
		ctx:               ctx,
		cancel:            cancel,
	}
	m.mu.Lock()
	m.items[id] = session
	m.mu.Unlock()
	_ = m.store.Audit(AuditEvent{Action: "transfer.send", Outcome: "started", RequestID: id, DeviceID: device.DeviceID, Details: map[string]any{"fileName": info.Name(), "size": info.Size()}})
	_ = m.persist()
	m.signal()
	return session.snapshot(), nil
}

func (m *TransferManager) List() []TransferView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]TransferView, 0, len(m.items))
	for _, session := range m.items {
		items = append(items, session.snapshot())
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items
}

func (m *TransferManager) Get(id string) (TransferView, bool) {
	m.mu.RLock()
	session, ok := m.items[id]
	m.mu.RUnlock()
	if !ok {
		return TransferView{}, false
	}
	return session.snapshot(), true
}

func (m *TransferManager) Decide(id string, approve bool, outDir string) (TransferView, error) {
	session, err := m.session(id)
	if err != nil {
		return TransferView{}, err
	}
	view := session.snapshot()
	if view.Direction != "incoming" || view.Status != "pending_approval" {
		return TransferView{}, &APIError{Code: "transfer_not_approvable", Message: "transfer is not waiting for approval", Retryable: false}
	}
	if approve {
		if strings.TrimSpace(outDir) == "" {
			outDir = m.store.Config().ReceiveDir
		}
		outDir, err = prepareDestination(outDir, view.Size)
		if err != nil {
			return TransferView{}, err
		}
	}
	select {
	case session.decision <- transferDecision{approve: approve, outDir: outDir}:
		if approve {
			session.setStatus("preparing", "Preparing secure receiver")
		} else {
			session.setStatus("rejecting", "Rejecting transfer")
		}
		_ = m.persist()
		return session.snapshot(), nil
	default:
		return TransferView{}, &APIError{Code: "transfer_already_decided", Message: "transfer decision was already submitted", Retryable: false}
	}
}

func (m *TransferManager) Cancel(id string) (TransferView, error) {
	session, err := m.session(id)
	if err != nil {
		return TransferView{}, err
	}
	view := session.snapshot()
	if view.Terminal() {
		return view, nil
	}
	session.cancel()
	session.setStatus("cancelled", "Transfer cancelled")
	_ = m.store.Audit(AuditEvent{Action: "transfer.cancel", Outcome: "success", RequestID: id, DeviceID: view.DeviceID})
	_ = m.persist()
	return session.snapshot(), nil
}

func (m *TransferManager) HandleControl(ctx context.Context, peer Device, request ControlRequest) ControlResponse {
	if request.Type != "file.offer" && request.Type != "folder.offer" {
		return ControlResponse{OK: false, Error: &APIError{Code: "unsupported_request", Message: "control request type is unsupported", Retryable: false}}
	}
	if current, ok := m.store.Device(peer.DeviceID); ok {
		peer = current
	}
	if !hasCapability(peer, "files.request") {
		return ControlResponse{OK: false, Error: &APIError{Code: "request_denied", Message: "device is not allowed to request file transfers", Retryable: false}}
	}
	var offer fileOffer
	if err := json.Unmarshal(request.Payload, &offer); err != nil {
		return ControlResponse{OK: false, Error: &APIError{Code: "invalid_request", Message: "file offer is invalid", Retryable: false}}
	}
	if offer.Kind == "" {
		offer.Kind = "file"
	}
	if (request.Type == "file.offer" && offer.Kind != "file") || (request.Type == "folder.offer" && offer.Kind != "folder") {
		return ControlResponse{OK: false, Error: &APIError{Code: "invalid_request", Message: "transfer offer kind does not match its request type", Retryable: false}}
	}
	if err := validateFileOffer(offer, request.RequestID); err != nil {
		return ControlResponse{OK: false, Error: apiErrorFrom(err)}
	}
	session, err := m.addIncoming(peer, offer)
	if err != nil {
		return ControlResponse{OK: false, Error: apiErrorFrom(err)}
	}
	_ = m.store.Audit(AuditEvent{Action: "transfer.receive", Outcome: "pending_approval", RequestID: offer.TransferID, DeviceID: peer.DeviceID, Details: map[string]any{"fileName": offer.FileName, "size": offer.Size}})

	decision, automatic, err := m.automaticDecision(peer, offer)
	if err != nil {
		session.fail(apiErrorFrom(err).Code, apiErrorFrom(err).Message)
		return ControlResponse{OK: false, Error: apiErrorFrom(err)}
	}
	if !automatic {
		notifyDesktop("Verlay transfer request", fmt.Sprintf("%s wants to send %s", peer.Name, offer.FileName))
		select {
		case decision = <-session.decision:
		case <-time.After(5 * time.Minute):
			session.setStatus("rejected", "Approval expired")
			return responseJSON(fileOfferResponse{Approved: false})
		case <-ctx.Done():
			session.fail("device_unavailable", "Control connection closed")
			return ControlResponse{OK: false, Error: &APIError{Code: "device_unavailable", Message: "control connection closed", Retryable: true}}
		case <-session.ctx.Done():
			return ControlResponse{OK: false, Error: &APIError{Code: "transfer_cancelled", Message: "transfer was cancelled", Retryable: false}}
		}
	} else {
		session.setStatus("preparing", "Automatically approved by device policy")
	}
	if !decision.approve {
		session.setStatus("rejected", "Transfer rejected")
		_ = m.store.Audit(AuditEvent{Action: "transfer.approval", Outcome: "rejected", RequestID: offer.TransferID, DeviceID: peer.DeviceID})
		return responseJSON(fileOfferResponse{Approved: false})
	}
	session.setStatus("queued", "Waiting for receiver capacity")
	if !m.acquirePeerSlot(ctx, session.ctx, peer.DeviceID) {
		session.fail("device_unavailable", "Transfer was interrupted while waiting for receiver capacity")
		_ = m.persist()
		return ControlResponse{OK: false, Error: &APIError{Code: "device_unavailable", Message: "receiver capacity wait was interrupted", Retryable: true}}
	}
	staging := filepath.Join(m.store.Paths().DataDir, "transfers", offer.TransferID, "incoming")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		m.releaseSlot(peer.DeviceID)
		session.fail("state_write_failed", "Could not prepare incoming transfer")
		return ControlResponse{OK: false, Error: &APIError{Code: "state_write_failed", Message: "could not prepare incoming transfer", Retryable: true}}
	}
	session.setStatus("transferring", "Receiving securely")
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		m.runIncoming(session, offer, staging, decision.outDir)
	}()
	return responseJSON(fileOfferResponse{Approved: true})
}

func (m *TransferManager) runOutgoing(session *transferSession, device Device) {
	view := session.snapshot()
	offer := fileOffer{TransferID: view.TransferID, Kind: view.Kind, FileName: view.FileName, Size: view.Size, FileCount: view.FileCount, MIMEType: view.MIMEType, Secret: session.secret}
	payload, _ := json.Marshal(offer)
	requestType := "file.offer"
	if view.Kind == "folder" {
		requestType = "folder.offer"
	}
	requestCtx, cancel := context.WithTimeout(session.ctx, 10*time.Minute)
	defer cancel()
	response, err := m.control.Send(requestCtx, device, ControlRequest{ProtocolVersion: ProtocolVersion, RequestID: view.TransferID, Type: requestType, Payload: payload})
	if err != nil {
		apiErr := apiErrorFrom(err)
		if apiErr.Retryable {
			m.queueRetry(session, apiErr.Code, apiErr.Message)
		} else {
			session.fail(apiErr.Code, apiErr.Message)
		}
		return
	}
	if !response.OK {
		if response.Error == nil {
			session.fail("transfer_failed", "Peer returned an invalid response")
		} else if response.Error.Retryable {
			m.queueRetry(session, response.Error.Code, response.Error.Message)
		} else {
			session.fail(response.Error.Code, response.Error.Message)
		}
		return
	}
	var accepted fileOfferResponse
	if err := json.Unmarshal(response.Payload, &accepted); err != nil {
		session.fail("transfer_failed", "Peer returned an invalid approval")
		return
	}
	if !accepted.Approved {
		session.setStatus("rejected", "Receiver rejected the file")
		return
	}
	session.setStatus("transferring", "Sending securely")
	config := m.store.Config()
	if err := m.engine.Send(session.ctx, session.secret, config.RelayAddress, config.RelayPassword, session.sourcePath, session.exclusions); err != nil {
		if errors.Is(err, context.Canceled) {
			session.setStatus("cancelled", "Transfer cancelled")
		} else {
			m.queueRetry(session, "transfer_interrupted", sanitizeError(err.Error()))
		}
		return
	}
	session.setStatus("completed", "File sent successfully")
	_ = m.store.Audit(AuditEvent{Action: "transfer.send", Outcome: "success", RequestID: view.TransferID, DeviceID: view.DeviceID, Details: map[string]any{"fileName": view.FileName, "size": view.Size}})
}

func (m *TransferManager) runIncoming(session *transferSession, offer fileOffer, staging, outDir string) {
	defer m.releaseSlot(session.snapshot().DeviceID)
	defer m.persist()
	defer func() {
		view := session.snapshot()
		if view.Status == "completed" {
			notifyDesktop("Verlay received", view.FileName+" was saved successfully")
		} else if view.Status == "failed" {
			notifyDesktop("Verlay transfer failed", view.FileName+": "+view.Message)
		}
	}()
	config := m.store.Config()
	err := m.engine.Receive(session.ctx, offer.Secret, config.RelayAddress, config.RelayPassword, staging)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			session.setStatus("cancelled", "Transfer cancelled")
		} else {
			session.fail("transfer_failed", sanitizeError(err.Error()))
		}
		return
	}
	source := filepath.Join(staging, offer.FileName)
	destination, err := publishReceivedPath(source, outDir, offer.FileName, offer.Kind)
	if err != nil {
		session.fail("save_failed", sanitizeError(err.Error()))
		return
	}
	session.update(func(view *TransferView) {
		view.Status = "completed"
		view.Message = "File received successfully"
		view.SavedPath = destination
		view.UpdatedAt = time.Now().UTC()
	})
	_ = os.RemoveAll(filepath.Dir(staging))
	_ = m.store.Audit(AuditEvent{Action: "transfer.receive", Outcome: "success", RequestID: offer.TransferID, DeviceID: session.snapshot().DeviceID, Details: map[string]any{"fileName": offer.FileName, "size": offer.Size}})
}

func (m *TransferManager) addIncoming(peer Device, offer fileOffer) (*transferSession, error) {
	m.mu.Lock()
	if existing, exists := m.items[offer.TransferID]; exists {
		view := existing.snapshot()
		if view.Direction == "incoming" && view.Status == "failed" && view.DeviceID == peer.DeviceID &&
			view.FileName == offer.FileName && view.Size == offer.Size && view.Kind == offer.Kind {
			existing.cancel()
			delete(m.items, offer.TransferID)
		} else {
			m.mu.Unlock()
			return nil, &APIError{Code: "duplicate_request", Message: "transfer request was already seen", Retryable: false}
		}
	}
	for _, existing := range m.items {
		view := existing.snapshot()
		if view.DeviceID == peer.DeviceID && !view.Terminal() {
			m.mu.Unlock()
			return nil, &APIError{Code: "device_busy", Message: "device already has an active transfer", Retryable: true}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC()
	session := &transferSession{
		view:   TransferView{TransferID: offer.TransferID, Direction: "incoming", DeviceID: peer.DeviceID, DeviceName: peer.Name, Kind: offer.Kind, FileName: offer.FileName, Size: offer.Size, FileCount: offer.FileCount, MIMEType: offer.MIMEType, Status: "pending_approval", Message: "Waiting for approval", CreatedAt: now, UpdatedAt: now},
		secret: offer.Secret, decision: make(chan transferDecision, 1), ctx: ctx, cancel: cancel,
	}
	m.items[offer.TransferID] = session
	m.mu.Unlock()
	_ = m.persist()
	return session, nil
}

func (m *TransferManager) resolveDevice(query string) (Device, error) {
	return m.store.ResolveDevice(query)
}

func (m *TransferManager) session(id string) (*transferSession, error) {
	m.mu.RLock()
	session, ok := m.items[id]
	m.mu.RUnlock()
	if !ok {
		return nil, &APIError{Code: "transfer_not_found", Message: "transfer was not found", Retryable: false}
	}
	return session, nil
}

func (s *transferSession) snapshot() TransferView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.view
}

func (s *transferSession) update(update func(*TransferView)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	update(&s.view)
}

func (s *transferSession) setStatus(status, message string) {
	s.update(func(view *TransferView) {
		view.Status = status
		view.Message = message
		view.UpdatedAt = time.Now().UTC()
	})
}

func (s *transferSession) fail(code, message string) {
	s.update(func(view *TransferView) {
		view.Status = "failed"
		view.ErrorCode = code
		view.Message = message
		view.UpdatedAt = time.Now().UTC()
	})
}

func validateFileOffer(offer fileOffer, requestID string) error {
	if offer.TransferID == "" || offer.TransferID != requestID {
		return &APIError{Code: "invalid_request", Message: "transfer request ID is invalid", Retryable: false}
	}
	if offer.Size < 0 || offer.FileCount < 0 {
		return &APIError{Code: "invalid_request", Message: "file size is invalid", Retryable: false}
	}
	if offer.Kind != "file" && offer.Kind != "folder" {
		return &APIError{Code: "invalid_request", Message: "transfer kind is invalid", Retryable: false}
	}
	if offer.Kind == "file" && offer.FileCount > 1 {
		return &APIError{Code: "invalid_request", Message: "file offer contains an invalid file count", Retryable: false}
	}
	if offer.FileName == "" || offer.FileName != filepath.Base(offer.FileName) || strings.ContainsAny(offer.FileName, "/\\") || strings.ContainsRune(offer.FileName, 0) {
		return &APIError{Code: "invalid_request", Message: "file name is unsafe", Retryable: false}
	}
	if len(offer.FileName) > 240 || len(offer.Secret) < 16 || len(offer.Secret) > 256 {
		return &APIError{Code: "invalid_request", Message: "file offer is invalid", Retryable: false}
	}
	return nil
}

func inspectTransferSource(source string, info os.FileInfo, exclusions []string) (kind string, size int64, fileCount int, err error) {
	if info.Mode().IsRegular() {
		return "file", info.Size(), 1, nil
	}
	if !info.IsDir() {
		return "", 0, 0, &APIError{Code: "invalid_file", Message: "source must be one regular file or directory", Retryable: false}
	}
	err = filepath.Walk(source, func(path string, entry os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		if excludedPath(relative, exclusions) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Mode().IsRegular() {
			return &APIError{Code: "unsafe_folder", Message: "folders containing symlinks or special files are not supported", Retryable: false}
		}
		size += entry.Size()
		fileCount++
		return nil
	})
	if err != nil {
		return "", 0, 0, err
	}
	return "folder", size, fileCount, nil
}

func validateExclusions(exclusions []string) ([]string, error) {
	validated := make([]string, 0, len(exclusions))
	for _, exclusion := range exclusions {
		exclusion = filepath.Clean(strings.TrimSpace(exclusion))
		if exclusion == "" || exclusion == "." || filepath.IsAbs(exclusion) || exclusion == ".." ||
			strings.HasPrefix(exclusion, ".."+string(os.PathSeparator)) {
			return nil, &APIError{Code: "invalid_exclusion", Message: "folder exclusions must be safe relative paths", Retryable: false}
		}
		validated = append(validated, filepath.ToSlash(exclusion))
	}
	return validated, nil
}

func excludedPath(relative string, exclusions []string) bool {
	relative = filepath.ToSlash(filepath.Clean(relative))
	for _, exclusion := range exclusions {
		if relative == exclusion {
			return true
		}
	}
	return false
}

func fingerprintSource(source string, exclusions []string) (string, error) {
	root, err := filepath.Abs(filepath.Clean(source))
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if excludedPath(relative, exclusions) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return &APIError{Code: "unsafe_folder", Message: "source contains a symlink or special file", Retryable: false}
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00%d\n", filepath.ToSlash(relative), info.Size(), info.ModTime().UnixNano(), info.Mode())
		return nil
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(hash.Sum(nil)), nil
}

func deriveTransferSecret(encodedRelationshipKey, transferID string) (string, error) {
	key, err := base64.RawURLEncoding.DecodeString(encodedRelationshipKey)
	if err != nil || len(key) != 32 || transferID == "" {
		return "", errors.New("trusted device relationship key is invalid")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("mesh-transfer-secret-v1\x00"))
	mac.Write([]byte(transferID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (m *TransferManager) automaticDecision(peer Device, offer fileOffer) (transferDecision, bool, error) {
	policy := peer.ReceivePolicy
	if policy == nil || !policy.AutoReceive || offer.Size > policy.MaxBytes || (offer.Kind == "folder" && !policy.AllowFolders) {
		return transferDecision{}, false, nil
	}
	outDir, err := prepareDestination(policy.Inbox, offer.Size)
	if err != nil {
		return transferDecision{}, false, err
	}
	_ = m.store.Audit(AuditEvent{Action: "transfer.approval", Outcome: "automatic", RequestID: offer.TransferID, DeviceID: peer.DeviceID})
	return transferDecision{approve: true, outDir: outDir}, true, nil
}

func prepareDestination(path string, size int64) (string, error) {
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", &APIError{Code: "invalid_destination", Message: "could not create destination directory", Retryable: false}
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", &APIError{Code: "invalid_destination", Message: "destination is not a directory", Retryable: false}
	}
	if available, ok := availableDiskBytes(path); ok {
		if size > available {
			return "", &APIError{Code: "insufficient_space", Message: "destination does not have enough free space", Retryable: false}
		}
	}
	return path, nil
}

func publishReceivedPath(source, outDir, name, kind string) (string, error) {
	info, err := os.Lstat(source)
	if err != nil || (kind == "file" && !info.Mode().IsRegular()) || (kind == "folder" && !info.IsDir()) {
		return "", errors.New("transfer completed without the expected file or folder")
	}
	destination := uniqueDestination(outDir, name, kind)
	if err := os.Rename(source, destination); err == nil {
		return destination, nil
	}
	if kind == "folder" {
		if err := copyDirectory(source, destination); err != nil {
			_ = os.RemoveAll(destination)
			return "", err
		}
		if err := os.RemoveAll(source); err != nil {
			return "", err
		}
		return destination, nil
	}
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		_ = os.Remove(destination)
		return "", err
	}
	if err := output.Sync(); err != nil {
		output.Close()
		_ = os.Remove(destination)
		return "", err
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(destination)
		return "", err
	}
	if err := os.Remove(source); err != nil {
		return "", err
	}
	return destination, nil
}

func copyDirectory(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return errors.New("received folder escaped its staging root")
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return errors.New("received folder contains a symlink or special file")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func uniqueDestination(directory, name, kind string) string {
	candidate := filepath.Join(directory, name)
	if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
		return candidate
	}
	extension := ""
	base := name
	if kind == "file" {
		extension = filepath.Ext(name)
		base = strings.TrimSuffix(name, extension)
	}
	for i := 1; ; i++ {
		candidate = filepath.Join(directory, fmt.Sprintf("%s (%d)%s", base, i, extension))
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}

func hasCapability(device Device, wanted string) bool {
	for _, capability := range device.Capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func responseJSON(value any) ControlResponse {
	payload, err := json.Marshal(value)
	if err != nil {
		return ControlResponse{OK: false, Error: &APIError{Code: "internal_error", Message: "could not encode response", Retryable: false}}
	}
	return ControlResponse{OK: true, Payload: payload}
}

func apiErrorFrom(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return &APIError{Code: "internal_error", Message: sanitizeError(err.Error()), Retryable: false}
}

func notifyDesktop(title, message string) {
	executable, err := exec.LookPath("notify-send")
	if err != nil {
		return
	}
	if len(message) > 240 {
		message = message[:240]
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, executable, title, message).Run()
	}()
}
