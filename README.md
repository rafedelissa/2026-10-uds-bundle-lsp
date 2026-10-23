# UDS language server

Editor support for UDS CLI Next bundles — an October 2026 Dash Days experiment.

`uds-lsp` brings live validation, completion, navigation, and refactoring to
`bundle.uds.hcl`, `defaults.uds.hcl`, `config.uds.hcl`, and referenced YAML values
templates. It comes bundled with a **VS Code extension** and also works with
**Neovim's built-in LSP client**.

## Why this project exists

- **Problem:** Moving bundle authoring from YAML to HCL means rebuilding the
  editor support bundle authors rely on. Finding mistakes by editing a file and
  then running bundle creation slows the feedback loop.
- **Approach:** Reuse the [CLI Next parser](internal/bundle/bundle.go) and
  expose bundle-aware assistance through a language server, so editors can
  understand UDS fields, references, and variable context.
- **Outcome:** Authors get diagnostics as they type, including errors across HCL
  and values templates, plus completion, hover information, navigation, rename,
  and formatting in their editor.
- **Mission value:** Catch bundle configuration mistakes earlier and reduce
  repeated CLI validation cycles, helping platform and delivery engineers spend
  more time preparing deployable software and less time tracking down authoring
  errors.

  - **AI-assisted workflows:** The same diagnostics can give workflows such as
    Ark early feedback during bundle authoring, helping them correct mistakes
    before bundle creation or deployment. Agents without LSP support can use
    `bundle lint` and `bundle fmt` for validation and formatting through the CLI.

This repository contains the experimental LSP alongside the UDS CLI source it
builds on. The editor setup below runs locally without a cluster or registry
credentials.

## Features

| Feature | What you get |
| --- | --- |
| [Live diagnostics](docs/reference/next/language-server.mdx#local-validation) | HCL syntax, evaluation, bundle constraints, dependency errors, missing values files, and Go template failures using merged defaults/config variables. Unsaved edits take precedence over disk. |
| [Hover information](internal/lsp/hover.go) | Field documentation and context for expressions, variables, and `file()` calls. |
| [Completion](NEOVIM.md#configure-completion) | UDS fields and blocks, local/package references, built-in expressions, local file paths, and `.vars` keys in referenced values templates. |
| [Go to definition](docs/reference/next/language-server.mdx#go-to-definition) | Jump to locals, package declarations, values files, and variable assignments in defaults/config. |
| [Rename](internal/lsp/rename.go) | Rename locals and packages with their references, or variables across defaults, config, and referenced values templates. Ambiguous shared-template renames are rejected. |
| [Document highlights](internal/lsp/highlight.go) | Highlight local and package declarations and references in the current bundle. |
| [Formatting](docs/reference/next/language-server.mdx#formatting) | Apply canonical HCL whitespace to the three UDS HCL source files. |

The server also [discovers bundles across the workspace](docs/reference/next/language-server.mdx#workspace-discovery),
so diagnostics and navigation work with unopened files and shared values
templates. Both editor integrations use the same server.

## CLI lint and format

The CLI exposes the same diagnostics and HCL formatter for terminal and CI
workflows:

```sh
CLI_FEATURES=NextMode=true uds bundle lint tests/test_data/lsp/healthy
CLI_FEATURES=NextMode=true uds bundle fmt --check tests/test_data/lsp/healthy
CLI_FEATURES=NextMode=true uds bundle fmt tests/test_data/lsp/healthy
```

- [`bundle lint`](internal/cli/bundle/lint.go) checks saved bundle source and referenced values templates, reporting diagnostics with file locations. Errors cause a nonzero exit; `--check` fails on any diagnostic.
- [`bundle fmt`](internal/cli/bundle/fmt.go) formats UDS HCL files in the selected directory. `--check` reports needed changes without writing files and exits nonzero if any are found.

Both commands default to the current directory and support `--output json` or
`--output yaml`. AI agents without LSP support can also run these commands to
check and format bundles, using structured output and exit codes to guide fixes
during authoring.

## Connecting your editor

Use a [pre-built binary](#download-pre-built-binaries), or build from a
checkout of this repository. For source builds, install and
activate the [repository's development tools](CONTRIBUTING.md#set-up-the-development-environment)
with mise; they provide Go, Node.js/npm, and the bootstrap `uds` task runner.
Run the commands below from the repository root.

### Download pre-built binaries

Skip building from source by downloading the pre-built binary matching your platform
from [this repository's releases](https://github.com/defenseunicorns-dashdays/2026-10-uds-bundle-lsp/releases):

- **VS Code extension:** `uds-lsp-extension-<target>.vsix`, including its server (for example, `uds-lsp-extension-darwin-arm64.vsix`).
- **Standalone LSP:** `uds-lsp-<os>-<arch>` (plus `.exe` on Windows), for Neovim and other editors.
- **CLI:** A platform-specific `uds` executable, such as `uds-mac-apple`, with the lint and format commands.

Pre-built binaries need no Go or Node.js toolchain. Keep a checkout for the
playground examples, and give downloaded executables permission to run on
macOS/Linux with `chmod +x`.

### VS Code

Use VS Code **1.140.0 or later**. If building from source, run:

```sh
uds run build-vscode-extension
```

In VS Code, run **Extensions: Install from VSIX...** and select the downloaded
`uds-lsp-extension-<target>.vsix` (or the generated file under `build/`). The [extension installation guide](vscode-extension/README.md#install)
lists platform targets and remote workspace instructions. The VSIX bundles the
server, so installing a built package needs no separate Go toolchain or server
setup. Leave `udsLsp.serverPath` empty to use it.

Open the healthy example as a workspace:

```sh
code tests/test_data/lsp/healthy
```

Open `bundle.uds.hcl` to activate the extension. See the
[extension README](vscode-extension/README.md) for settings and the F5
development workflow.

### Neovim

Use Neovim **0.11 or later**. If building the standalone server from source, run:

```sh
uds run build-lsp
```

Follow [the Neovim guide](NEOVIM.md#configure-neovim) to register the server
with its built-in LSP client. Set `cmd` to the absolute path of the downloaded
server or this checkout's `build/uds-lsp`, or install it on your `PATH` as
described in the guide. Then
open `tests/test_data/lsp/healthy/bundle.uds.hcl`. The guide includes completion,
keybinding, and connection checks; no particular plugin manager is required.

Neither editor needs the CLI's `NextMode` feature flag to run `uds-lsp`.

## Testing LSP diagnostics

The [validation playground](tests/test_data/lsp/) contains several bundles with
healthy and intentionally broken configurations. It covers missing required
blocks and fields, missing files, invalid config, template errors, and values
files shared between bundles. Open
that directory as your editor workspace; the [playground README](tests/test_data/lsp/README.md)
lists all scenarios and their expected diagnostics. Errors in the broken
scenarios are expected, even with their tabs closed.

### Values paths and templates

With either editor connected, try these checks. Paths are relative to the
playground directory:

1. **Missing values file:** Open [broken-paths/bundle.uds.hcl](tests/test_data/lsp/broken-paths/bundle.uds.hcl).
   Expect an error on `values/missing.yaml` because the file does not exist,
   plus another on `values/directory.yaml` because it is a directory.
2. **Undefined variable:** Open [broken-values/values/missing-variable.yaml](tests/test_data/lsp/broken-values/values/missing-variable.yaml).
   Expect an error for `.vars.missing`. Replace it with `.vars.service.name`
   without saving; that file's diagnostic should clear using the value in
   `broken-values/defaults.uds.hcl`.
3. **Template syntax:** Open [broken-values/values/unclosed.yaml](tests/test_data/lsp/broken-values/values/unclosed.yaml).
   Expect an error for a `range` without an `end`. Add `{{ end }}` on a new line
   to close the block; that file's diagnostic should clear.

Undo your edits after each check to restore the original scenarios.

### Missing required blocks and fields

Open [missing-required/bundle.uds.hcl](tests/test_data/lsp/missing-required/bundle.uds.hcl).
It intentionally omits the `uds` block, `metadata.name`, and the package's
`source`. Expect diagnostics for each missing item. Replace the fixture's
contents with this complete definition to clear them, even before saving:

```hcl
uds {
  bundle_api_version = "uds.dev/v1alpha1"
}

metadata {
  name = "lsp-missing-required"
}

package "app" {
  source = "oci://example.invalid/lsp/demo:0.0.0"
}
```

Undo afterward to restore the fixture. See the [bundle definition reference](docs/reference/next/bundle-uds-hcl.mdx#top-level-structure)
for required blocks and fields.

In VS Code, filter the Problems panel to `uds-lsp`; other YAML servers may flag
unrendered template syntax independently.

The fixtures use placeholder package sources and are for editor testing. The
LSP does not fetch packages, and these examples do not require deployment.

## Scope and further reading

Validation covers local HCL and Go template execution. It does not validate
rendered YAML, package schemas, registry availability, or cluster state. Editor
validation uses adjacent defaults/config; CLI commands still select deploy-time
config explicitly with `--config`.

- [Language-server reference](docs/reference/next/language-server.mdx) — diagnostics, navigation, workspace behavior, and validation limits.
- [VS Code extension](vscode-extension/README.md) — install, activate, configure, and package the bundled client/server.
- [Neovim setup](NEOVIM.md) — standalone server configuration and troubleshooting.
- [Validation playground](tests/test_data/lsp/README.md) — reproducible scenarios and expected diagnostics.
- [Contributing](CONTRIBUTING.md#building-the-app) — build tasks, platform binaries, and checksums.
- [UDS CLI documentation](docs/) — bundle format and the underlying CLI workflows.
