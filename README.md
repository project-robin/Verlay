# Verlay

**A trusted path between machines.**

Verlay is a secure, permission-based device mesh for people and AI agents. Pair
devices once, keep their identities, and move files or folders between them
through a CLI designed for both interactive use and automation.

> **Beta:** Linux and Android are the currently tested platforms. A minimal
> Windows CLI is available for testing. macOS, iOS, and production relay
> infrastructure are future work.

## Why Verlay

Normal file-sharing workflows make people upload work to cloud storage, send a
link, wait for someone else to download it, and repeat. Verlay creates a trusted
relationship between devices so an approved person or agent can deliver work
directly to the intended machine.

The current beta provides:

- persistent device identities and memorable device names;
- manual, four-word PAKE pairing;
- files and native folder-tree transfers;
- explicit receive permissions with per-device size limits;
- a durable queue with retry and interrupted-transfer recovery;
- two concurrent transfers globally and one active transfer per peer;
- stable JSON responses, documented exit codes, schemas, and an embedded agent
  skill;
- a Linux user daemon and no-sudo installer;
- an Android foreground peer with Keystore-backed identity;
- encrypted live relay transport without cloud file storage.

## Platform status

| Platform | Current status |
| --- | --- |
| Linux | CLI and background daemon; physically tested with Android |
| Verlay Mobile (Android) | Functional beta app; bidirectional single-file transfer tested |
| Windows | CLI and login-started background daemon; physical verification pending |
| macOS and Verlay Mobile (iOS) | Planned, not currently supported |

Folder transfer, recovery after interruption, concurrent multi-peer transfers,
notification delivery, and reboot persistence still need complete physical
verification before a production claim.

## Give this to an agent on Linux or Windows

This is the platform-neutral installation instruction:

```text
Install Verlay from https://github.com/project-robin/Verlay.
First identify this computer's operating system and CPU architecture; do not
assume it is Linux. Clone the repository and read the matching installation
section in README.md. On Linux, inspect and run scripts/bootstrap-agent.sh. On
Windows, inspect and run scripts/bootstrap-agent.ps1 from PowerShell. Do not run
the Linux installer on Windows, do not edit go.mod, and do not downgrade
dependencies. Verify `verlay --json status` and `verlay --json doctor`, then
tell me it is ready. Stop before pairing or changing the relay so I can
authorize those security decisions. If the operating system is unsupported, stop and explain
that instead of improvising an installation.
```

## Install the Linux beta

Requirements:

- Linux with systemd user services;
- Git.

### Linux-specific agent instruction

Paste this instruction into Codex, Claude Code, Copilot, Cursor, or another
terminal-capable coding agent:

```text
Install Verlay on this Linux computer from https://github.com/project-robin/Verlay.
Inspect scripts/bootstrap-agent.sh before running it. Run the bootstrap,
verify verlay status and doctor, and tell me when it is ready. Stop before
pairing or changing the relay so I can authorize those security decisions.
```

The agent can clone the repository and complete the installation. The only
required interactive boundary is first-device pairing and permission approval.

### One-command setup

Install the runtime, start its Linux user daemon, register the standard Agent
Skill, and run health checks:

```sh
curl -fsSL https://raw.githubusercontent.com/project-robin/Verlay/main/scripts/bootstrap-agent.sh | bash
```

The remote bootstrap installs a checksum-verified release. Running the
bootstrap from a source checkout builds that checkout instead:

```sh
git clone https://github.com/project-robin/Verlay.git
cd Verlay
./scripts/bootstrap-agent.sh
```

The bootstrap installs the Verlay CLI as `~/.local/bin/verlay`, starts
`verlayd.service`, places the skill at `~/.agents/skills/verlay`, and uses the
Skills CLI when available to register it with detected agent hosts. It does not
require `sudo`.

Verify the installation:

```sh
verlay status
verlay doctor
```

Uninstall the binary and service while preserving device identity and pairing
data:

```sh
./scripts/install-verlay-linux.sh --uninstall
```

Replace a detected legacy `mesh` installation and intentionally discard its
old identity, pairings, settings, and transfer history:

```sh
./scripts/bootstrap-agent.sh --reset-legacy
```

This reset is never performed during an ordinary install or update, and it
does not delete received files.

Install only the skill into all agent hosts supported by the Skills CLI:

```sh
npx skills add project-robin/Verlay \
  --skill verlay \
  --global \
  --agent '*' \
  --copy \
  --yes
```

This skill-only command does **not** install the `verlay` executable or daemon.
Use the bootstrap for a complete installation. The skill teaches an agent to
operate Verlay; it does not silently pair a device or grant receive
permissions. Some agent hosts discover newly installed skills or PATH changes
only after starting a new session.

`verlay` is the supported public command.

## Pair two computers

Create a short-lived pairing code on the first device:

```sh
verlay --json add
```

Enter that code on the second device:

```sh
verlay --json add WORD-WORD-WORD-WORD
```

The second command waits until the devices are authenticated. Repeating
`verlay add` reuses the active code instead of creating competing requests.

Creating or entering a pairing code is an intentional human security boundary.
Pairing also authorizes files and folders from that device to its dedicated
inbox, up to 100 GiB per transfer. Agents should not pair without explicit
instruction.

## Send files and folders

Send a file and wait until delivery is verified:

```sh
verlay --json send /path/to/video.mp4 --to EDITOR_TWO
```

Send a folder while excluding generated content:

```sh
verlay --json send /path/to/project --to EDITOR_TWO --exclude .git,node_modules
```

If only one device is trusted, omit `--to`. Sending waits for verified delivery
by default. Queue without waiting only when intended:

```sh
verlay --json --background send /path/to/video.mp4 --to EDITOR_TWO
```

If that device was returned to manual approval, approve an incoming transfer:

```sh
verlay --json receive TRANSFER_ID
```

## Receive policy

Pairing grants a 100 GiB-per-transfer trusted inbox by default. Change that
device to manual approval when needed:

```sh
verlay --json receive --ask EDITOR_TWO
```

Allow automatic delivery again:

```sh
verlay --json receive --always EDITOR_TWO
```

Revoke the relationship completely:

```sh
verlay --json remove EDITOR_TWO
```

## Verlay Mobile beta

The Android app embeds the same Go Verlay runtime used by Linux. It can:

- create or enter pairing codes;
- remain available through a visible foreground service;
- send a selected document to a paired computer;
- approve or reject incoming transfers;
- publish received files to `Downloads/Verlay`.

Build instructions and requirements are in
[apps/android/README.md](apps/android/README.md).

## Windows beta

Give this Windows-specific instruction to a terminal-capable coding agent:

```text
Install Verlay on this Windows computer from https://github.com/project-robin/Verlay.
Download and inspect scripts/bootstrap-agent.ps1, run it, verify Verlay status
and doctor, and tell me when it is ready. Do not edit go.mod or downgrade
dependencies. Stop before pairing or changing the relay so I can authorize
those decisions.
```

Download, inspect, and run the native bootstrap:

```powershell
$installer = Join-Path $env:TEMP "bootstrap-verlay.ps1"
Invoke-WebRequest https://raw.githubusercontent.com/project-robin/Verlay/main/scripts/bootstrap-agent.ps1 -OutFile $installer
Get-Content $installer
& $installer
```

The remote bootstrap downloads a checksum-verified release. When run from a
cloned checkout, it builds that checkout instead. It installs `verlay.exe` and
`verlayd.exe`, adds Verlay to the user `PATH`, registers the Agent Skill with
detected agent hosts, registers a user-level Task Scheduler job that keeps the
daemon running and starts it at login, and runs health checks. The script never
edits `go.mod`.

GitHub releases will contain `verlay-windows-amd64.exe`. Copy it to
`verlayd.exe` to run the same binary in daemon mode:

```powershell
Copy-Item .\verlay-windows-amd64.exe .\verlayd.exe
.\verlayd.exe
```

Physical Linux-to-Windows verification remains outstanding. See
[docs/VERLAY_BETA.md](docs/VERLAY_BETA.md) for the complete friend-test workflow.

## Agent interface

Verlay is CLI-first so an AI agent can use it without operating a desktop UI.
Automation features include:

```sh
verlay --json status
verlay --json add
verlay --json send /path/to/file --to DEVICE
verlay --json receive
verlay --json devices
verlay --json transfers
verlay --json doctor
verlay --json schema
verlay skill print
```

Commands return stable JSON envelopes and meaningful process exit codes. The
embedded skill documents which actions require human authorization.

## Architecture

```text
Person or AI agent
        |
     verlay CLI
        |
  local verlayd daemon
        |
authenticated control channel
        |
 encrypted live relay transport
        |
 remote verlayd or Verlay Mobile peer
```

- The daemon owns identity, trust relationships, permissions, queues, and
  transfer state.
- Pairing uses a short-lived PAKE code and persistent Ed25519 identities.
- Later control sessions use TLS 1.3 and pin the paired device identity.
- Each transfer receives a distinct secret derived from the trusted
  relationship and transfer ID.
- The relay forwards encrypted live traffic. It does not store files or possess
  the keys required to read their contents.

Both devices currently need to be online at the same time. The beta defaults to
the public Croc relay, which is suitable for development and compatibility
testing but is not infrastructure controlled by the Verlay project. The relay
is configurable, and production deployment requires independently operated,
monitored relay endpoints.

## Repository layout

```text
cmd/verlay/          Verlay CLI
internal/mesh/     daemon, identity, pairing, permissions, queue, and transfers
skills/verlay/     portable Agent Skill and focused operating references
apps/android/      Android beta
packaging/linux/   systemd user service
scripts/           installation tooling
docs/              security model and beta operations
src/               Croc-derived encrypted transfer foundation
```

## Documentation

- [Beta operations and limitations](docs/VERLAY_BETA.md)
- [Security model](docs/SECURITY.md)
- [Android build and testing](apps/android/README.md)
- [LocalSend comparison methodology](benchmarks/localsend-vs-croc/README.md)

## Built on Croc

Verlay originated from [Croc](https://github.com/schollz/croc), created by Zack
Scholl and its contributors. Croc provides the encrypted relay-based transfer
foundation used internally by Verlay.

Verlay adds persistent device identity, trusted pairing, permissions, a daemon,
durable queues, an agent-oriented JSON interface, Android integration, and
product-specific packaging. Verlay is an independent
project and is not affiliated with or endorsed by the original Croc project.

The original copyright notices are preserved. Both the original work and
Verlay modifications are distributed under the MIT License. See
[LICENSE](LICENSE) and [NOTICE](NOTICE).

## Contributing

Verlay is currently a production-oriented beta. Bug reports should include the
platforms involved, `verlay doctor` output with secrets removed, the transfer
direction, and whether the failure occurred during pairing, connection, or
transfer.

Run `./scripts/test-local-two-device.sh` to exercise two isolated Verlay
installations and a real local relay on one computer. It verifies pairing,
automatic and manual receiving, files and folders, hashes, daemon restarts,
durable retry, and relay reconnection without mocks.
