# UDS language support for VS Code

This extension includes the `uds-lsp` language server. It validates
`bundle.uds.hcl`, `defaults.uds.hcl`, `config.uds.hcl`, and referenced YAML
values templates, including unsaved edits.

Workspace file creation, changes, and deletion are forwarded for these HCL and
YAML files. Open `.yaml` and `.yml` files outside the workspace are also
synchronized. The server determines which YAML files are referenced values
files; disk changes outside the workspace's watched scope refresh on the next
document synchronization event.

## Install

Use VS Code 1.140.0 or later. Install the VSIX matching the operating system and
architecture of the extension host. The package includes its server; consumers
need no separate LSP installation, Go toolchain, or executable path setting.

| Platform | VSIX target |
| --- | --- |
| macOS, Apple silicon | `darwin-arm64` |
| macOS, Intel | `darwin-x64` |
| Linux, ARM64 | `linux-arm64` |
| Linux, x86-64 | `linux-x64` |
| Windows, ARM64 | `win32-arm64` |
| Windows, x86-64 | `win32-x64` |

In VS Code, use **Extensions: Install from VSIX...** and select the matching
file. You can also install it from a shell. For example, on Apple silicon
macOS, with the artifact in `build/`:

```console
code --install-extension build/uds-lsp-extension-darwin-arm64.vsix
```

Open a UDS HCL file to activate the client. Updating the extension also updates
the bundled server.

In Remote SSH, WSL, or a dev container, install the extension in the remote
workspace environment and choose the package for that environment. A macOS
desktop connected to an x86-64 Linux workspace needs the `linux-x64` package
on the remote host. Browser-only extension hosts cannot run the native server.

If you previously configured `udsLsp.serverPath`, reset it to its empty default
to use the bundled server.

## Settings

| Setting | Default | Behavior |
| --- | --- | --- |
| `udsLsp.serverPath` | Empty string | Automatically selects the bundled server, or the repository build in development. A nonempty value overrides automatic selection. |
| `udsLsp.serverArgs` | `[]` | Arguments passed to the server, which must communicate over stdin/stdout. |
| `udsLsp.trace.server` | `off` | LSP protocol trace level: `off`, `messages`, or `verbose`. Also set the UDS LSP output channel log level to Trace to see protocol traces. |

For a custom server build, open **Preferences: Open User Settings (JSON)** and
set its absolute executable path. Replace this example with your actual path:

```json
{
  "udsLsp.serverPath": "/absolute/path/to/uds-cli/build/uds-lsp"
}
```

You can also search for `udsLsp.serverPath` in the Settings UI. Set it to
`uds-lsp` to explicitly use a server on the extension host's `PATH`. User
settings apply across projects; workspace settings can override them for one
project. Keep machine-specific paths out of shared workspace settings. Remote
settings and executable overrides refer to the extension host's machine.

`serverPath` is passed directly to the process launcher. Do not add shell
quotes around the value or use `~`, `${workspaceFolder}`, or `${env:...}`;
the extension does not expand these expressions in settings.

Reload the VS Code window after changing the executable path or arguments.
Use **UDS LSP: Restart Server** after replacing the executable at the same path.

## Develop the extension

With the repository's
[development tools](../CONTRIBUTING.md#set-up-the-development-environment)
installed and mise activated, run these commands from the repository root:

```console
npm --prefix vscode-extension ci
code vscode-extension
```

Press F5 with **Run LSP Client** selected. The launch task builds
`build/uds-lsp` (`build/uds-lsp.exe` on Windows) and starts the TypeScript and
esbuild watchers. In development mode, the client resolves that repository
build directly, independently of `PATH` and of the workspace opened in the
Extension Development Host. Leave `udsLsp.serverPath` empty for this behavior;
a nonempty setting overrides it.

Open `tests/test_data/lsp/` from this repository in the Development Host and
follow the [validation playground](../tests/test_data/lsp/README.md) to check
diagnostics. After changing Go code, run `uds run build-lsp` from the repository
root and use **UDS LSP: Restart Server**. After changing TypeScript, the watchers
rebuild the client; reload the Development Host to load the changes. F5 debugs
the TypeScript client; Go breakpoints require a separate Go debugger session.

Run `uds run test-vscode-extension` to install locked dependencies and check
client types, lint, and executable selection without opening VS Code.

## Build distributable packages

From the repository root with the development tools installed, run:

```console
uds run build-vscode-extension
```

This installs locked npm dependencies, runs client unit tests, checks types and
lint, bundles the client, cross-compiles a standalone server for the current
host target, and creates `build/uds-lsp-extension-<target>.vsix`. The target is one of the
values in the installation table above. Each package includes only its matching
server, the bundled client, the manifest, the README, the changelog, and the
repository license.

To select a target explicitly, for example Apple silicon macOS from a Linux
build host:

```console
UDS_VSCODE_TARGET=darwin-arm64 uds run build-vscode-extension
```

After dependencies are installed, the equivalent npm entrypoint is:

```console
npm --prefix vscode-extension run package:vsix -- --target darwin-arm64
```

To build all six supported targets:

```console
uds run build-vscode-extension-all
```

Build Unix distributables on macOS or Linux to preserve executable permissions
in the VSIX. Go cross-compilation uses `CGO_ENABLED=0`. The packager shares
[`hack/build-lsp.mjs`](../hack/build-lsp.mjs) with the standalone LSP tasks and
retains each server in `build/` before copying it into a temporary VSIX staging
directory. For example, `darwin-x64` sets `GOOS=darwin` and `GOARCH=amd64`, writes
`build/uds-lsp-darwin-amd64`, and copies that exact file to `bin/uds-lsp` in the
VSIX. Windows binaries use the `.exe` suffix.

To build standalone LSP binaries without packaging the client or installing
npm dependencies, run either of these tasks from the repository root:

```console
uds run build-lsp-all
UDS_LSP_TARGET=darwin-arm64 uds run build-lsp-target
```

The all-target task produces these standalone files:

```text
build/uds-lsp-darwin-arm64
build/uds-lsp-darwin-amd64
build/uds-lsp-linux-arm64
build/uds-lsp-linux-amd64
build/uds-lsp-windows-arm64.exe
build/uds-lsp-windows-amd64.exe
build/lsp-checksums.txt
```

The checksum file covers the available standalone variants and is refreshed
by standalone builds and VSIX packaging. From the repository root, verify it
with `sha256sum -c build/lsp-checksums.txt`. Consumers of other editors can
configure their LSP client to launch the matching standalone executable over
stdin/stdout. Distribute the repository license with these binaries.

`uds run build-all` builds the existing CLI variants and all six standalone
LSP variants, with combined hashes in `build/checksums.txt`. Packaging and
standalone release builds preserve the local `build/uds-lsp` used by F5. These
tasks run locally and do not publish artifacts.
