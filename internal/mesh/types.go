package mesh

import "time"

const (
	APIVersion                  = "v1"
	ProtocolVersion             = 1
	DefaultRelay                = "croc.schollz.com:9009"
	DefaultTrustedTransferLimit = int64(100 << 30)
)

// Envelope is the stable machine-readable result produced by the CLI and API.
type Envelope struct {
	OK    bool      `json:"ok"`
	Data  any       `json:"data,omitempty"`
	Error *APIError `json:"error,omitempty"`
}

type APIError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

func (e *APIError) Error() string { return e.Message }

type Identity struct {
	DeviceID  string    `json:"deviceId"`
	Name      string    `json:"name"`
	Platform  string    `json:"platform"`
	PublicKey string    `json:"publicKey"`
	Seed      string    `json:"seed"`
	CreatedAt time.Time `json:"createdAt"`
}

type Device struct {
	DeviceID        string         `json:"deviceId"`
	Name            string         `json:"name"`
	Platform        string         `json:"platform"`
	PublicKey       string         `json:"publicKey"`
	RelationshipKey string         `json:"relationshipKey,omitempty"`
	Capabilities    []string       `json:"capabilities"`
	ReceivePolicy   *ReceivePolicy `json:"receivePolicy,omitempty"`
	PairedAt        time.Time      `json:"pairedAt"`
	LastSeenAt      time.Time      `json:"lastSeenAt,omitempty"`
}

type ReceivePolicy struct {
	AutoReceive  bool   `json:"autoReceive"`
	Inbox        string `json:"inbox"`
	MaxBytes     int64  `json:"maxBytes"`
	AllowFolders bool   `json:"allowFolders"`
}

type Config struct {
	RelayAddress  string `json:"relayAddress"`
	RelayPassword string `json:"relayPassword,omitempty"`
	ReceiveDir    string `json:"receiveDir"`
}

type Status struct {
	APIVersion      string    `json:"apiVersion"`
	ProtocolVersion int       `json:"protocolVersion"`
	Version         string    `json:"version"`
	DeviceID        string    `json:"deviceId"`
	DeviceName      string    `json:"deviceName"`
	Platform        string    `json:"platform"`
	RelayAddress    string    `json:"relayAddress"`
	PairedDevices   int       `json:"pairedDevices"`
	StartedAt       time.Time `json:"startedAt"`
}

type DoctorCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type DoctorResult struct {
	Healthy bool          `json:"healthy"`
	Checks  []DoctorCheck `json:"checks"`
}

type AuditEvent struct {
	Timestamp time.Time      `json:"timestamp"`
	Action    string         `json:"action"`
	Outcome   string         `json:"outcome"`
	RequestID string         `json:"requestId,omitempty"`
	DeviceID  string         `json:"deviceId,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}
