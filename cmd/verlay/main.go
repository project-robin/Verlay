package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	meshcore "github.com/project-robin/Verlay/internal/mesh"
)

var version = "dev"

type options struct {
	json    bool
	noInput bool
	wait    bool
	timeout time.Duration
}

func main() {
	os.Exit(run(invocationArgs(os.Args[0], os.Args[1:])))
}

func invocationArgs(executable string, args []string) []string {
	executable = strings.ReplaceAll(executable, `\`, "/")
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(executable)), ".exe")
	if name == "verlayd" || strings.HasPrefix(name, "verlayd-") {
		if len(args) > 0 && args[0] == "internal-transfer" {
			return args
		}
		if len(args) != 0 {
			return []string{"daemon", "invalid-verlayd-arguments"}
		}
		return []string{"daemon", "run"}
	}
	return args
}

func run(rawArgs []string) int {
	if len(rawArgs) > 0 && rawArgs[0] == "internal-transfer" {
		if err := meshcore.RunInternalTransfer(rawArgs[1:], os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "transfer engine: %s\n", err)
			return 5
		}
		return 0
	}
	args, opts, err := parseOptions(rawArgs)
	if err != nil {
		return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: err.Error(), Retryable: false}, 2)
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(helpText)
		return 0
	}
	// Static automation contracts stay available during installation, before
	// daemon runtime directories necessarily exist.
	switch args[0] {
	case "version":
		return printSuccess(opts.json, map[string]any{"version": version, "apiVersion": meshcore.APIVersion, "protocolVersion": meshcore.ProtocolVersion})
	case "schema":
		return printSuccess(opts.json, commandSchema())
	case "skill":
		if len(args) != 2 || args[1] != "print" {
			return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay skill print", Retryable: false}, 2)
		}
		if opts.json {
			return printSuccess(true, map[string]any{"skill": meshcore.AgentSkill()})
		}
		fmt.Print(meshcore.AgentSkill())
		return 0
	}

	paths, err := meshcore.ResolvePaths()
	if err != nil {
		return printFailure(opts.json, apiError(err), 10)
	}

	switch args[0] {
	case "daemon":
		return runDaemon(args[1:], opts, paths)
	case "status":
		return callDaemon(opts, paths, http.MethodGet, "/v1/status")
	case "doctor":
		return callDaemon(opts, paths, http.MethodGet, "/v1/doctor")
	case "relay":
		return runRelay(args[1:], opts, paths)
	case "devices":
		return runDevices(args[1:], opts, paths)
	case "pair":
		return runPair(args[1:], opts, paths)
	case "send":
		return runSend(args[1:], opts, paths)
	case "transfers":
		return runTransfers(args[1:], opts, paths)
	default:
		return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: fmt.Sprintf("unknown command %q", args[0]), Retryable: false}, 2)
	}
}

func runRelay(args []string, opts options, paths meshcore.Paths) int {
	if len(args) == 1 && args[0] == "show" {
		return callDaemon(opts, paths, http.MethodGet, "/v1/relay")
	}
	if len(args) == 3 && args[0] == "set" && args[2] == "--password-stdin" {
		password, err := io.ReadAll(io.LimitReader(os.Stdin, 513))
		if err != nil || len(password) > 512 || strings.TrimSpace(string(password)) == "" {
			return printFailure(opts.json, &meshcore.APIError{Code: "invalid_relay", Message: "relay password must be provided on stdin", Retryable: false}, 2)
		}
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/relay", map[string]any{
			"address": args[1], "password": strings.TrimSpace(string(password)),
		})
	}
	return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay relay show|set HOST:PORT --password-stdin", Retryable: false}, 2)
}

func runDaemon(args []string, opts options, paths meshcore.Paths) int {
	if len(args) != 1 {
		return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay daemon start|stop|restart (run verlayd directly for foreground mode)", Retryable: false}, 2)
	}
	if args[0] == "run" {
		store, err := meshcore.OpenStore(paths)
		if err != nil {
			return printFailure(opts.json, apiError(err), 10)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		daemon, err := meshcore.NewDaemon(store, version)
		if err != nil {
			return printFailure(opts.json, apiError(err), 10)
		}
		if err := daemon.Run(ctx); err != nil {
			return printFailure(opts.json, apiError(err), exitCode(err))
		}
		return 0
	}
	if args[0] != "start" && args[0] != "stop" && args[0] != "restart" {
		return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay daemon start|stop|restart (run verlayd directly for foreground mode)", Retryable: false}, 2)
	}
	if runtime.GOOS == "windows" {
		return printFailure(opts.json, &meshcore.APIError{
			Code:    "service_control_unavailable",
			Message: "Windows beta has no background service yet; keep `verlayd.exe` open in one terminal",
		}, 3)
	}
	command := exec.Command("systemctl", "--user", args[0], "verlayd.service")
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return printFailure(opts.json, &meshcore.APIError{Code: "service_control_failed", Message: message, Retryable: true}, 3)
	}
	return printSuccess(opts.json, map[string]any{"action": args[0], "service": "verlayd.service"})
}

func runSend(args []string, opts options, paths meshcore.Paths) int {
	device, path, exclusions, err := parseSendArgs(args)
	if err != nil {
		return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: err.Error(), Retryable: false}, 2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	client := meshcore.NewClient(paths.SocketFile, opts.timeout)
	envelope, err := client.Call(ctx, http.MethodPost, "/v1/transfers/send", map[string]any{"device": device, "path": path, "exclusions": exclusions})
	if err != nil {
		return printFailure(opts.json, apiError(err), exitCode(err))
	}
	if !envelope.OK {
		return printFailure(opts.json, envelope.Error, exitCode(envelope.Error))
	}
	if !opts.wait {
		return printSuccess(opts.json, envelope.Data)
	}
	transferID := dataString(envelope.Data, "transferId")
	if transferID == "" {
		return printFailure(opts.json, &meshcore.APIError{Code: "internal_error", Message: "daemon omitted the transfer ID", Retryable: false}, 10)
	}
	return waitForTransfer(opts, paths, transferID)
}

func parseSendArgs(args []string) (device, path string, exclusions []string, err error) {
	if len(args) < 2 {
		return "", "", nil, errors.New("usage: verlay send DEVICE PATH [--exclude PATHS]")
	}
	device, path = args[0], args[1]
	for i := 2; i < len(args); i++ {
		if args[i] != "--exclude" || i+1 >= len(args) {
			return "", "", nil, errors.New("usage: verlay send DEVICE PATH [--exclude PATHS]")
		}
		i++
		if len(args[i]) > 4096 {
			return "", "", nil, errors.New("--exclude is too long")
		}
		for _, item := range strings.Split(args[i], ",") {
			if item = strings.TrimSpace(item); item != "" {
				exclusions = append(exclusions, item)
			}
		}
	}
	return device, path, exclusions, nil
}

func runTransfers(args []string, opts options, paths meshcore.Paths) int {
	if len(args) == 1 && args[0] == "list" {
		return callDaemon(opts, paths, http.MethodGet, "/v1/transfers")
	}
	if len(args) == 2 && args[0] == "show" {
		return callDaemon(opts, paths, http.MethodGet, "/v1/transfers/"+url.PathEscape(args[1]))
	}
	if len(args) >= 2 && (args[0] == "approve" || args[0] == "reject") {
		outDir := ""
		if len(args) == 4 && args[2] == "--out" {
			outDir = args[3]
		} else if len(args) != 2 {
			return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay transfers approve ID [--out DIRECTORY]", Retryable: false}, 2)
		}
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/transfers/"+url.PathEscape(args[1])+"/decision", map[string]any{"approve": args[0] == "approve", "outDir": outDir})
	}
	if len(args) == 2 && args[0] == "cancel" {
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/transfers/"+url.PathEscape(args[1])+"/cancel", map[string]any{})
	}
	return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay transfers list|show ID|approve ID [--out DIR]|reject ID|cancel ID", Retryable: false}, 2)
}

func runDevices(args []string, opts options, paths meshcore.Paths) int {
	if len(args) == 1 && args[0] == "list" {
		return callDaemon(opts, paths, http.MethodGet, "/v1/devices")
	}
	if len(args) == 2 && args[0] == "show" {
		return callDaemon(opts, paths, http.MethodGet, "/v1/devices/"+url.PathEscape(args[1]))
	}
	if len(args) == 3 && args[0] == "rename" {
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/devices/"+url.PathEscape(args[1])+"/rename", map[string]any{"name": args[2]})
	}
	if len(args) == 2 && args[0] == "revoke" {
		return callDaemon(opts, paths, http.MethodDelete, "/v1/devices/"+url.PathEscape(args[1]))
	}
	if len(args) >= 2 && args[0] == "authorize" {
		device, maxBytes, folders, err := parseReceiveAuthorization(args[1:])
		if err != nil {
			return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: err.Error(), Retryable: false}, 2)
		}
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/devices/"+url.PathEscape(device)+"/receive-policy", map[string]any{
			"autoReceive": true, "maxBytes": maxBytes, "allowFolders": folders,
		})
	}
	if len(args) == 2 && args[0] == "require-approval" {
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/devices/"+url.PathEscape(args[1])+"/receive-policy", map[string]any{"autoReceive": false})
	}
	return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay devices list|show ID|rename ID NAME|revoke ID|authorize DEVICE --max-bytes N [--folders]|require-approval DEVICE", Retryable: false}, 2)
}

func parseReceiveAuthorization(args []string) (device string, maxBytes int64, folders bool, err error) {
	if len(args) < 3 {
		return "", 0, false, errors.New("usage: verlay devices authorize DEVICE --max-bytes N [--folders]")
	}
	device = strings.TrimSpace(args[0])
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--folders":
			folders = true
		case "--max-bytes":
			if i+1 >= len(args) {
				return "", 0, false, errors.New("--max-bytes requires a positive integer")
			}
			i++
			value, parseErr := strconv.ParseInt(args[i], 10, 64)
			if parseErr != nil || value <= 0 {
				return "", 0, false, errors.New("--max-bytes requires a positive integer")
			}
			maxBytes = value
		default:
			return "", 0, false, fmt.Errorf("unknown authorize option %q", args[i])
		}
	}
	if device == "" || maxBytes <= 0 {
		return "", 0, false, errors.New("usage: verlay devices authorize DEVICE --max-bytes N [--folders]")
	}
	return device, maxBytes, folders, nil
}

func runPair(args []string, opts options, paths meshcore.Paths) int {
	if len(args) == 1 && args[0] == "create" {
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/pairings/create", map[string]any{})
	}
	if len(args) == 2 && args[0] == "accept" {
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/pairings/accept", map[string]any{"code": args[1]})
	}
	if len(args) == 1 && args[0] == "list" {
		return callDaemon(opts, paths, http.MethodGet, "/v1/pairings")
	}
	if len(args) == 2 && (args[0] == "approve" || args[0] == "reject") {
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/pairings/"+url.PathEscape(args[1])+"/decision", map[string]any{"approve": args[0] == "approve"})
	}
	return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay pair create|accept CODE|list|approve ID|reject ID", Retryable: false}, 2)
}

func callDaemon(opts options, paths meshcore.Paths, method, path string) int {
	return callDaemonWithBody(opts, paths, method, path, nil)
}

func callDaemonWithBody(opts options, paths meshcore.Paths, method, path string, body any) int {
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	client := meshcore.NewClient(paths.SocketFile, opts.timeout)
	envelope, err := client.Call(ctx, method, path, body)
	if err != nil {
		return printFailure(opts.json, apiError(err), exitCode(err))
	}
	if !envelope.OK {
		return printFailure(opts.json, envelope.Error, exitCode(envelope.Error))
	}
	return printSuccess(opts.json, envelope.Data)
}

func parseOptions(args []string) ([]string, options, error) {
	opts := options{timeout: 15 * time.Second}
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--":
			filtered = append(filtered, args[i+1:]...)
			return filtered, opts, nil
		case args[i] == "--json":
			opts.json = true
		case args[i] == "--no-input":
			opts.noInput = true
		case args[i] == "--wait":
			opts.wait = true
		case args[i] == "--timeout":
			if i+1 >= len(args) {
				return nil, opts, errors.New("--timeout requires a duration")
			}
			i++
			duration, err := time.ParseDuration(args[i])
			if err != nil || duration <= 0 {
				return nil, opts, errors.New("--timeout must be a positive duration such as 10s")
			}
			opts.timeout = duration
		case strings.HasPrefix(args[i], "--timeout="):
			duration, err := time.ParseDuration(strings.TrimPrefix(args[i], "--timeout="))
			if err != nil || duration <= 0 {
				return nil, opts, errors.New("--timeout must be a positive duration such as 10s")
			}
			opts.timeout = duration
		default:
			filtered = append(filtered, args[i])
		}
	}
	return filtered, opts, nil
}

func waitForTransfer(opts options, paths meshcore.Paths, transferID string) int {
	deadline := time.Now().Add(opts.timeout)
	client := meshcore.NewClient(paths.SocketFile, opts.timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return printFailure(opts.json, &meshcore.APIError{
				Code: "timeout", Message: "timed out waiting; the persisted transfer remains queued", Retryable: true,
				Details: map[string]any{"transferId": transferID},
			}, 3)
		}
		ctx, cancel := context.WithTimeout(context.Background(), minDuration(remaining, 5*time.Second))
		envelope, err := client.Call(ctx, http.MethodGet, "/v1/transfers/"+url.PathEscape(transferID), nil)
		cancel()
		if err != nil {
			return printFailure(opts.json, apiError(err), exitCode(err))
		}
		status := dataString(envelope.Data, "status")
		if status == "completed" {
			return printSuccess(opts.json, envelope.Data)
		}
		if status == "failed" || status == "rejected" || status == "cancelled" {
			return printFailure(opts.json, &meshcore.APIError{Code: "transfer_failed", Message: dataString(envelope.Data, "message"), Retryable: status == "failed"}, 5)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func dataString(data any, key string) string {
	if object, ok := data.(map[string]any); ok {
		if value, ok := object[key].(string); ok {
			return value
		}
	}
	return ""
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func printSuccess(jsonMode bool, data any) int {
	if jsonMode {
		_ = json.NewEncoder(os.Stdout).Encode(meshcore.Envelope{OK: true, Data: data})
		return 0
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 10
	}
	fmt.Println(string(b))
	return 0
}

func printFailure(jsonMode bool, apiErr *meshcore.APIError, code int) int {
	if apiErr == nil {
		apiErr = &meshcore.APIError{Code: "internal_error", Message: "unknown error", Retryable: false}
	}
	if jsonMode {
		_ = json.NewEncoder(os.Stdout).Encode(meshcore.Envelope{OK: false, Error: apiErr})
	} else {
		fmt.Fprintf(os.Stderr, "verlay: %s (%s)\n", apiErr.Message, apiErr.Code)
	}
	return code
}

func apiError(err error) *meshcore.APIError {
	var apiErr *meshcore.APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return &meshcore.APIError{Code: "internal_error", Message: err.Error(), Retryable: false}
}

func exitCode(err error) int {
	var apiErr *meshcore.APIError
	if !errors.As(err, &apiErr) {
		return 10
	}
	switch apiErr.Code {
	case "invalid_usage":
		return 2
	case "daemon_unavailable", "device_unavailable", "service_control_failed":
		return 3
	case "request_denied":
		return 4
	case "transfer_failed":
		return 5
	case "authentication_failed", "security_failure":
		return 6
	default:
		return 10
	}
}

func commandSchema() map[string]any {
	return map[string]any{
		"apiVersion":    meshcore.APIVersion,
		"globalOptions": []string{"--json", "--no-input", "--wait", "--timeout DURATION"},
		"commands": []string{
			"status", "doctor", "daemon start|stop|restart",
			"relay show|set HOST:PORT --password-stdin",
			"pair create|accept|list|approve|reject", "devices list|show|rename|revoke|authorize|require-approval", "schema", "version",
			"send DEVICE PATH [--exclude PATHS]", "transfers list|show|approve|reject|cancel",
			"skill print",
		},
		"securityBoundaries": []string{"pair approve", "pair reject", "transfers approve", "transfers reject", "devices authorize", "devices require-approval", "devices revoke", "relay set"},
		"transport": map[string]any{
			"current":            "live encrypted relay",
			"storesFiles":        false,
			"requiresBothOnline": true,
		},
		"exitCodes": map[string]int{"success": 0, "invalidUsage": 2, "unavailable": 3, "denied": 4, "transferFailed": 5, "securityFailure": 6, "internal": 10},
	}
}

const helpText = `verlay - secure cross-device CLI for Verlay

Usage:
  verlay [--json] [--no-input] [--wait] [--timeout 15s] COMMAND

Commands:
  status                         Show daemon and device status
  doctor                         Check local daemon security and health
  relay show                     Show the relay address without its password
  relay set HOST:PORT --password-stdin
                                 Configure a self-hosted relay, then restart
  daemon start|stop|restart      Control the Linux user service
  pair create                    Create a ten-minute pairing code
  pair accept CODE               Connect using a pairing code
  pair list                      List current pairing requests
  pair approve|reject ID         Decide a pending pairing request
  devices list                   List trusted devices
  devices show DEVICE_ID         Show one trusted device
  devices rename DEVICE_ID NAME  Assign a local name to a trusted device
  devices revoke DEVICE_ID       Revoke a trusted device
  devices authorize DEVICE --max-bytes N [--folders]
                                 Auto-receive within a bounded policy
  devices require-approval DEVICE
                                 Require manual approval again
  send DEVICE PATH [--exclude PATHS]
                                 Queue one file or folder transfer
  transfers list                 List transfers
  transfers show ID              Show one transfer
  transfers approve ID [--out]   Accept an incoming transfer
  transfers reject|cancel ID     Reject or cancel a transfer
  schema                         Print the machine command contract
  skill print                    Print the installed agent skill
  version                        Print build and protocol versions
`
