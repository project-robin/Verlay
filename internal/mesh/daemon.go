package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/schollz/logger"
)

type Daemon struct {
	store     *Store
	version   string
	startedAt time.Time
	server    *http.Server
	listener  net.Listener
	lockFile  *os.File
	pairings  *PairingManager
	control   *ControlManager
	transfers *TransferManager
	closeOnce sync.Once
}

func NewDaemon(store *Store, version string) (*Daemon, error) {
	// Croc's reusable transport packages default to debug logging. The daemon
	// keeps production-safe logs and never emits relay or pairing key material.
	log.SetLevel("warn")
	engine, err := NewProcessEngine()
	if err != nil {
		return nil, err
	}
	transfers := NewTransferManager(store, engine)
	if err := transfers.Load(); err != nil {
		return nil, err
	}
	control, err := NewControlManager(store, transfers.HandleControl)
	if err != nil {
		return nil, err
	}
	transfers.SetControl(control)
	return &Daemon{store: store, version: version, startedAt: time.Now().UTC(), pairings: NewPairingManager(store), control: control, transfers: transfers}, nil
}

func (d *Daemon) Run(ctx context.Context) error {
	if err := d.acquireLock(); err != nil {
		return err
	}
	defer d.Close()
	if err := d.listen(); err != nil {
		return err
	}
	d.server = &http.Server{
		Handler:           d.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	_ = d.store.Audit(AuditEvent{Action: "daemon.start", Outcome: "success"})
	d.control.Start()

	errCh := make(chan error, 1)
	go func() {
		err := d.server.Serve(d.listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("stop daemon: %w", err)
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}

func (d *Daemon) Close() error {
	var closeErr error
	d.closeOnce.Do(func() {
		d.control.Close()
		d.transfers.Close()
		d.pairings.Close()
		_ = d.store.Audit(AuditEvent{Action: "daemon.stop", Outcome: "success"})
		if d.listener != nil {
			closeErr = d.listener.Close()
		}
		_ = os.Remove(d.store.Paths().SocketFile)
		if d.lockFile != nil {
			_ = unlockFile(d.lockFile)
			if err := d.lockFile.Close(); closeErr == nil {
				closeErr = err
			}
		}
	})
	return closeErr
}

func (d *Daemon) acquireLock() error {
	f, err := os.OpenFile(d.store.Paths().LockFile, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon lock: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, errLockHeld) {
			return &APIError{Code: "daemon_already_running", Message: "mesh daemon is already running", Retryable: false}
		}
		return fmt.Errorf("lock daemon: %w", err)
	}
	d.lockFile = f
	return nil
}

func (d *Daemon) listen() error {
	path := d.store.Paths().SocketFile
	if _, err := os.Lstat(path); err == nil {
		conn, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return &APIError{Code: "daemon_already_running", Message: "mesh daemon is already listening", Retryable: false}
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove stale daemon socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect daemon socket: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen on daemon socket: %w", err)
	}
	if err := protectSocket(path); err != nil {
		listener.Close()
		return fmt.Errorf("protect daemon socket: %w", err)
	}
	d.listener = listener
	return nil
}

func (d *Daemon) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", d.handleStatus)
	mux.HandleFunc("GET /v1/doctor", d.handleDoctor)
	mux.HandleFunc("GET /v1/relay", d.handleRelay)
	mux.HandleFunc("POST /v1/relay", d.handleRelay)
	mux.HandleFunc("GET /v1/devices", d.handleDevices)
	mux.HandleFunc("GET /v1/devices/{deviceID}", d.handleDevice)
	mux.HandleFunc("DELETE /v1/devices/{deviceID}", d.handleRevokeDevice)
	mux.HandleFunc("POST /v1/devices/{deviceID}/rename", d.handleRenameDevice)
	mux.HandleFunc("POST /v1/devices/{deviceID}/receive-policy", d.handleReceivePolicy)
	mux.HandleFunc("GET /v1/pairings", d.handlePairings)
	mux.HandleFunc("POST /v1/pairings/create", d.handleCreatePairing)
	mux.HandleFunc("POST /v1/pairings/accept", d.handleAcceptPairing)
	mux.HandleFunc("POST /v1/pairings/{pairingID}/decision", d.handlePairingDecision)
	mux.HandleFunc("GET /v1/transfers", d.handleTransfers)
	mux.HandleFunc("POST /v1/transfers/send", d.handleSendTransfer)
	mux.HandleFunc("GET /v1/transfers/{transferID}", d.handleTransfer)
	mux.HandleFunc("POST /v1/transfers/{transferID}/decision", d.handleTransferDecision)
	mux.HandleFunc("POST /v1/transfers/{transferID}/cancel", d.handleTransferCancel)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func (d *Daemon) handleTransfers(w http.ResponseWriter, _ *http.Request) {
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: map[string]any{"transfers": d.transfers.List()}})
}

func (d *Daemon) handleSendTransfer(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Device     string   `json:"device"`
		Path       string   `json:"path"`
		Exclusions []string `json:"exclusions,omitempty"`
	}
	if err := decodeRequest(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return
	}
	transfer, err := d.transfers.Send(request.Device, request.Path, request.Exclusions)
	if err != nil {
		writeError(w, err)
		return
	}
	writeEnvelope(w, http.StatusAccepted, Envelope{OK: true, Data: transfer})
}

func (d *Daemon) handleTransfer(w http.ResponseWriter, r *http.Request) {
	transfer, ok := d.transfers.Get(r.PathValue("transferID"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "transfer_not_found", "transfer was not found", false)
		return
	}
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: transfer})
}

func (d *Daemon) handleTransferDecision(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Approve bool   `json:"approve"`
		OutDir  string `json:"outDir,omitempty"`
	}
	if err := decodeRequest(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return
	}
	transfer, err := d.transfers.Decide(r.PathValue("transferID"), request.Approve, request.OutDir)
	if err != nil {
		writeError(w, err)
		return
	}
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: transfer})
}

func (d *Daemon) handleTransferCancel(w http.ResponseWriter, r *http.Request) {
	transfer, err := d.transfers.Cancel(r.PathValue("transferID"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: transfer})
}

func (d *Daemon) handlePairings(w http.ResponseWriter, _ *http.Request) {
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: map[string]any{"pairings": d.pairings.List()}})
}

func (d *Daemon) handleCreatePairing(w http.ResponseWriter, _ *http.Request) {
	created, err := d.pairings.Create()
	if err != nil {
		writeError(w, err)
		return
	}
	writeEnvelope(w, http.StatusAccepted, Envelope{OK: true, Data: created})
}

func (d *Daemon) handleAcceptPairing(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Code string `json:"code"`
	}
	if err := decodeRequest(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return
	}
	pairing, err := d.pairings.Accept(request.Code)
	if err != nil {
		writeError(w, err)
		return
	}
	writeEnvelope(w, http.StatusAccepted, Envelope{OK: true, Data: pairing})
}

func (d *Daemon) handlePairingDecision(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Approve bool `json:"approve"`
	}
	if err := decodeRequest(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return
	}
	pairing, err := d.pairings.Decide(r.PathValue("pairingID"), request.Approve)
	if err != nil {
		writeError(w, err)
		return
	}
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: pairing})
}

func (d *Daemon) handleStatus(w http.ResponseWriter, _ *http.Request) {
	identity := d.store.Identity()
	config := d.store.Config()
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: Status{
		APIVersion:      APIVersion,
		ProtocolVersion: ProtocolVersion,
		Version:         d.version,
		DeviceID:        identity.DeviceID,
		DeviceName:      identity.Name,
		Platform:        identity.Platform,
		RelayAddress:    config.RelayAddress,
		PairedDevices:   len(d.store.Devices()),
		StartedAt:       d.startedAt,
	}})
}

func (d *Daemon) handleRelay(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		config := d.store.Config()
		writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: map[string]any{
			"address": config.RelayAddress, "passwordConfigured": config.RelayPassword != "",
		}})
		return
	}
	var request struct {
		Address  string `json:"address"`
		Password string `json:"password"`
	}
	if err := decodeRequest(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return
	}
	config, err := d.store.SetRelay(request.Address, request.Password)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = d.store.Audit(AuditEvent{Action: "relay.configure", Outcome: "success"})
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: map[string]any{
		"address": config.RelayAddress, "passwordConfigured": true, "restartRequired": true,
	}})
}

func (d *Daemon) handleDevices(w http.ResponseWriter, _ *http.Request) {
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: map[string]any{"devices": d.store.Devices()}})
}

func (d *Daemon) handleDevice(w http.ResponseWriter, r *http.Request) {
	device, ok := d.store.Device(r.PathValue("deviceID"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "device_not_found", "trusted device was not found", false)
		return
	}
	device.RelationshipKey = ""
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: device})
}

func (d *Daemon) handleRenameDevice(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
	}
	if err := decodeRequest(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return
	}
	device, err := d.store.RenameDevice(r.PathValue("deviceID"), request.Name)
	if err != nil {
		writeError(w, err)
		return
	}
	_ = d.store.Audit(AuditEvent{Action: "device.rename", Outcome: "success", DeviceID: device.DeviceID})
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: device})
}

func (d *Daemon) handleReceivePolicy(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AutoReceive  bool  `json:"autoReceive"`
		MaxBytes     int64 `json:"maxBytes,omitempty"`
		AllowFolders bool  `json:"allowFolders,omitempty"`
	}
	if err := decodeRequest(r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), false)
		return
	}
	device, err := d.store.ResolveDevice(r.PathValue("deviceID"))
	if err != nil {
		writeError(w, err)
		return
	}
	if request.AutoReceive {
		device, err = d.store.AuthorizeReceive(device.DeviceID, request.MaxBytes, request.AllowFolders)
	} else {
		device, err = d.store.RequireReceiveApproval(device.DeviceID)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	outcome := "manual"
	if request.AutoReceive {
		outcome = "automatic"
	}
	_ = d.store.Audit(AuditEvent{Action: "device.receive_policy", Outcome: outcome, DeviceID: device.DeviceID})
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: device})
}

func (d *Daemon) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("deviceID"))
	if id == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_device", "device ID is required", false)
		return
	}
	revoked, err := d.store.RevokeDevice(id)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "state_write_failed", "could not update trusted devices", true)
		return
	}
	if !revoked {
		writeAPIError(w, http.StatusNotFound, "device_not_found", "trusted device was not found", false)
		return
	}
	_ = d.store.Audit(AuditEvent{Action: "device.revoke", Outcome: "success", DeviceID: id})
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: map[string]any{"deviceId": id, "revoked": true}})
}

func (d *Daemon) handleDoctor(w http.ResponseWriter, _ *http.Request) {
	checks := []DoctorCheck{
		checkPathMode("data_directory", d.store.Paths().DataDir, 0o700),
		checkPathMode("runtime_directory", d.store.Paths().RuntimeDir, 0o700),
		checkPathMode("identity_file", d.store.Paths().IdentityFile, 0o600),
		checkPathMode("devices_file", d.store.Paths().DevicesFile, 0o600),
		checkOptionalPathMode("transfers_file", d.store.Paths().TransfersFile, 0o600),
		checkPathMode("daemon_socket", d.store.Paths().SocketFile, 0o600),
	}
	healthy := true
	for _, check := range checks {
		healthy = healthy && check.OK
	}
	writeEnvelope(w, http.StatusOK, Envelope{OK: true, Data: DoctorResult{Healthy: healthy, Checks: checks}})
}

func checkOptionalPathMode(name, path string, expected os.FileMode) DoctorCheck {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return DoctorCheck{Name: name, OK: true, Message: filepath.Base(path) + " has not been created yet"}
	}
	return checkPathMode(name, path, expected)
}

func checkPathMode(name, path string, expected os.FileMode) DoctorCheck {
	info, err := os.Stat(path)
	if err != nil {
		return DoctorCheck{Name: name, OK: false, Message: err.Error()}
	}
	ok, message := securePathMode(filepath.Base(path), info.Mode().Perm(), expected)
	return DoctorCheck{Name: name, OK: ok, Message: message}
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, retryable bool) {
	writeEnvelope(w, status, Envelope{OK: false, Error: &APIError{Code: code, Message: message, Retryable: retryable}})
}

func writeError(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		status := http.StatusBadRequest
		if apiErr.Code == "pairing_not_found" || apiErr.Code == "device_not_found" || apiErr.Code == "transfer_not_found" {
			status = http.StatusNotFound
		}
		writeEnvelope(w, status, Envelope{OK: false, Error: apiErr})
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error(), false)
}

func decodeRequest(r *http.Request, value any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func writeEnvelope(w http.ResponseWriter, status int, envelope Envelope) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope)
}
