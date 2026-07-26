# Verlay desktop compatibility prototype

This is a small Tauri 2 interface for Verlay's legacy Croc-compatible
code-transfer path. It predates the persistent paired-device mesh and is not yet
the primary Verlay desktop client. The UI is plain HTML, CSS, and JavaScript;
the transfer protocol remains in the bundled Go sidecar.

## Low-memory build

From this directory:

```sh
npm install
./build-low-memory.sh
```

The script limits Rust to one build job and builds the Go sidecar with stripped
debug information. It also gives AppImage tooling an executable project-local
temporary directory and disables linuxdeploy's outdated bundled `strip`, which
cannot read Fedora 44's RELR ELF sections. The AppImage is written below
`src-tauri/target/release/bundle/appimage/`.

For development, run `./run-low-memory.sh` after installing dependencies.

## Security boundary

- Receive codes are sent to `croc` through `CROC_SECRET`, not command arguments.
- The Rust bridge exposes fixed send/receive commands rather than arbitrary shell execution.
- Only one sidecar transfer runs at a time, and it can be cancelled from the UI.
