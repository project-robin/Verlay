package main

import (
	"strings"
	"testing"
	"time"

	"github.com/project-robin/Verlay/skills"
)

func TestParseOptionsAnywhere(t *testing.T) {
	args, options, err := parseOptions([]string{"status", "--json", "--timeout=4s"})
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] != "status" || !options.json || options.timeout != 4*time.Second {
		t.Fatalf("unexpected parse result: args=%v options=%#v", args, options)
	}
}

func TestParseReceiveAuthorization(t *testing.T) {
	device, maxBytes, folders, err := parseReceiveAuthorization([]string{"Editor Two", "--folders", "--max-bytes", "4096"})
	if err != nil {
		t.Fatal(err)
	}
	if device != "Editor Two" || maxBytes != 4096 || !folders {
		t.Fatalf("unexpected authorization: device=%q max=%d folders=%v", device, maxBytes, folders)
	}
	if _, _, _, err := parseReceiveAuthorization([]string{"peer", "--max-bytes", "0"}); err == nil {
		t.Fatal("zero byte policy was accepted")
	}
}

func TestParseSendFolderExclusions(t *testing.T) {
	device, path, exclusions, err := parseSendArgs([]string{"editor-2", "/work/project", "--exclude", "node_modules,.git"})
	if err != nil {
		t.Fatal(err)
	}
	if device != "editor-2" || path != "/work/project" || strings.Join(exclusions, ",") != "node_modules,.git" {
		t.Fatalf("unexpected send parse: device=%q path=%q exclusions=%v", device, path, exclusions)
	}
}

func TestParseSimpleSend(t *testing.T) {
	device, path, exclusions, err := parseSendArgs([]string{"/work/result.zip", "--to", "Editor Two", "--exclude", ".git"})
	if err != nil {
		t.Fatal(err)
	}
	if device != "Editor Two" || path != "/work/result.zip" || strings.Join(exclusions, ",") != ".git" {
		t.Fatalf("unexpected send parse: device=%q path=%q exclusions=%v", device, path, exclusions)
	}
	device, path, _, err = parseSendArgs([]string{"/work/result.zip"})
	if err != nil || device != "" || path != "/work/result.zip" {
		t.Fatalf("single-device send parse: device=%q path=%q err=%v", device, path, err)
	}
}

func TestSendWaitsByDefault(t *testing.T) {
	_, options, err := parseOptions([]string{"send", "result.zip"})
	if err != nil || !options.wait {
		t.Fatalf("default options = %#v, err=%v", options, err)
	}
	_, options, err = parseOptions([]string{"send", "result.zip", "--background"})
	if err != nil || options.wait {
		t.Fatalf("background options = %#v, err=%v", options, err)
	}
}

func TestAgentSkillAndSchemaExposeAutomationContract(t *testing.T) {
	schema := commandSchema()
	commands, ok := schema["commands"].([]string)
	if !ok {
		t.Fatalf("commands schema has type %T", schema["commands"])
	}
	joined := strings.Join(commands, "\n")
	for _, command := range []string{
		"add [CODE]", "send PATH", "receive [TRANSFER", "remove DEVICE",
		"devices [DEVICE]", "transfers [TRANSFER]", "doctor", "schema",
	} {
		if !strings.Contains(joined, command) {
			t.Fatalf("schema omitted %q", command)
		}
	}
	skill := skills.Verlay()
	for _, guidance := range []string{
		"Never run them",
		"verlay --json",
		"Always resolve the destination",
		"device ID explicitly with `--to`",
		"never guess from recency",
		"`waiting_for_peer`; that is not a completed pairing",
		"Only report an incoming item as received when its status is",
		"cannot wake an idle agent",
		"`device_revoked`",
	} {
		if !strings.Contains(skill, guidance) {
			t.Fatalf("embedded skill omitted %q", guidance)
		}
	}
}

func TestInvocationArgsUsesVerlaydAsDaemon(t *testing.T) {
	args := invocationArgs("/usr/bin/verlayd", nil)
	if strings.Join(args, " ") != "daemon run" {
		t.Fatalf("verlayd args = %v, want daemon run", args)
	}
	args = invocationArgs(`C:\Program Files\Verlay\verlayd.exe`, nil)
	if strings.Join(args, " ") != "daemon run" {
		t.Fatalf("verlayd.exe args = %v, want daemon run", args)
	}
	args = invocationArgs("verlayd-windows-amd64.exe", nil)
	if strings.Join(args, " ") != "daemon run" {
		t.Fatalf("release verlayd args = %v, want daemon run", args)
	}
	args = invocationArgs("verlayd-windows-amd64.exe", []string{"internal-transfer", "send"})
	if strings.Join(args, " ") != "internal-transfer send" {
		t.Fatalf("verlayd internal args = %v, want internal transfer passthrough", args)
	}
	if args := invocationArgs("/usr/bin/verlay", []string{"status"}); strings.Join(args, " ") != "status" {
		t.Fatalf("verlay args = %v, want status", args)
	}
}

func TestParseOptionsRejectsBadTimeout(t *testing.T) {
	if _, _, err := parseOptions([]string{"--timeout", "never", "status"}); err == nil {
		t.Fatal("bad timeout was accepted")
	}
}

func TestParseOptionsSupportsEndOfOptions(t *testing.T) {
	args, options, err := parseOptions([]string{"--json", "send", "device", "--", "--timeout"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.json || len(args) != 3 || args[2] != "--timeout" {
		t.Fatalf("unexpected parse result: args=%v options=%#v", args, options)
	}
}
