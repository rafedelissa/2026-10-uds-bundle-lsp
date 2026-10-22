package lsp

import "bufio"

func (s *documentStore) registerFileWatcher(writer *bufio.Writer) error {
	if !s.watchFilesSupport || s.watchRegistrationSent {
		return nil
	}
	// Let the client watch workspace files; keeping filesystem events on the
	// protocol loop avoids concurrent reads and writes to document state.
	err := writeMessage(writer, struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{
		JSONRPC: jsonRPCVersion,
		ID:      "uds-lsp-watch-files",
		Method:  "client/registerCapability",
		Params: map[string]any{"registrations": []any{map[string]any{
			"id":     "uds-lsp-watch-files",
			"method": "workspace/didChangeWatchedFiles",
			"registerOptions": map[string]any{"watchers": []any{
				map[string]any{"globPattern": "**/*", "kind": 7},
			}},
		}}},
	})
	if err != nil {
		return err
	}
	s.watchRegistrationSent = true
	s.watchRegistrationPending = true
	return nil
}
