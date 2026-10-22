# LSP validation playground

Open these fixtures in an editor connected to the locally built `uds-lsp` to exercise local cross-file diagnostics. Each scenario has its own bundle and variable context. Package sources under `example.invalid` are placeholders for editor testing; the LSP does not fetch them. These fixtures do not supply deployable Zarf packages.

Build the current language server from the repository root and point your editor's LSP launch command at the resulting executable:

```sh
go build -o build/uds-lsp ./cmd/uds-lsp
```

Restart your editor's language server after rebuilding. Configure its document selector to synchronize both the HCL files and YAML values files. Use the editor's Problems panel filtered to `uds-lsp`; a separate YAML server can report errors against unrendered template syntax.

Use this directory as an editor workspace root, with that root supplied through LSP `workspaceFolders` or `rootUri`. The server discovers bundles and standalone defaults/config on disk and publishes their diagnostics before you open any file. Keep the entire directory in scope for the shared-file cases. To focus on a single independent scenario, open just that scenario's directory as the workspace root.

The paths in the tables below are relative to this directory. The intentionally broken fixtures are expected to produce errors even with their tabs closed. The table lists each scenario's contribution, rather than the total number of errors when the whole playground is indexed.

| Indexed scenario | Expected UDS diagnostics with the supplied files |
| --- | --- |
| [healthy/bundle.uds.hcl](healthy/bundle.uds.hcl) | None. Exercises scalar override, nested merge, list replacement, template control flow, architecture selection, and a file outside the bundle directory. |
| [broken-paths/bundle.uds.hcl](broken-paths/bundle.uds.hcl) | Two errors on `values_files`: a missing file and a directory named `directory.yaml`. |
| [broken-values/bundle.uds.hcl](broken-values/bundle.uds.hcl) | One diagnostic in each of eight broken values files; none in `unexecuted-branch.yaml`. |
| [invalid-config/bundle.uds.hcl](invalid-config/bundle.uds.hcl) | One nested-null error in config and one template syntax error in `values/syntax-error.yaml`. Execution of `values/needs-variable.yaml` is suppressed. |
| [no-variables/bundle.uds.hcl](no-variables/bundle.uds.hcl) | None: both adjacent variable files are absent, so execution of `values/lazy.yaml` is skipped. |
| [empty-variables/bundle.uds.hcl](empty-variables/bundle.uds.hcl) | One missing-key error in `no-variables/values/lazy.yaml`: an empty variables object enables execution. |
| [shared-a/bundle.uds.hcl](shared-a/bundle.uds.hcl), [shared-b/bundle.uds.hcl](shared-b/bundle.uds.hcl), and [shared/per-bundle.yaml](shared/per-bundle.yaml) inside the workspace | One diagnostic in `shared/per-bundle.yaml`, attributed to `shared-b`; `shared-a` supplies the required variable. Neither bundle needs an open tab. |
| [standalone/defaults.uds.hcl](standalone/defaults.uds.hcl) | Unexpected `options` block, without needing a bundle. |
| [standalone/config.uds.hcl](standalone/config.uds.hcl) | `concurrency` requires a number, without needing a bundle. |
| [unreferenced.yaml](unreferenced.yaml) | None: no bundle references this file. |

For merge and template checks, start with the healthy bundle:

| Action | Expected result |
| --- | --- |
| Open `healthy/values/app.yaml` and `healthy/values/merge-checks.yaml` | No diagnostics. The checks file deliberately fails if config's replicas/port do not win, the default host disappears, or default list entries survive. |
| Change config's `replicas` from `3` to `4`, without saving | A diagnostic in `merge-checks.yaml` confirms the unsaved override was used. Undo to clear it. |
| Change config's database object to `database = "external"` | Traversing `.vars.database.host` or `.port` fails. The replacement is legal HCL; its template use causes the errors. |
| Change config's `ports` from a list to `true` | `range` fails in `app.yaml`. Undo to clear the errors. |
| Change defaults' `enabled` to `false` | The previously skipped branch in `app.yaml` runs and reports `only_needed_when_disabled`. |
| Inspect a template diagnostic's related information | Find the package's `values_files` reference and the defaults/config variable locations. |
| Change config's architecture from `amd64` to `arm64` | No error; the bundle resolves the supplied `values/arch-arm64.yaml`. |
| Change architecture to `missing-arch` | A missing-file diagnostic points to `values/arch-${sys.arch}.yaml` in the bundle. Undo to clear it. |

`app.yaml` also exercises `with`, `range` aliases, root scope, `index` for a key containing punctuation, pipelines, and `printf`. `merge-checks.yaml` checks actual merged values through conditional template execution; the LSP does not display rendered YAML.

For individual template failures, open the broken-values bundle and these files:

| Values file under `broken-values/values/` | Expected diagnostic |
| --- | --- |
| `missing-variable.yaml` | `.vars.missing` is undefined. |
| `missing-nested.yaml` | `service` exists, but `service.missing` does not. |
| `object-replaced.yaml` | Config replaced defaults' `database` object with a scalar. |
| `bad-range.yaml` | Config replaced the `ports` collection with a boolean. |
| `bad-index.yaml` | Index `99` is outside the supplied `names` list. |
| `unclosed.yaml` | A `range` action is missing its `end`. |
| `unknown-function.yaml` | `toYaml` is not a standard Go template function. |
| `unicode.yaml` | Missing-key error; the highlight should align after the emoji and accented character. |
| `unexecuted-branch.yaml` | No error until defaults' `disabled` becomes `true`. |

For defaults/config validation, use the fragments in `cases/`. These filenames are intentionally different from `config.uds.hcl` and `defaults.uds.hcl`: copy their contents into the indicated active file to trigger validation. Test one fragment at a time.

| Fragment under `cases/` | Copy into | Expected result |
| --- | --- | --- |
| `config-syntax.hcl` | `invalid-config/config.uds.hcl` | HCL syntax error for the unclosed object. |
| `config-option-type.hcl` | `invalid-config/config.uds.hcl` | `concurrency` requires a number. |
| `variables-not-object.hcl` | Either active variables file | `variables` must be an object. |
| `variables-nested-null.hcl` | Either active variables file | A null inside a nested list is rejected. |
| `defaults-block.hcl` | `standalone/defaults.uds.hcl` | Defaults reject the `options` block. |
| `defaults-extra-attribute.hcl` | `standalone/defaults.uds.hcl` | Defaults reject the unexpected top-level attribute. |
| `variables-absent.hcl` | `empty-variables/config.uds.hcl` | The missing-key execution error clears when no variables object exists. |
| `variables-empty.hcl` | `empty-variables/config.uds.hcl` | The missing-key execution error returns. |
| `config-valid.hcl` | `invalid-config/config.uds.hcl` | The config error clears; `needs-variable.yaml` succeeds. The independent template syntax error remains. |

While config is invalid, execution errors should be suppressed but template syntax errors should remain. To test a top-level scalar as well as the supplied list fragment, replace the whole attribute with `variables = "wrong"`.

Saved copies of the initial healthy and invalid configs are available as `cases/healthy-config.hcl` and `cases/invalid-config.hcl`. Use editor undo or copy those contents back after an experiment.

For path and refresh checks:

| Action | Expected result |
| --- | --- |
| Open the broken-paths bundle and create `values/missing.yaml` in an unsaved editor buffer containing `value: true` | The missing-file error clears if the editor sends `didOpen` for that file URI. A saved file is not required. |
| Save that file, close its tab, then rename it away and back using the file explorer | The bundle's missing-file diagnostic returns and clears through watcher notifications. |
| Replace `healthy`'s `../shared/common.yaml` entry with the file's absolute path, copied from your editor | No path error. Do not leave an example path literal in the bundle. |
| Introduce a missing-variable error in a values file without saving, then discard the edit and close its tab | Diagnostics return to the saved contents. The bundle can remain closed. |
| Edit a closed defaults/config file on disk or through another editor | Diagnostics refresh when your client sends its watcher notification. |
| Remove a broken values-file entry from its bundle | Its old UDS diagnostics clear when no indexed or open bundle references it. |
| Open only `shared/per-bundle.yaml`, with no bundle tabs open | The error from indexed `shared-b` is already present. No ancestor relationship is required. |
| Open an invalid standalone HCL file | Its HCL diagnostics appear without a referencing bundle. |
| Close every document associated with a scenario, including values/config/defaults tabs | Diagnostics remain for its saved, indexed content. |
| Add, rename, or remove a directory containing an unopened bundle | File-watcher notifications update discovery and clear results for removed paths. |
| Remove a workspace folder from the editor | Diagnostics clear for its files unless an open document or another workspace bundle still requires them. |

Use `no-variables` to verify that absent defaults/config are allowed. Removing these files from another scenario can still cause template failures because required variables are no longer available.

For an unreadable-file check on a POSIX filesystem, keep the broken-paths bundle open and keep `unreadable.yaml` closed. From the repository root, run:

```sh
chmod 000 tests/test_data/lsp/broken-paths/values/unreadable.yaml
```

Trigger a document edit/save if the client does not report permission changes. Expect a read error on the bundle reference. Restore access immediately after checking:

```sh
chmod 644 tests/test_data/lsp/broken-paths/values/unreadable.yaml
```

A language server running as root may still be able to read the file. File permission enforcement depends on the host filesystem.

For shared ownership, keep both shared bundle directories inside the workspace. Open only `shared/per-bundle.yaml`, or inspect the Problems panel with no files open. The shared template accesses `.vars.message`: `shared-a` supplies it through defaults, while `shared-b` defines only `other`. Expect a diagnostic containing `map has no entry for key "message"` and identifying `shared-b/bundle.uds.hcl`.

The client must use the current server binary, provide its workspace roots, and display UDS YAML diagnostics. If the client supplies no root, discovery falls back to open documents and ancestor lookup; in that fallback mode, keep the sibling bundle tabs open for this case.

Add `message = "available in shared-b"` to `shared-b/defaults.uds.hcl`; its diagnostic should clear immediately, even before saving. Remove `message` from both bundles' defaults to see two diagnostics in the same values file, each identifying its bundle. Remove both bundles' references to clear them.

The active config is the adjacent `config.uds.hcl` editor convention. The CLI still requires `--config` to select deploy-time config. This playground checks Go template execution and local paths; rendered YAML and package schemas are outside this pass.

See [the language-server reference](../../../docs/reference/next/language-server.mdx) for supported diagnostics and limits, and [the implementation regression tests](../../../internal/lsp/workspace_diagnostics_test.go) for protocol-level version and stale-notification checks that are difficult to trigger manually.
