options {
  architecture = "amd64"
  concurrency  = 2
}

variables = {
  replicas = 3

  # host is retained from defaults; only port changes.
  database = {
    port = 6432
  }

  # This replaces both default entries.
  ports = [
    { name = "configured-http", port = 8443 },
  ]
}
