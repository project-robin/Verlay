# Verlay command reference

Load this reference when operating or diagnosing an installed Verlay runtime.
The installed binary remains authoritative; use `verlay --json schema` to inspect
its exact command and exit-code contract.

## Installation recovery

Installing this skill alone does not install the `verlay` executable. If
`command -v verlay` or `Get-Command verlay` fails, use the full bootstrap from
the repository. It installs the per-user CLI, background daemon, PATH entry,
and global skill together. After installation, verify the command from a
directory outside the repository before continuing the original operation.

## Read-only discovery

```sh
verlay --json version
verlay --json schema
verlay --json status
verlay --json doctor
verlay --json relay show
verlay --json pair
verlay --json devices
verlay --json devices DEVICE
verlay --json transfers
verlay --json transfers TRANSFER
```

## Pairing and device names

```sh
verlay --json add
verlay --json add FOUR-WORD-CODE
verlay --json devices rename DEVICE_ID FRIENDLY_NAME
verlay --json remove DEVICE
```

Pairing codes expire after ten minutes and are not long-term credentials.
Friendly names are local labels; the stable device ID is the unambiguous
automation identifier. Revocation is also local: run it on both devices when
the human wants the relationship removed from both device lists.
The daemon must remain running until pairing finishes; do not create a second
code or restart the daemon while the first request is active.

## Transfer lifecycle

```sh
verlay --json send /absolute/path --to DEVICE
verlay --json send /absolute/folder --to DEVICE --exclude .git,node_modules
verlay --json receive TRANSFER_ID
verlay --json receive TRANSFER_ID --reject
verlay --json receive --always DEVICE
verlay --json receive --ask DEVICE
verlay --json cancel TRANSFER_ID
```

A send waits for verified delivery by default and is persisted in the local
queue. Use `--background` only to return after queueing. Both devices need to be
online simultaneously for bytes to move. If a peer or network is temporarily
unavailable, the daemon retries the persisted transfer. A `--wait` timeout
stops that CLI wait but does not cancel the job.

Use `transfers show` until the state becomes one of:

- `completed`: delivery finished successfully;
- `failed`: delivery failed and may be retryable;
- `rejected`: the receiver rejected it;
- `cancelled`: the sender cancelled it.

## JSON and exit codes

Success envelope:

```json
{"ok":true,"data":{}}
```

Failure envelope:

```json
{"ok":false,"error":{"code":"daemon_unavailable","message":"...","retryable":true}}
```

Documented exit codes:

- `0`: success;
- `2`: invalid usage;
- `3`: daemon, service, or device unavailable;
- `4`: request denied;
- `5`: transfer failed;
- `6`: authentication or security failure;
- `10`: internal failure.

## Diagnostics

If `status` reports `daemon_unavailable` on Linux:

```sh
systemctl --user status verlayd.service
journalctl --user -u verlayd.service --no-pager -n 100
verlay --json doctor
```

On Windows, inspect the `Verlay` scheduled task and run
`verlay --json daemon start`. Rerun `scripts/bootstrap-agent.ps1` if the task is
missing. Do not repeatedly restart the daemon during pairing or transfer.

If a transfer is queued:

1. Verify both daemons are online.
2. Verify the destination device ID with `devices list`.
3. Inspect `transfers show`.
4. Check the relay address with `relay show`.
5. Run `doctor` on both devices.

Do not expose relay passwords or identity material when sharing diagnostics.
