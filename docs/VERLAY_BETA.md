# Verlay Linux–Android production beta

Verlay is the daemon-centered, CLI-first product built on Croc's transfer
foundation.
This milestone targets Linux, Android, and a minimal Windows CLI beta. macOS,
iOS, and native Windows service packaging are deliberately deferred.

## Current security model

- Each installation owns a persistent Ed25519 identity.
- A four-word, ten-minute PAKE code bootstraps pairing.
- Creating and entering the same short-lived code authorizes the pairing.
- Future control sessions use TLS 1.3 and pin the paired Ed25519 keys.
- Each transfer derives a distinct Croc secret from the paired relationship key
  and transfer ID. Secrets are delivered to the child process through private
  standard input, never through arguments, environment variables, or logs.
- The relay forwards live encrypted traffic. It stores no files and cannot read
  control messages or file contents; it can observe connection metadata.
- Newly paired devices receive only `files.request`. Incoming transfers require
  approval until a human grants that device a size-bounded receive policy.
- Auto-receive uses a fixed per-device inbox below the configured receive root;
  folder permission is granted separately.

The Linux identity is stored in the user's data directory with mode `0600`.
Android wraps its identity seed with Android Keystore and passes it only to the
app-private daemon process. Pairing codes and transfer secrets are not written
to the audit log. See [SECURITY.md](SECURITY.md) for boundaries and known gaps.

## Linux install from this checkout

From the repository root:

```sh
./scripts/install-verlay-linux.sh
verlay status
verlay doctor
```

This installs `~/.local/bin/verlay` and enables `verlayd.service` as a systemd user
service. It requires neither sudo nor a language runtime after installation.
Run `./scripts/install-verlay-linux.sh --uninstall` to remove the binary and user
service; identity and trust data are deliberately preserved.

## Windows CLI friend test

Until a binary release is published, clone the repository and run the native
bootstrap from PowerShell:

```powershell
.\scripts\bootstrap-agent.ps1
```

It builds the unchanged dependency graph, installs the CLI, daemon, and Agent
Skill, and verifies the runtime. It requires Go 1.21 or newer so Go can select
the toolchain declared by the repository. Do not lower the `go` directive or
downgrade dependencies to make an older local toolchain compile.

After a release is published, Windows can instead use
`verlay-windows-amd64.exe` from that release.
Because beta binaries are not code-signed, Windows may ask the user to confirm
running them.

Keep the daemon open in the first PowerShell window:

```powershell
Copy-Item .\verlay-windows-amd64.exe .\verlayd.exe
.\verlayd.exe
```

Use a second PowerShell window for pairing and transfers:

```powershell
.\verlay-windows-amd64.exe --json pair accept FOUR-WORD-CODE
.\verlay-windows-amd64.exe --json pair list
.\verlay-windows-amd64.exe --json devices list
.\verlay-windows-amd64.exe --json send FEDORA C:\Users\friend\Videos\clip.mp4 --wait --timeout 2h
```

For automatic Linux-to-Windows delivery, the Windows user must first grant the
paired Linux device a bounded policy with `devices authorize`. The Windows beta
does not install a background service; closing its daemon window makes that
computer unavailable.

After the first Verlay release, the same installer can be piped from the
repository's raw GitHub URL. It downloads a checksum-verified Linux binary. Set
`VERLAY_REPOSITORY=OWNER/REPOSITORY` when testing a fork.

## Pairing

On one device:

```sh
verlay --json pair create
```

Enter the returned code on the other device:

```sh
verlay --json pair accept word-word-word-word
verlay --json pair list
```

## Autonomous receiving

Pair once manually, then have the receiving user explicitly grant a bounded
policy:

```sh
verlay --json devices authorize EDITOR_TWO --max-bytes 10737418240 --folders
```

Files from that device are then delivered to its dedicated inbox without a
second click. Revoke the grant while keeping the pairing with:

```sh
verlay --json devices require-approval EDITOR_TWO
```

Pairing, authorization, relay changes, and revocation are human security
boundaries. The embedded agent skill tells agents not to cross them without
explicit instruction.

## Sending files and folders

```sh
verlay --json devices list
verlay --json send EDITOR_TWO /path/to/video.mp4
verlay --json send EDITOR_TWO /path/to/project --exclude .git,node_modules
verlay --json transfers list
verlay --json transfers approve TRANSFER_ID --out "$HOME/Downloads/Verlay"
```

Use `--wait --timeout 30m` on `verlay send` when an agent should remain attached
until the transfer reaches a terminal state. A wait timeout does not cancel the
durable queue entry.

The scheduler persists outgoing jobs, processes them FIFO, permits two
concurrent transfers globally, and permits one active transfer per peer. An
offline or interrupted receiver is retried with bounded exponential backoff.
The same transfer ID and staging directory are reused so Croc can reuse valid
partial chunks. The source is fingerprinted; a queued transfer fails closed if
the source changes.

## Connectivity, relay, and current limits

Both devices must be online at the same time. The current data path uses the
configurable public Croc relay for the control and file paths. There is no
hosted file storage. Direct mesh-owned LAN routing is the next transport task;
Croc's simultaneous local/public route race is intentionally disabled in the
daemon child until that route has deterministic tests.

Configure an owned Croc relay without exposing its password in shell history:

```sh
printf '%s\n' "$RELAY_PASSWORD" | verlay --json relay set relay.example.com:9009 --password-stdin
verlay daemon restart
```

Verlay supports regular files and native folder trees. It does not yet
provide store-and-forward delivery, remote command execution, GPU leasing,
distributed resource discovery, bandwidth quotas, or organization accounts.
Manual incoming approvals do not survive a receiver daemon restart; bounded
auto-receive policies do.

## Production infrastructure boundary

A production domain and owned relay are availability infrastructure, not cloud
file storage. The current public relay is appropriate for compatibility testing,
but the product cannot promise uptime, capacity, abuse controls, or stable
operations for infrastructure it does not own. A production deployment should
operate redundant relay endpoints under its own domain and publish health and
version information. File bytes remain end-to-end encrypted and are forwarded
live; durable storage is a separate, optional future product decision.

## Beta checklist

- [x] Preserve the Croc transport and relay foundation.
- [x] Add the standalone Go `verlay` CLI and single-instance `verlayd` daemon.
- [x] Persist Ed25519 identity and trusted-device relationships securely.
- [x] Add one-step four-word PAKE pairing.
- [x] Add TLS 1.3 pinned control sessions and policy-bounded receive approval.
- [x] Transfer files and native folder trees through the configurable Croc
  relay without putting secrets in arguments, environment variables, or logs.
- [x] Add a persistent FIFO queue, two global slots, one slot per peer, retry
  backoff, stable transfer IDs, and partial-chunk reuse.
- [x] Add stable JSON envelopes, schema output, exit codes, and an embedded agent
  skill with human-approval boundaries.
- [x] Add a no-sudo Linux installer and hardened systemd user service.
- [x] Integrate the same Go daemon into the Android app with Keystore-backed
  identity, a foreground availability service, pairing, device selection, and
  incoming transfer approval.
- [x] Verify Go unit/integration tests and physical Linux↔Android pairing and
  bidirectional single-file transfers.
- [ ] Physically verify folder transfer, interrupted-transfer recovery, two-peer
  concurrency, notification delivery, and reboot persistence.
- [ ] Add deterministic mesh-owned LAN discovery/direct transport, with relay
  fallback, before calling the data path LAN-first.
- [ ] Operate redundant owned relay endpoints with monitoring and abuse controls
  before making an availability promise.
