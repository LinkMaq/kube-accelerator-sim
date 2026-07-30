package main

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const domainImportPrefix = "github.com/LinkMaq/kube-accelerator-sim/internal/domain"

var forbiddenCatchAllPackages = map[string]struct{}{
	"internal/backend":   {},
	"internal/common":    {},
	"internal/providers": {},
	"internal/utils":     {},
}

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	if err := check(*root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func check(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkError error) error {
		if walkError != nil {
			return walkError
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			slashPath := filepath.ToSlash(relative)
			if _, forbidden := forbiddenCatchAllPackages[slashPath]; forbidden {
				return fmt.Errorf("forbidden catch-all package %s", slashPath)
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		slashPath := filepath.ToSlash(relative)
		if !strings.HasPrefix(slashPath, "internal/domain/") {
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("%s: %w", slashPath, err)
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return fmt.Errorf("%s: invalid import %s", slashPath, imported.Path.Value)
			}
			if forbiddenDomainImport(importPath) {
				return fmt.Errorf(
					"internal/domain cannot import %s (%s)",
					importPath,
					slashPath,
				)
			}
		}
		return nil
	})
}

func forbiddenDomainImport(importPath string) bool {
	if importPath == domainImportPrefix ||
		strings.HasPrefix(importPath, domainImportPrefix+"/") {
		return false
	}

	firstPathElement, _, _ := strings.Cut(importPath, "/")
	return strings.Contains(firstPathElement, ".")
}
