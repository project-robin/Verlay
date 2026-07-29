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
- Newly paired devices receive `files.request` and a fixed per-device inbox
  capped at 100 GiB per file or folder. Pairing is the human authorization.
- The receive policy can be reduced, expanded, or returned to manual approval.

The Linux identity is stored in the user's data directory with mode `0600`.
Android wraps its identity seed with Android Keystore and passes it only to the
app-private daemon process. Pairing codes and transfer secrets are not written
to the audit log. See [SECURITY.md](SECURITY.md) for boundaries and known gaps.

## Linux installation

For a complete per-user installation from any directory:

```sh
curl -fsSL https://raw.githubusercontent.com/project-robin/Verlay/main/scripts/bootstrap-agent.sh | bash
verlay status
verlay doctor
```

Running `./scripts/bootstrap-agent.sh` from a checkout builds that checkout
instead of downloading a release. Both paths install `~/.local/bin/verlay`,
enable `verlayd.service`, register the global Agent Skill, and verify the
command outside the repository. They require no sudo.
Run `./scripts/install-verlay-linux.sh --uninstall` to remove the binary and user
service; identity and trust data are deliberately preserved.

## Windows CLI friend test

Download, inspect, and run the native bootstrap from PowerShell:

```powershell
$installer = Join-Path $env:TEMP "bootstrap-verlay.ps1"
Invoke-WebRequest https://raw.githubusercontent.com/project-robin/Verlay/main/scripts/bootstrap-agent.ps1 -OutFile $installer
Get-Content $installer
& $installer
```

The remote bootstrap downloads and verifies the Windows release. From a source
checkout it builds the unchanged dependency graph instead. Both paths install
the CLI, daemon, and Agent Skill, add the CLI to the user PATH, register the
daemon with Windows Task Scheduler for login persistence, and verify the
runtime from an unrelated directory.
Because beta binaries are not code-signed, Windows may ask the user to confirm
running them.

For a manually downloaded release binary, keep the daemon open in one
PowerShell window:

```powershell
Copy-Item .\verlay-windows-amd64.exe .\verlayd.exe
.\verlayd.exe
```

Use a second PowerShell window for pairing and transfers:

```powershell
.\verlay-windows-amd64.exe --json add FOUR-WORD-CODE
.\verlay-windows-amd64.exe --json status
.\verlay-windows-amd64.exe --json send C:\Users\friend\Videos\clip.mp4 --to FEDORA
```

The bootstrap manages the background daemon. Do not restart it while a
pairing or transfer is active.

The Linux installer can be piped from the repository's raw GitHub URL and
downloads a checksum-verified binary. Set
`VERLAY_REPOSITORY=OWNER/REPOSITORY` when testing a fork.

## Pairing

On one device:

```sh
verlay --json add
```

Enter the returned code on the other device:

```sh
verlay --json add word-word-word-word
```

## Autonomous receiving

Pairing grants a dedicated inbox with a 100 GiB per-transfer limit. Return to
manual approval when needed:

```sh
verlay --json receive --ask EDITOR_TWO
```

Files from that device are delivered without a second click. Return to manual
approval while keeping the pairing with:

```sh
verlay --json receive --always EDITOR_TWO
```

Pairing, receive-policy changes, relay changes, and revocation are human
security boundaries. The embedded agent skill tells agents not to cross them
without explicit instruction.

## Sending files and folders

```sh
verlay --json status
verlay --json send /path/to/video.mp4 --to EDITOR_TWO
verlay --json send /path/to/project --to EDITOR_TWO --exclude .git,node_modules
verlay --json receive
verlay --json receive TRANSFER_ID
```

Sending waits for `completed` by default. A timeout does not cancel the durable
queue entry.

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
