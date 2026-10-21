// Copyright 2026 Defense Unicorns
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-Defense-Unicorns-Commercial

package lsp

import (
	"bufio"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadMessageRejectsOversizedHeaderLine(t *testing.T) {
	input := strings.Repeat("x", maxHeaderLineSize+1) + "\n\n"

	reader := bufio.NewReaderSize(strings.NewReader(input), maxHeaderLineSize+1)
	_, err := readMessage(reader)

	require.ErrorContains(t, err, "LSP header line exceeds")
}
