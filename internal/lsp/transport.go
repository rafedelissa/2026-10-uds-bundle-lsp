// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	maxHeaderLineSize = 8 << 10
	maxMessageSize    = 16 << 20
)

// readMessage reads one Content-Length-framed LSP message.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#baseProtocol.
func readMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		lineBytes, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) || len(lineBytes) > maxHeaderLineSize {
			return nil, fmt.Errorf("LSP header line exceeds %d byte limit", maxHeaderLineSize)
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(lineBytes) == 0 {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("read LSP header: %w", err)
		}

		line := strings.TrimSuffix(strings.TrimSuffix(string(lineBytes), "\n"), "\r")
		if line == "" {
			break
		}

		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid LSP header %q", line)
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		if contentLength >= 0 {
			return nil, errors.New("duplicate Content-Length header")
		}

		contentLength, err = strconv.Atoi(strings.TrimSpace(value))
		if err != nil || contentLength < 0 {
			return nil, fmt.Errorf("invalid Content-Length header %q", value)
		}
		if contentLength > maxMessageSize {
			return nil, fmt.Errorf("LSP message exceeds %d byte limit", maxMessageSize)
		}
	}

	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}

	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, fmt.Errorf("read LSP payload: %w", err)
	}
	return payload, nil
}

// writeMessage writes one Content-Length-framed JSON-RPC response or notification.
// See https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/#baseProtocol.
func writeMessage(writer *bufio.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode LSP message: %w", err)
	}
	header := "Content-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n"
	if _, err := writer.WriteString(header); err != nil {
		return fmt.Errorf("write LSP header: %w", err)
	}
	if _, err := writer.Write(payload); err != nil {
		return fmt.Errorf("write LSP payload: %w", err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush LSP response: %w", err)
	}
	return nil
}
