# Editor fixture: the OCI source is a placeholder and is never fetched by the LSP.
uds {
  bundle_api_version = "uds.dev/v1alpha1"
}

metadata {
  name    = "lsp-shared-a"
  version = "0.0.0"
}

package "app" {
  source = "oci://example.invalid/lsp/demo:${sys.arch}"
  values_files = [
    "../shared/per-bundle.yaml",
  ]
}
