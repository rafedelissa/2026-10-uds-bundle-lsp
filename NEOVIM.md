# Using the UDS language server with Neovim

This guide configures Neovim to use `uds-lsp` for UDS Next bundle HCL and referenced YAML values files. It uses Neovim's built-in LSP client and does not require a plugin manager or a particular completion plugin.

The language server provides diagnostics, hover information, completion, definition navigation, document highlights, cross-file rename, and formatting.

This guide provides a general, plugin-independent starting point rather than a recommended configuration for every Neovim setup. Neovim distributions, plugin managers, LSP frameworks, completion plugins, and personal conventions may configure these features differently. Adapt the examples to your local configuration and avoid registering duplicate clients, mappings, completion sources, or formatters.

## Prerequisites

- Neovim 0.11 or later
- macOS or Linux on `amd64` or `arm64`, matching the platforms supported by UDS CLI
- A `uds-lsp` executable built for your platform
- A UDS Next source workspace containing `bundle.uds.hcl`
- `uds-lsp` on the environment `PATH` visible to Neovim, or its absolute path

The LSP does not require the `NextMode` feature flag. That flag controls UDS CLI command routing, not editor communication with `uds-lsp`.

## Install `uds-lsp`

`uds-lsp` communicates over standard input and output. Start it through Neovim rather than running it interactively in a terminal.

First, check [UDS CLI releases](https://github.com/defenseunicorns/uds-cli/releases) for a prebuilt `uds-lsp` binary matching macOS or Linux and your `amd64` or `arm64` architecture. If the release does not include `uds-lsp`, build it from a UDS CLI source checkout:

```bash
uds run build-lsp
```

A local build creates `build/uds-lsp`. Install either the downloaded or locally built executable to a directory on your `PATH`:

```bash
mkdir -p ~/.local/bin
install -m 0755 /path/to/uds-lsp ~/.local/bin/uds-lsp
```

Alternatively, use the executable's absolute path as `cmd` in the configuration below. Graphical Neovim applications do not always inherit the shell's `PATH`, so an absolute path can be more reliable.

## Configure Neovim

Place the following snippets in an appropriate Lua configuration file that your Neovim setup loads. The file name and location depend on your local configuration, plugin manager, or Neovim distribution.

### 1. Configure filetype detection

Assign the HCL filetype to the exact UDS source filenames and the YAML filetype to values-file extensions:

```lua
vim.filetype.add({
  filename = {
    ["bundle.uds.hcl"] = "hcl",
    ["config.uds.hcl"] = "hcl",
    ["defaults.uds.hcl"] = "hcl",
  },
  extension = {
    yaml = "yaml",
    yml = "yaml",
  },
})
```

Neovim already detects `.yaml` and `.yml` by default, but listing them here makes the UDS LSP document types explicit. The server still applies UDS values behavior only to YAML files referenced by a bundle.

### 2. Register the language server

Add the server configuration after the filetype configuration:

```lua
vim.lsp.config("uds_lsp", {
  cmd = { "uds-lsp" },
  filetypes = { "hcl", "yaml" },
  root_markers = { ".git", "bundle.uds.hcl" },
})

vim.lsp.enable("uds_lsp")
```

Replace `"uds-lsp"` with an absolute executable path when it is not on Neovim's `PATH`:

```lua
cmd = { "/absolute/path/to/uds-lsp" },
```

Neovim checks root markers in priority order. Preferring `.git` lets one server index bundles across a repository and supports shared values files outside a bundle directory. The bundle marker is the fallback for source directories that are not in a Git repository.

If a repository contains intentionally invalid fixtures or unrelated bundles, reverse the marker order or open a narrower standalone source directory:

```lua
root_markers = { "bundle.uds.hcl", ".git" },
```

The client may attach `uds-lsp` to unrelated HCL or YAML buffers in the selected workspace. The server ignores files that are not recognized UDS source files or referenced values files.

### 3. Configure optional LSP actions

`uds-lsp` provides standard LSP capabilities; it does not require particular keybindings. Your Neovim distribution or existing LSP configuration may already map hover, definition, rename, highlights, and formatting. Use those mappings if they work for your setup, or assign any keys you prefer.

The following is only an example of adding buffer-local mappings when the UDS server attaches. Omit this section if your configuration already provides equivalent actions:

```lua
vim.api.nvim_create_autocmd("LspAttach", {
  callback = function(event)
    local client = vim.lsp.get_client_by_id(event.data.client_id)
    if not client or client.name ~= "uds_lsp" then
      return
    end

    local function map(lhs, rhs, description)
      vim.keymap.set("n", lhs, rhs, {
        buffer = event.buf,
        desc = description,
      })
    end

    map("K", vim.lsp.buf.hover, "UDS hover")
    map("gd", vim.lsp.buf.definition, "UDS go to definition")
    map("grn", vim.lsp.buf.rename, "UDS rename")
    map("<leader>lh", vim.lsp.buf.document_highlight, "UDS document highlights")
    map("<leader>lc", vim.lsp.buf.clear_references, "Clear UDS highlights")
    map("<leader>lf", function()
      vim.lsp.buf.format({
        bufnr = event.buf,
        filter = function(formatter)
          return formatter.name == "uds_lsp"
        end,
      })
    end, "Format with uds-lsp")

    vim.bo[event.buf].omnifunc = "v:lua.vim.lsp.omnifunc"
  end,
})
```

Change or remove these mappings to match your local Neovim configuration. If another plugin manages LSP mappings or formatting, configure it there instead of maintaining a second set here.

Ensure the file containing this configuration is loaded by your Neovim setup. Restart Neovim, then open the repository or a directory containing `bundle.uds.hcl`.

## Configure completion

The configuration sets Neovim's built-in LSP omnifunction. In Insert mode, request completion with `<C-x><C-o>`.

If you use a completion frontend such as `blink.cmp` or `nvim-cmp`, enable its LSP source according to that plugin's documentation. Do not start a second `uds-lsp` client only to provide completion; completion, diagnostics, and navigation should share the same client.

## Verify the setup

Open `bundle.uds.hcl`, then inspect the clients attached to the current buffer:

```vim
:lua vim.print(vim.lsp.get_clients({ bufnr = 0 }))
```

Confirm that the output includes a client named `uds_lsp`. Run Neovim's built-in LSP health check for additional configuration and executable diagnostics:

```vim
:checkhealth vim.lsp
```

Verify the features against a bundle workspace:

1. Introduce an invalid bundle attribute and confirm that a diagnostic appears.
2. Place the cursor over a supported field or expression and press `K`.
3. Place the cursor on `local.name`, `package.name`, or a `.vars.name` reference and press `gd`.
4. Request completion with `<C-x><C-o>` or your completion frontend.
5. Run `<leader>lf` in a UDS HCL file and confirm that formatting is applied.

## Troubleshooting

### `uds_lsp` does not attach

Check the current filetype, executable, workspace root, and built-in LSP health report:

```vim
:set filetype?
:lua print(vim.fn.executable("uds-lsp"))
:lua print(vim.fs.root(0, { ".git" }) or vim.fs.root(0, { "bundle.uds.hcl" }))
:checkhealth vim.lsp
```

The executable check must print `1`, and root discovery must return a directory. Use an absolute `cmd` path if Neovim cannot find the executable.

### Completion does not appear automatically

Built-in omnifunction completion is manual; use `<C-x><C-o>`. For automatic menus, configure your completion frontend's LSP source. Verify that only one completion frontend owns the menu to avoid duplicate candidates.

### A YAML server reports errors on template actions

A general YAML language server may report syntax errors on `{{ ... }}` actions even though `uds-lsp` accepts the values template. Check the diagnostic source before changing the file.

Configure the general YAML server to exclude templated values files, or disable that server only for those buffers. Do not disable YAML support globally when ordinary YAML files still need it.

### Formatting uses another server

Keep the `filter` in the formatting mapping so that the request goes only to `uds_lsp`. Review other formatter plugins for additional format-on-save behavior.

### Diagnostics are missing or stale

Confirm that Neovim selected the expected workspace root. Exit and reopen Neovim after changing the root configuration. A plugin or Neovim distribution may provide a client restart command, but it is not required by this setup.

The server writes its own log to the operating system temporary directory as `uds-lsp.log` by default. Set an explicit location when debugging:

```lua
vim.lsp.config("uds_lsp", {
  cmd = { "uds-lsp" },
  cmd_env = {
    UDS_LSP_LOG_FILE = vim.fn.stdpath("log") .. "/uds-lsp.log",
  },
  filetypes = { "hcl", "yaml" },
  root_markers = { ".git", "bundle.uds.hcl" },
})
```

Restart Neovim and inspect that file together with Neovim's LSP log.

## More information

- [UDS bundle definition reference](docs/reference/next/bundle-uds-hcl.mdx)
- [Neovim LSP documentation](https://neovim.io/doc/user/lsp.html)
