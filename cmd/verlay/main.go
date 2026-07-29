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
	"github.com/project-robin/Verlay/skills"
)

var version = "dev"

type options struct {
	json       bool
	wait       bool
	timeout    time.Duration
	timeoutSet bool
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
			return printSuccess(true, map[string]any{"skill": skills.Verlay()})
		}
		fmt.Print(skills.Verlay())
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
	case "add":
		return runAdd(args[1:], opts, paths)
	case "receive":
		return runReceive(args[1:], opts, paths)
	case "cancel":
		if len(args) != 2 {
			return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay cancel TRANSFER", Retryable: false}, 2)
		}
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/transfers/"+url.PathEscape(args[1])+"/cancel", map[string]any{})
	case "remove":
		if len(args) != 2 {
			return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay remove DEVICE", Retryable: false}, 2)
		}
		return callDaemon(opts, paths, http.MethodDelete, "/v1/devices/"+url.PathEscape(args[1]))
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
		return runWindowsDaemon(args[0], opts)
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

func runWindowsDaemon(action string, opts options) int {
	runTask := func(argument string) error {
		output, err := exec.Command("schtasks.exe", argument, "/TN", "Verlay").CombinedOutput()
		if err == nil {
			return nil
		}
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	if action == "stop" || action == "restart" {
		if err := runTask("/End"); err != nil && action == "stop" {
			return printFailure(opts.json, &meshcore.APIError{Code: "service_control_failed", Message: err.Error(), Retryable: true}, 3)
		}
	}
	if action == "start" || action == "restart" {
		if err := runTask("/Run"); err != nil {
			return printFailure(opts.json, &meshcore.APIError{Code: "service_control_failed", Message: err.Error() + "; rerun the Windows bootstrap to install the Verlay task", Retryable: true}, 3)
		}
	}
	return printSuccess(opts.json, map[string]any{"action": action, "service": "Verlay"})
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
	if !opts.timeoutSet {
		opts.timeout = 2 * time.Hour
	}
	return waitForTransfer(opts, paths, transferID)
}

func parseSendArgs(args []string) (device, path string, exclusions []string, err error) {
	var positionals []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to":
			if i+1 >= len(args) {
				return "", "", nil, errors.New("--to requires a device")
			}
			i++
			device = args[i]
		case "--exclude":
			if i+1 >= len(args) {
				return "", "", nil, errors.New("--exclude requires paths")
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
		default:
			positionals = append(positionals, args[i])
		}
	}
	if device != "" && len(positionals) == 1 {
		return device, positionals[0], exclusions, nil
	}
	if device == "" && len(positionals) == 1 {
		return "", positionals[0], exclusions, nil
	}
	// Keep the old DEVICE PATH form as a compatibility alias.
	if device == "" && len(positionals) == 2 {
		return positionals[0], positionals[1], exclusions, nil
	}
	return "", "", nil, errors.New("usage: verlay send PATH [--to DEVICE] [--exclude PATHS]")
}

func runAdd(args []string, opts options, paths meshcore.Paths) int {
	if len(args) == 0 {
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/pairings/create", map[string]any{})
	}
	if len(args) != 1 {
		return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay add [CODE]", Retryable: false}, 2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()
	client := meshcore.NewClient(paths.SocketFile, opts.timeout)
	envelope, err := client.Call(ctx, http.MethodPost, "/v1/pairings/accept", map[string]any{"code": args[0]})
	if err != nil {
		return printFailure(opts.json, apiError(err), exitCode(err))
	}
	if !envelope.OK {
		return printFailure(opts.json, envelope.Error, exitCode(envelope.Error))
	}
	if !opts.wait {
		return printSuccess(opts.json, envelope.Data)
	}
	pairingID := dataString(envelope.Data, "pairingId")
	if !opts.timeoutSet {
		opts.timeout = 10 * time.Minute
	}
	return waitForPairing(opts, paths, pairingID)
}

func runReceive(args []string, opts options, paths meshcore.Paths) int {
	if len(args) == 0 {
		return callDaemon(opts, paths, http.MethodGet, "/v1/transfers")
	}
	if len(args) == 2 && (args[0] == "--always" || args[0] == "--ask") {
		body := map[string]any{"autoReceive": args[0] == "--always"}
		if args[0] == "--always" {
			body["maxBytes"] = meshcore.DefaultTrustedTransferLimit
			body["allowFolders"] = true
		}
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/devices/"+url.PathEscape(args[1])+"/receive-policy", body)
	}
	reject := len(args) == 2 && args[1] == "--reject"
	if len(args) != 1 && !reject {
		return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay receive [TRANSFER [--reject]] | --always DEVICE | --ask DEVICE", Retryable: false}, 2)
	}
	return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/transfers/"+url.PathEscape(args[0])+"/decision", map[string]any{"approve": !reject})
}

func runTransfers(args []string, opts options, paths meshcore.Paths) int {
	if len(args) == 0 {
		return callDaemon(opts, paths, http.MethodGet, "/v1/transfers")
	}
	if len(args) == 1 && args[0] == "list" {
		return callDaemon(opts, paths, http.MethodGet, "/v1/transfers")
	}
	if len(args) == 1 {
		return callDaemon(opts, paths, http.MethodGet, "/v1/transfers/"+url.PathEscape(args[0]))
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
	if len(args) == 0 {
		return callDaemon(opts, paths, http.MethodGet, "/v1/devices")
	}
	if len(args) == 1 && args[0] == "list" {
		return callDaemon(opts, paths, http.MethodGet, "/v1/devices")
	}
	if len(args) == 1 {
		return callDaemon(opts, paths, http.MethodGet, "/v1/devices/"+url.PathEscape(args[0]))
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
	if len(args) == 0 {
		return callDaemon(opts, paths, http.MethodGet, "/v1/pairings")
	}
	if len(args) == 1 && args[0] == "create" {
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/pairings/create", map[string]any{})
	}
	if len(args) == 2 && args[0] == "accept" {
		return callDaemonWithBody(opts, paths, http.MethodPost, "/v1/pairings/accept", map[string]any{"code": args[1]})
	}
	if len(args) == 1 && args[0] == "list" {
		return callDaemon(opts, paths, http.MethodGet, "/v1/pairings")
	}
	return printFailure(opts.json, &meshcore.APIError{Code: "invalid_usage", Message: "usage: verlay pair create|accept CODE|list", Retryable: false}, 2)
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
	opts := options{wait: true, timeout: 15 * time.Second}
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--":
			filtered = append(filtered, args[i+1:]...)
			return filtered, opts, nil
		case args[i] == "--json":
			opts.json = true
		case args[i] == "--wait":
			opts.wait = true
		case args[i] == "--background":
			opts.wait = false
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
			opts.timeoutSet = true
		case strings.HasPrefix(args[i], "--timeout="):
			duration, err := time.ParseDuration(strings.TrimPrefix(args[i], "--timeout="))
			if err != nil || duration <= 0 {
				return nil, opts, errors.New("--timeout must be a positive duration such as 10s")
			}
			opts.timeout = duration
			opts.timeoutSet = true
		default:
			filtered = append(filtered, args[i])
		}
	}
	return filtered, opts, nil
}

func waitForPairing(opts options, paths meshcore.Paths, pairingID string) int {
	deadline := time.Now().Add(opts.timeout)
	client := meshcore.NewClient(paths.SocketFile, opts.timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return printFailure(opts.json, &meshcore.APIError{
				Code: "timeout", Message: "timed out waiting for the other device; the pairing request remains active", Retryable: true,
				Details: map[string]any{"pairingId": pairingID},
			}, 3)
		}
		ctx, cancel := context.WithTimeout(context.Background(), minDuration(remaining, 5*time.Second))
		envelope, err := client.Call(ctx, http.MethodGet, "/v1/pairings", nil)
		cancel()
		if err != nil {
			if apiError(err).Code == "daemon_unavailable" {
				time.Sleep(250 * time.Millisecond)
				continue
			}
			return printFailure(opts.json, apiError(err), exitCode(err))
		}
		if pairing := pairingData(envelope.Data, pairingID); pairing != nil {
			status := dataString(pairing, "status")
			if status == "paired" {
				return printSuccess(opts.json, pairing)
			}
			if status == "failed" {
				return printFailure(opts.json, &meshcore.APIError{
					Code: dataString(pairing, "errorCode"), Message: dataString(pairing, "errorMessage"), Retryable: false,
				}, 6)
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func pairingData(data any, pairingID string) map[string]any {
	object, _ := data.(map[string]any)
	items, _ := object["pairings"].([]any)
	for _, item := range items {
		pairing, _ := item.(map[string]any)
		if dataString(pairing, "pairingId") == pairingID {
			return pairing
		}
	}
	return nil
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
			if apiError(err).Code == "daemon_unavailable" {
				time.Sleep(250 * time.Millisecond)
				continue
			}
			return printFailure(opts.json, apiError(err), exitCode(err))
		}
		status := dataString(envelope.Data, "status")
		if status == "completed" {
			return printSuccess(opts.json, envelope.Data)
		}
		if status == "failed" || status == "rejected" || status == "cancelled" {
			code := dataString(envelope.Data, "errorCode")
			if code == "" {
				code = "transfer_" + status
			}
			return printFailure(opts.json, &meshcore.APIError{Code: code, Message: dataString(envelope.Data, "message"), Retryable: false}, 5)
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
		"globalOptions": []string{"--json", "--background", "--timeout DURATION"},
		"commands": []string{
			"add [CODE]", "send PATH [--to DEVICE] [--exclude PATHS]", "receive [TRANSFER [--reject]]",
			"receive --always|--ask DEVICE", "cancel TRANSFER", "remove DEVICE",
			"status", "devices [DEVICE]", "transfers [TRANSFER]", "pair", "doctor", "version", "schema",
		},
		"administrativeCommands": []string{
			"daemon start|stop|restart", "relay show|set HOST:PORT --password-stdin",
			"devices rename|authorize|require-approval", "skill print",
		},
		"compatibilityCommands": []string{
			"pair create|accept|list", "devices list|show|rename|revoke|authorize|require-approval", "schema", "version",
			"send DEVICE PATH [--exclude PATHS]", "transfers list|show|approve|reject|cancel",
		},
		"securityBoundaries": []string{
			"add [CODE]", "receive TRANSFER [--reject]", "receive --always|--ask DEVICE",
			"remove DEVICE", "devices authorize", "devices require-approval", "relay set",
		},
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
  verlay [--json] [--background] [--timeout DURATION] COMMAND

Commands:
  add [CODE]                     Add a trusted device
  send PATH [--to DEVICE]       Send and verify delivery
  receive [TRANSFER]            Show incoming items or accept one
  receive TRANSFER --reject     Reject an incoming item
  receive --always DEVICE       Automatically receive from a device
  receive --ask DEVICE          Ask before receiving from a device
  status                        Show devices, connections, and transfers
  cancel TRANSFER               Cancel a transfer
  remove DEVICE                 Remove a trusted device

Inspect:
  status                        Show the complete operational overview
  devices [DEVICE]              List devices or inspect one
  transfers [TRANSFER]          List transfers or inspect one
  pair                          Show active add-device requests
  doctor                        Run local health and security checks
  schema                        Show every command and automation contract
  version                       Show build and protocol versions

Options:
  --background                  Queue without waiting for completion
  --json                        Print the stable machine-readable result
  --timeout DURATION            Limit how long to wait

Administrative and compatibility commands remain available in "verlay schema".
`
