package lsp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/defenseunicorns/uds-cli/internal/bundle"
	"github.com/defenseunicorns/uds-cli/pkg/iostreams"
)

func (s *documentStore) initializeWorkspace(params initializeParams) {
	s.workspaceFolders = make(map[string]workspaceFolder)
	folders := params.WorkspaceFolders
	// An explicitly empty workspaceFolders list means there are no roots.
	// Older clients can supply rootUri, or the deprecated local rootPath.
	if folders == nil {
		uri := params.RootURI
		if uri == "" && filepath.IsAbs(params.RootPath) {
			uri = localFileURI(params.RootPath)
		}
		if uri != "" {
			folders = []workspaceFolder{{URI: uri}}
		}
	}
	for _, folder := range folders {
		if filename, ok := fileURIPath(folder.URI); ok {
			s.workspaceFolders[filename] = folder
		}
	}
}

func (s *documentStore) changeWorkspaceFolders(params changeWorkspaceFoldersParams) {
	if s.workspaceFolders == nil {
		s.workspaceFolders = make(map[string]workspaceFolder)
	}
	for _, folder := range params.Event.Removed {
		if filename, ok := fileURIPath(folder.URI); ok {
			delete(s.workspaceFolders, filename)
		}
	}
	for _, folder := range params.Event.Added {
		if filename, ok := fileURIPath(folder.URI); ok {
			s.workspaceFolders[filename] = folder
		}
	}
}

func indexBundleWorkspace(ctx context.Context, root, operation string) (string, *documentStore, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", nil, fmt.Errorf("resolve %s path %q: %w", operation, root, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", nil, fmt.Errorf("access %s path %q: %w", operation, root, err)
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("%s path %q is not a directory", operation, root)
	}
	store := newDocumentStore()
	store.initializeWorkspace(initializeParams{WorkspaceFolders: []workspaceFolder{{URI: localFileURI(root)}}})
	if err := store.indexWorkspace(ctx); err != nil {
		return "", nil, fmt.Errorf("index %s workspace %q: %w", operation, root, err)
	}
	for filename := range store.workspaceFiles {
		if filepath.Base(filename) == bundle.BundleFileName {
			return root, store, nil
		}
	}
	return "", nil, fmt.Errorf("%s workspace %q: %w", operation, root, ErrNoBundleDefinitions)
}

// Scan only editor-selected roots. References from discovered bundles still
// support author-selected files outside those roots, as local authoring does.
// The index stores paths; sourceDocument overlays current editor buffers when
// validation reads a file. No scan is needed for ordinary buffer edits.
func (s *documentStore) indexWorkspace(ctx context.Context) error {
	files := make(map[string]struct{})
	visited := make(map[string]struct{})
	var errs []error
	for _, root := range slices.Sorted(maps.Keys(s.workspaceFolders)) {
		err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("scan workspace path %q: %w", filename, err))
				return nil // An inaccessible subtree must not hide other bundles.
			}
			if entry.IsDir() {
				if filename != root && excludedWorkspaceDirectory(entry.Name()) {
					return filepath.SkipDir
				}
				if _, found := visited[filename]; found {
					return filepath.SkipDir
				}
				visited[filename] = struct{}{}
				return nil
			}
			if isWorkspaceHCL(filename) {
				files[filename] = struct{}{}
			}
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
		if ctx.Err() != nil {
			return ctx.Err() // Preserve the previous index on cancellation.
		}
	}
	s.workspaceFiles = files
	return errors.Join(errs...)
}

func (s *documentStore) reindexWorkspace(ctx context.Context, streams iostreams.IOStreams) {
	if err := s.indexWorkspace(ctx); err != nil && ctx.Err() == nil {
		streams.Warn("LSP workspace discovery skipped inaccessible paths", "error", err)
	}
}

// Saving a newly created HCL file should retain it in the index even when the
// client does not support watched-file notifications.
func (s *documentStore) indexSavedDocument(uri string) {
	filename, ok := fileURIPath(uri)
	if !ok || !isWorkspaceHCL(filename) || !s.workspaceIncludes(filename) {
		return
	}
	info, err := os.Stat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	if s.workspaceFiles == nil {
		s.workspaceFiles = make(map[string]struct{})
	}
	s.workspaceFiles[filename] = struct{}{}
}

func (s *documentStore) workspaceIncludes(filename string) bool {
	for root := range s.workspaceFolders {
		rel, err := filepath.Rel(root, filename)
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		excluded := false
		for _, dir := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
			if excludedWorkspaceDirectory(dir) {
				excluded = true
				break
			}
		}
		if !excluded {
			return true
		}
	}
	return false
}

func isWorkspaceHCL(filename string) bool {
	return filepath.Base(filename) == bundle.BundleFileName || isVariablesFile(filename)
}

func excludedWorkspaceDirectory(name string) bool {
	switch name {
	case ".git", "vendor", "node_modules", "build", "dist":
		return true
	default:
		return false
	}
}
