package mesh

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	ConfigDir     string
	DataDir       string
	RuntimeDir    string
	ConfigFile    string
	IdentityFile  string
	DevicesFile   string
	TransfersFile string
	AuditFile     string
	SocketFile    string
	LockFile      string
}

func ResolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}
	configRoot := os.Getenv("XDG_CONFIG_HOME")
	if configRoot == "" {
		configRoot = filepath.Join(home, ".config")
	}
	dataRoot := os.Getenv("XDG_DATA_HOME")
	if dataRoot == "" {
		dataRoot = filepath.Join(home, ".local", "share")
	}
	runtimeRoot := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeRoot == "" {
		runtimeRoot = filepath.Join(os.TempDir(), fmt.Sprintf("mesh-%d", os.Getuid()))
	}
	configDir := filepath.Join(configRoot, "mesh")
	dataDir := filepath.Join(dataRoot, "mesh")
	runtimeDir := filepath.Join(runtimeRoot, "mesh")
	return Paths{
		ConfigDir:     configDir,
		DataDir:       dataDir,
		RuntimeDir:    runtimeDir,
		ConfigFile:    filepath.Join(configDir, "config.json"),
		IdentityFile:  filepath.Join(dataDir, "identity.json"),
		DevicesFile:   filepath.Join(dataDir, "devices.json"),
		TransfersFile: filepath.Join(dataDir, "transfers.json"),
		AuditFile:     filepath.Join(dataDir, "audit.jsonl"),
		SocketFile:    filepath.Join(runtimeDir, "mesh.sock"),
		LockFile:      filepath.Join(runtimeDir, "mesh.lock"),
	}, nil
}

func (p Paths) Ensure() error {
	for _, dir := range []string{p.ConfigDir, p.DataDir, p.RuntimeDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("protect %s: %w", dir, err)
		}
	}
	return nil
}
