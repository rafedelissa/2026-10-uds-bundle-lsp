# Change Log

All notable changes to the "uds-lsp-extension" extension will be documented in this file.

Check [Keep a Changelog](http://keepachangelog.com/) for recommendations on how to structure this file.

## [Unreleased]

- Bundle the language server in platform-specific VSIX packages for macOS,
  Linux, and Windows on x64 and arm64.
- Select the bundled server automatically, use the repository build during
  development, and retain `udsLsp.serverPath` as an explicit override.
- Initial release
