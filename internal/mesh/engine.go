package mesh

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/project-robin/Verlay/src/croc"
	"github.com/project-robin/Verlay/src/models"
)

type Engine interface {
	Send(context.Context, string, string, string, string, []string) error
	Receive(context.Context, string, string, string, string) error
}

type ProcessEngine struct {
	Executable string
}

func NewProcessEngine() (*ProcessEngine, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &ProcessEngine{Executable: executable}, nil
}

func (e *ProcessEngine) Send(ctx context.Context, secret, relay, relayPassword, source string, exclusions []string) error {
	return e.run(ctx, secret, relayPassword, exclusions, "send", relay, source)
}

func (e *ProcessEngine) Receive(ctx context.Context, secret, relay, relayPassword, destination string) error {
	return e.run(ctx, secret, relayPassword, nil, "receive", relay, destination)
}

func (e *ProcessEngine) run(ctx context.Context, secret, relayPassword string, exclusions []string, args ...string) error {
	command := exec.CommandContext(ctx, e.Executable, append([]string{"internal-transfer"}, args...)...)
	// The transfer child does not need the long-term device identity. In
	// particular, do not let Android's externally managed seed cross another
	// process boundary unnecessarily.
	command.Env = environmentWithout("MESH_IDENTITY_SEED")
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	encodedExclusions, err := json.Marshal(exclusions)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(stdin, secret+"\n"+relayPassword+"\n"+base64.RawURLEncoding.EncodeToString(encodedExclusions)+"\n"); err != nil {
		stdin.Close()
		_ = command.Process.Kill()
		return err
	}
	_ = stdin.Close()
	lines := &safeLines{}
	var wg sync.WaitGroup
	wg.Add(2)
	go collectSafeLines(stdout, lines, &wg)
	go collectSafeLines(stderr, lines, &wg)
	waitErr := command.Wait()
	wg.Wait()
	if waitErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return context.Canceled
		}
		detail := lines.last()
		if detail != "" {
			return fmt.Errorf("transfer engine failed: %s", detail)
		}
		return fmt.Errorf("transfer engine failed: %w", waitErr)
	}
	return nil
}

func environmentWithout(names ...string) []string {
	blocked := make(map[string]struct{}, len(names))
	for _, name := range names {
		blocked[name] = struct{}{}
	}
	environment := os.Environ()
	filtered := environment[:0]
	for _, item := range environment {
		name, _, _ := strings.Cut(item, "=")
		if _, found := blocked[name]; !found {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

type safeLines struct {
	mu    sync.Mutex
	lines []string
}

func (s *safeLines) add(line string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "Code is:") || strings.Contains(line, "CROC_SECRET") || strings.HasPrefix(line, "On the other computer run:") || strings.HasPrefix(line, "croc ") {
		return
	}
	if len(line) > 240 {
		line = line[:240]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lines) == 12 {
		s.lines = s.lines[1:]
	}
	s.lines = append(s.lines, line)
}

func (s *safeLines) last() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lines) == 0 {
		return ""
	}
	return s.lines[len(s.lines)-1]
}

func collectSafeLines(reader io.Reader, lines *safeLines, wg *sync.WaitGroup) {
	defer wg.Done()
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		lines.add(scanner.Text())
	}
}

func RunInternalTransfer(args []string, secretReader io.Reader) error {
	if len(args) != 3 || (args[0] != "send" && args[0] != "receive") {
		return errors.New("invalid internal transfer invocation")
	}
	reader := bufio.NewReader(io.LimitReader(secretReader, 64<<10))
	secret, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return errors.New("could not read transfer secret")
	}
	secret = strings.TrimSpace(secret)
	if len(secret) < 16 || len(secret) > 256 {
		return errors.New("invalid transfer secret")
	}
	relayPassword, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return errors.New("could not read relay password")
	}
	relayPassword = strings.TrimSpace(relayPassword)
	if relayPassword == "" {
		relayPassword = models.DEFAULT_PASSPHRASE
	}
	if len(relayPassword) > 256 {
		return errors.New("invalid relay password")
	}
	encodedExclusions, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return errors.New("could not read folder exclusions")
	}
	var exclusions []string
	encodedExclusions = strings.TrimSpace(encodedExclusions)
	if encodedExclusions != "" {
		payload, decodeErr := base64.RawURLEncoding.DecodeString(encodedExclusions)
		if decodeErr != nil || json.Unmarshal(payload, &exclusions) != nil {
			return errors.New("invalid folder exclusions")
		}
	}
	relay := strings.TrimSpace(args[1])
	if relay == "" {
		return errors.New("relay address is required")
	}
	options := croc.Options{
		SharedSecret:  secret,
		IsSender:      args[0] == "send",
		NoPrompt:      true,
		RelayAddress:  relay,
		RelayPorts:    []string{"9009", "9010", "9011", "9012", "9013"},
		RelayPassword: relayPassword,
		// The daemon already has an authenticated control path. Running Croc's
		// ephemeral local-relay race from two peers on one host can select
		// different routes during PAKE; use the explicit relay until the mesh
		// transport owns LAN discovery directly.
		DisableLocal:     true,
		Overwrite:        true,
		Curve:            "p256",
		HashAlgorithm:    "xxhash",
		MulticastAddress: "239.255.255.250",
		DisableClipboard: true,
		IgnoreStdin:      true,
	}
	client, err := croc.New(options)
	if err != nil {
		return err
	}
	if options.IsSender {
		path := filepath.Clean(args[2])
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return errors.New("mesh sends one regular file or directory at a time")
		}
		files, emptyFolders, folderCount, err := croc.GetFilesInfoWithExactExclusions([]string{path}, false, false, nil, exclusions)
		if err != nil {
			return err
		}
		return client.Send(files, emptyFolders, folderCount)
	}
	destination := filepath.Clean(args[2])
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	oldDirectory, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(destination); err != nil {
		return err
	}
	defer os.Chdir(oldDirectory)
	return client.Receive()
}
