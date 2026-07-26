# Croc Transfer vs LocalSend benchmark

Date: 2026-07-22

This benchmark intentionally separates local-network speed from product reach. LocalSend and croc solve overlapping, but not identical, transfer problems.

## Test setup

- Laptop: Fedora Linux, Wi-Fi address `192.168.29.29`
- Phone: POCO `2201117PI`, Android 13, Wi-Fi address `192.168.29.59`
- Network: same 2.4 GHz Wi-Fi, phone reported a 72 Mbps link and approximately -50 dBm RSSI
- Payload: one 64 MiB incompressible random file (`67,108,864` bytes)
- Payload SHA-256: `2825e511019f4a826ef48dfbd6c0ec618a1c6fb3354c079af9dab93e60246a5f`
- Croc engine: v10.5.0
- LocalSend: v1.17.0, protocol v2.1

## Download/package size

These are the actual artifacts tested, not estimates. The Croc Android artifact is a debug APK, so this is not yet a final production-package comparison.

| Platform | Croc Transfer | LocalSend | Difference |
| --- | ---: | ---: | ---: |
| Linux AppImage | 111,901,176 bytes (106.72 MiB) | 49,558,720 bytes (47.26 MiB) | Croc is 59.46 MiB larger |
| Android APK | 16,149,063 bytes (15.40 MiB) | 17,491,108 bytes (16.68 MiB) | Croc is 1.28 MiB smaller |

The Croc Linux AppImage's extracted `AppDir` is about 298 MiB. Its largest bundled files include WebKitGTK (about 89 MiB), JavaScriptCoreGTK (about 33 MiB), ICU data (about 30 MiB), and the croc sidecar (about 10.4 MiB). Packaging—not the Tauri application binary alone—is the main current desktop size problem.

## Capability comparison

### Observed in source and official protocol documentation

| Criterion | Croc / current MVP | LocalSend 1.17 |
| --- | --- | --- |
| Same-LAN transfer | Sender starts a local relay and advertises it; the receiver attempts local discovery. The project contains a regression test that the local relay is preferred over an external relay. | Receiver exposes an HTTPS server; discovery normally uses multicast. No external server is required. |
| Different networks or cities | Supported by croc through a reachable relay, without port forwarding. The relay coordinates/connects peers and can carry encrypted transfer traffic. | Not a normal LocalSend use case. The documented protocol is local REST/HTTPS plus multicast or local IP scanning. A VPN that places both devices on one reachable network would be an extra setup. |
| Encryption | Croc documents end-to-end encryption using PAKE. A relay is not trusted with plaintext. | LocalSend uses HTTPS with a self-signed per-device certificate and fingerprinting on the local network. |
| External availability dependency | Remote transfers depend on a reachable public or self-hosted relay. Same-LAN transfers can use the sender's local relay. | Same-LAN transfers do not depend on cloud infrastructure, but discovery can be blocked by guest Wi-Fi/client isolation, multicast restrictions, or firewalls. |
| Protocol-level interrupted-file resume | The underlying croc engine compares chunks and requests missing chunks. | The documented v2.1 upload API sends a file as one binary POST and documents no offset/chunk resume endpoint. |

### Current MVP resume limitation

The underlying croc engine supports two useful forms of recovery:

1. Reconnection after a control or data connection drops while the same transfer process remains alive. Repository tests cover both cases.
2. Restarting a CLI transfer and resuming missing chunks from a compatible partial file.

The current GUI wrappers do **not** expose durable restart/resume correctly yet:

- Desktop receive passes `--overwrite`, which skips croc's resume prompt and missing-chunk reuse path.
- Android receive also passes `--overwrite` and creates a new UUID staging directory for each attempt, so a later attempt cannot locate the previous partial file.
- Android deletes its staging directory when the service finishes normally or is cancelled.

Therefore, durable resume after app restart, cancellation, reboot, or power loss is a product milestone—not a feature that should currently be advertised in the GUI.

## Transfer results

### Completed and valid

- LocalSend Linux-to-Android transferred the full 64 MiB payload.
- The SHA-256 checksum on Android exactly matched the source checksum.
- LocalSend Android receiver memory sampled during/after the run at roughly 162-171 MiB PSS and 276-287 MiB RSS.
- LocalSend Linux idle process was observed at roughly 177 MiB RSS.

### Not yet valid for a winner

The active timing collector was interrupted before its final timing output was retained. A subsequent matched croc run was then interrupted when the phone disconnected. Publishing a speed winner from those partial observations would be misleading.

The next valid run must repeat both products sequentially with the same payload and retain:

- preparation/pairing time, reported separately from data-transfer time;
- payload transfer seconds and MiB/s;
- source and destination SHA-256;
- receiver peak memory;
- local-route proof from logs;
- one deliberate Wi-Fi interruption;
- one remote-network test with laptop on Wi-Fi and phone on mobile data.

## Practical product interpretation

- LocalSend is the stronger baseline for effortless nearby transfer: automatic discovery, no relay dependency, and a much smaller current Linux download.
- Croc's defensible advantage is one simple pairing model that can work both nearby and across cities, plus an underlying chunk-resume design.
- The product should use local transfer automatically when possible, fall back to an end-to-end-encrypted managed relay when devices are apart, and explain this as “Nearby” versus “Anywhere”—not expose relay terminology by default.
- Before production, the GUI needs durable transfer records, persistent partial-file locations, automatic retry/resume, route/status messaging, relay health/failover, and a smaller Linux package.

## Preserved benchmark artifacts

No benchmark files were automatically deleted. This directory currently contains the official LocalSend test installers, the 64 MiB payload, and request metadata. The repository also includes the Android instrumentation hook used to start the private receive service repeatably.
