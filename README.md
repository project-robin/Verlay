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

The bootstrap uses a verified release binary when Go is unavailable. Until the
first Verlay release is published, install from a checkout with Go 1.25 or
newer:

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

Install the skill alone into all agent hosts supported by the Skills CLI:

```sh
npx skills add project-robin/Verlay \
  --skill verlay \
  --global \
  --agent '*' \
  --copy \
  --yes
```

The skill teaches an agent to operate Verlay; it does not silently pair a
device or grant receive permissions. Some agent hosts discover newly installed
skills only after starting a new session.

`verlay` is the supported public command.

## Pair two computers

Create a short-lived pairing code on the first device:

```sh
verlay --json pair create
```

Enter that code on the second device:

```sh
verlay --json pair accept WORD-WORD-WORD-WORD
```

The devices pair automatically after the second device enters the code:

```sh
verlay --json pair list
verlay --json devices list
```

Creating or entering a pairing code is an intentional human security boundary.
Pairing also authorizes files and folders from that device to its dedicated
inbox, up to 100 GiB per transfer. Agents should not pair without explicit
instruction.

## Send files and folders

Send a file and wait until delivery is verified:

```sh
verlay --json --wait --timeout 2h send EDITOR_TWO /path/to/video.mp4
```

Send a folder while excluding generated content:

```sh
verlay --json --wait --timeout 2h send EDITOR_TWO /path/to/project --exclude .git,node_modules
```

Inspect transfers:

```sh
verlay --json transfers list
verlay --json transfers show TRANSFER_ID
```

If that device was returned to manual approval, approve an incoming transfer:

```sh
verlay --json transfers approve TRANSFER_ID --out "$HOME/Downloads/Verlay"
```

## Receive policy

Pairing grants a 100 GiB-per-transfer trusted inbox by default. Change that
limit when needed:

```sh
verlay --json devices authorize EDITOR_TWO --max-bytes 10737418240 --folders
```

Return that device to manual approval without removing the pairing:

```sh
verlay --json devices require-approval EDITOR_TWO
```

Revoke the relationship completely:

```sh
verlay --json devices revoke EDITOR_TWO
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
Clone the repository, inspect scripts/bootstrap-agent.ps1, run it, verify Verlay
status and doctor, and tell me when it is ready. Do not edit go.mod or downgrade
dependencies. Stop before pairing or changing the relay so I can authorize
those decisions.
```

From a cloned checkout, the native bootstrap is:

```powershell
.\scripts\bootstrap-agent.ps1
```

It builds and installs `verlay.exe` and `verlayd.exe`, adds Verlay to the user
`PATH`, registers the Agent Skill, registers a user-level Task Scheduler job
that keeps the daemon running and starts it at login, and runs health checks. It
requires Go 1.21 or newer while the beta has
no published binary release; Go automatically selects the newer toolchain
declared by the repository. The script never edits `go.mod`.

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
verlay --json devices list
verlay --json transfers list
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
