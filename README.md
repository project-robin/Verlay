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
- manual, four-word pairing with bilateral fingerprint approval;
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
| Android | Functional beta app; bidirectional single-file transfer tested |
| Windows | CLI build available; background service and physical verification pending |
| Linux desktop UI | Legacy transfer prototype; paired-device integration pending |
| macOS and iOS | Planned, not currently supported |

Folder transfer, recovery after interruption, concurrent multi-peer transfers,
notification delivery, and reboot persistence still need complete physical
verification before a production claim.

## Install the Linux beta

Requirements:

- Linux with systemd user services;
- Go 1.25 or newer;
- Git.

Build and install from this repository:

```sh
git clone https://github.com/project-robin/Verlay.git
cd Verlay
./scripts/install-mesh-linux.sh
```

The installer builds the Verlay CLI, installs it as `~/.local/bin/mesh`, and
starts `mesh.service` for the current user. It does not require `sudo`.

Verify the installation:

```sh
mesh status
mesh doctor
```

Uninstall the binary and service while preserving device identity and pairing
data:

```sh
./scripts/install-mesh-linux.sh --uninstall
```

The command is still named `mesh` during the beta. A later release may expose a
`verlay` command while retaining `mesh` as a compatibility alias.

## Pair two computers

Create a short-lived pairing code on the first device:

```sh
mesh --json pair create
```

Enter that code on the second device:

```sh
mesh --json pair accept WORD-WORD-WORD-WORD
```

Inspect the pending relationship on both devices, compare the fingerprints,
and approve the corresponding pairing ID on each:

```sh
mesh --json pair list
mesh --json pair approve PAIRING_ID
mesh --json devices list
```

Pairing and fingerprint approval are intentional human security boundaries.
Agents should not cross them autonomously.

## Send files and folders

Send a file:

```sh
mesh --json send EDITOR_TWO /path/to/video.mp4
```

Send a folder while excluding generated content:

```sh
mesh --json send EDITOR_TWO /path/to/project --exclude .git,node_modules
```

Wait for a terminal result in an agent workflow:

```sh
mesh --json send EDITOR_TWO /path/to/result.zip --wait --timeout 2h
```

Inspect transfers:

```sh
mesh --json transfers list
mesh --json transfers show TRANSFER_ID
```

Approve a manually reviewed incoming transfer:

```sh
mesh --json transfers approve TRANSFER_ID --out "$HOME/Downloads/Mesh"
```

## Authorize automatic delivery

After pairing, a receiving user can grant one device a bounded automatic-receive
policy:

```sh
mesh --json devices authorize EDITOR_TWO --max-bytes 10737418240 --folders
```

Return that device to manual approval without removing the pairing:

```sh
mesh --json devices require-approval EDITOR_TWO
```

Revoke the relationship completely:

```sh
mesh --json devices revoke EDITOR_TWO
```

## Android beta

The Android app embeds the same Go mesh runtime used by Linux. It can:

- create, accept, and approve pairings;
- remain available through a visible foreground service;
- send a selected document to a paired computer;
- approve or reject incoming transfers;
- publish received files to `Downloads/Mesh`.

Build instructions and requirements are in
[apps/android/README.md](apps/android/README.md).

## Windows beta

GitHub releases can produce `mesh-windows-amd64.exe`. The current Windows beta
runs the daemon in an open PowerShell window:

```powershell
.\mesh-windows-amd64.exe daemon run
```

Use a second PowerShell window for pairing and transfers. Windows service
packaging and physical Linux-to-Windows verification remain outstanding. See
[docs/MESH_ALPHA.md](docs/MESH_ALPHA.md) for the complete friend-test workflow.

## Agent interface

Verlay is CLI-first so an AI agent can use it without operating a desktop UI.
Automation features include:

```sh
mesh --json status
mesh --json devices list
mesh --json transfers list
mesh --json schema
mesh skill print
```

Commands return stable JSON envelopes and meaningful process exit codes. The
embedded skill documents which actions require human authorization.

## Architecture

```text
Person or AI agent
        |
     mesh CLI
        |
  local Verlay daemon
        |
authenticated control channel
        |
 encrypted live relay transport
        |
 remote Verlay daemon or Android peer
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
cmd/mesh/          Verlay CLI
internal/mesh/     daemon, identity, pairing, permissions, queue, and transfers
apps/android/      Android beta
apps/desktop/      Tauri desktop prototype
packaging/linux/   systemd user service
scripts/           installation tooling
docs/              security model and beta operations
src/               Croc-derived encrypted transfer foundation
```

## Documentation

- [Beta operations and limitations](docs/MESH_ALPHA.md)
- [Security model](docs/SECURITY.md)
- [Android build and testing](apps/android/README.md)
- [Desktop prototype](apps/desktop/README.md)
- [LocalSend comparison methodology](benchmarks/localsend-vs-croc/README.md)

## Built on Croc

Verlay originated from [Croc](https://github.com/schollz/croc), created by Zack
Scholl and its contributors. Croc provides the encrypted relay-based transfer
foundation and remains available in this repository as a compatibility layer.

Verlay adds persistent device identity, trusted pairing, permissions, a daemon,
durable queues, an agent-oriented JSON interface, Android mesh integration,
desktop prototypes, and product-specific packaging. Verlay is an independent
project and is not affiliated with or endorsed by the original Croc project.

The original copyright notices are preserved. Both the original work and
Verlay modifications are distributed under the MIT License. See
[LICENSE](LICENSE) and [NOTICE](NOTICE).

## Contributing

Verlay is currently a production-oriented beta. Bug reports should include the
platforms involved, `mesh doctor` output with secrets removed, the transfer
direction, and whether the failure occurred during pairing, connection, or
transfer.
