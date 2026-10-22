package lsp

import (
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
)

var templateErrorPosition = regexp.MustCompile(`:(\d+)(?::(\d+))?:`)

func valuesTemplateDiagnostic(doc document, variables bundle.Variables, validVars bool) *diagnostic {
	filename, _ := fileURIPath(doc.URI)
	tmpl, err := template.New(filepath.Base(filename)).Option("missingkey=error").Parse(doc.Text)
	if err == nil && validVars && variables != nil {
		// Match deployment's nil-variable bypass. Execute into a sink: this pass
		// validates Go templates without materializing rendered values on disk.
		err = tmpl.Execute(io.Discard, map[string]any{"vars": variables})
	}
	if err == nil {
		return nil
	}
	return &diagnostic{
		Range: templateDiagnosticRange(doc.Text, err.Error()), Severity: 1,
		Source: "uds-lsp", Message: err.Error(),
	}
}

func templateDiagnosticRange(text, message string) documentRange {
	match := templateErrorPosition.FindStringSubmatch(message)
	if match == nil {
		return documentRange{}
	}
	line, _ := strconv.Atoi(match[1])
	column, _ := strconv.Atoi(match[2])
	start := 0
	for i := 1; i < line; i++ {
		next := strings.IndexByte(text[start:], '\n')
		if next < 0 {
			return documentRange{}
		}
		start += next + 1
	}
	end := strings.IndexByte(text[start:], '\n')
	if end < 0 {
		end = len(text)
	} else {
		end += start
	}
	if column > 0 {
		offset := min(start+column-1, end)
		if action := strings.LastIndex(text[start:offset], "{{"); action >= 0 {
			start += action
			if closing := strings.Index(text[offset:end], "}}"); closing >= 0 {
				end = offset + closing + 2
			}
		} else {
			start = offset
		}
	}
	return documentRange{Start: positionAtByte(text, start), End: positionAtByte(text, end)}
}
