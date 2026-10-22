# Editor fixture: the OCI source is a placeholder and is never fetched by the LSP.
uds {
  bundle_api_version = "uds.dev/v1alpha1"
}

metadata {
  name    = "lsp-broken-values"
  version = "0.0.0"
}

package "app" {
  source = "oci://example.invalid/lsp/demo:${sys.arch}"
  values_files = [
    "values/missing-variable.yaml",
    "values/missing-nested.yaml",
    "values/object-replaced.yaml",
    "values/bad-range.yaml",
    "values/bad-index.yaml",
    "values/unclosed.yaml",
    "values/unknown-function.yaml",
    "values/unicode.yaml",
    "values/unexecuted-branch.yaml",
  ]
}
