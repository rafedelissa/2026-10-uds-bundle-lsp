variables = {
  replicas = 2
  enabled  = true

  database = {
    host = "database.local"
    port = 5432
  }

  ports = [
    { name = "default-http", port = 8080 },
    { name = "default-metrics", port = 9090 },
  ]

  labels = {
    "app.kubernetes.io/name" = "lsp-playground"
  }
}
