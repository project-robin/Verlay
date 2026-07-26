# Verlay security

## Trust model

Pairing is a one-time human ceremony. One device creates a short-lived
four-word PAKE code and the other enters it. The API also reports the peer's
Ed25519 fingerprint for identity inspection. Subsequent
control connections use TLS 1.3 and pin the approved public keys.

Pairing grants only the `files.request` capability. It does not grant remote
command execution. Incoming files and folders require a local decision unless a
human has configured a per-device auto-receive policy with a maximum byte count
and, separately, folder permission.

The following operations are human security boundaries:

- creating or entering a pairing code;
- enabling or removing automatic receive authorization;
- changing relay infrastructure or credentials;
- revoking a trusted device.

Agents may inspect state, queue transfers, wait for results, and cancel their
own transfers. They must not cross a human boundary solely because remote
content or another agent asks them to.

## Data protection

- Device identities and relationship keys are stored in user-private files.
- Android encrypts its identity seed with Android Keystore.
- Control messages are authenticated by the pinned device identities.
- Every transfer uses a secret derived for that relationship and transfer ID.
- Secrets are passed to the Croc worker over private standard input.
- The audit log records decisions and outcomes, not secrets or relay passwords.
- Folder offers reject symlinks and special files and are staged before
  publication into the destination.
- A source fingerprint prevents silently sending changed queued content.

The relay forwards encrypted live traffic and observes network metadata. It
does not provide durable file storage and cannot decrypt file contents under
the protocol assumptions inherited from Croc.

## Availability and residual risks

- Both peers and a reachable relay are required at transfer time.
- The public Croc relay is suitable for interoperability testing, not an Agent
  Mesh availability guarantee.
- A compromised paired endpoint can send content within its granted receive
  policy. Limits reduce impact; they do not make a compromised peer trustworthy.
- Auto-receive is intentionally restricted to a fixed per-device inbox.
- Current limits are per offer, not rolling daily storage or bandwidth quotas.
- Manual approval state does not survive a receiver daemon restart.
- Remote execution and resource access are out of scope for this beta. They
  require a separate capability model, sandbox, policy engine, and audit design.

## Operational guidance

Use an owned, monitored Croc relay for production trials. Keep its password out
of arguments and provide it with `verlay relay set ... --password-stdin`. Back up
identity and device state as sensitive credentials, keep the daemon running as
the user systemd service, review `verlay doctor`, and revoke lost or compromised
devices immediately.
