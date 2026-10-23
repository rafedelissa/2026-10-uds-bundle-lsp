# UDS language support for VS Code

This extension connects VS Code to the external `uds-lsp` language server.
It validates `bundle.uds.hcl`, `defaults.uds.hcl`, `config.uds.hcl`, and
referenced YAML values templates, including unsaved edits.

Workspace file creation, changes, and deletion are forwarded for these HCL and
YAML files. Open `.yaml` and `.yml` files outside the workspace are also
synchronized. The server determines which YAML files are referenced values
files; disk changes outside the workspace's watched scope refresh on the next
document synchronization event.

## Install and configure

You need VS Code 1.140.0 or later and a `uds-lsp` executable built for the machine
running the extension. The VSIX contains the client; it does not bundle or
download the language server.

From a checkout of this repository with the
[development tools](../CONTRIBUTING.md#set-up-the-development-environment)
installed, run these commands at the repository root:

```console
uds run build-lsp
uds run build-vscode-extension
code --install-extension build/uds-lsp-extension.vsix
```

If the `code` command is unavailable, use **Extensions: Install from VSIX...**
in VS Code and select `build/uds-lsp-extension.vsix`. Consumers who receive an
already built VSIX and a matching `uds-lsp` executable can skip the build steps.

By default, the extension runs `uds-lsp` from the extension host's `PATH`.
Place the executable in a directory already on that `PATH`, or open
**Preferences: Open User Settings (JSON)** and set its absolute path:

```json
{
  "udsLsp.serverPath": "/absolute/path/to/uds-cli/build/uds-lsp"
}
```

Replace the example path with the actual executable path. In the Settings UI,
search for `udsLsp.serverPath` to edit the same setting without JSON. User
settings apply across projects; a workspace's `.vscode/settings.json` can
override them for that project. Keep machine-specific paths out of shared
workspace settings. In Remote SSH, WSL, or a dev container, install and
configure the executable on the machine running the workspace extension host.

Reload the VS Code window after changing the executable path or arguments.
Open a UDS HCL file to activate the client. Once it is running, use **UDS LSP:
Restart Server** after replacing or rebuilding the executable at the same path.

## Settings

| Setting | Default | Behavior |
| --- | --- | --- |
| `udsLsp.serverPath` | `uds-lsp` | Executable name on `PATH` or an absolute path. |
| `udsLsp.serverArgs` | `[]` | Arguments passed to the server, which must communicate over stdin/stdout. |
| `udsLsp.trace.server` | `off` | LSP protocol trace level: `off`, `messages`, or `verbose`. Also set the UDS LSP output channel log level to Trace to see protocol traces. |

`serverPath` is passed directly to the process launcher. Do not add shell
quotes around the value or use `~`, `${workspaceFolder}`, or `${env:...}`;
the extension does not expand these expressions in settings.

## Develop the extension

Run `uds run build-vscode-extension` once from the repository root to install
the locked npm dependencies and verify packaging. Then open `vscode-extension/`
as a VS Code folder and press F5 with **Run LSP Client** selected.

The launch task builds `../build/uds-lsp` and starts the TypeScript and esbuild
watchers. The Extension Development Host receives a `PATH` with `../build`
prepended, resolved from the extension folder. Leave `udsLsp.serverPath` at
its default `uds-lsp` to use that binary. An explicit setting overrides the
`PATH` lookup. This launch environment applies only to development sessions.

Open `tests/test_data/lsp/` from this repository in the Extension Development
Host and follow the [validation playground](../tests/test_data/lsp/README.md)
to check diagnostics. After changing Go code, run `uds run build-lsp` from
the repository root and use **UDS LSP: Restart Server**. F5 debugs the
TypeScript client; Go server breakpoints require a separate Go debugger session.

For a new installable artifact, rerun `uds run build-vscode-extension`. It
copies the repository license, installs dependencies with `npm ci`, runs type
and lint checks, bundles the client, and packages
`build/uds-lsp-extension.vsix`. This task packages locally and does not publish
to the Marketplace.
