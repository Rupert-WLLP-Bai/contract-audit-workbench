// File metadata (manifest mirror):
// Purpose: 从 typed Go API contracts 和已登记 routes 生成嵌入的 OpenAPI JSON，并以 --check 比较生成结果。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

// Command openapi-gen emits the embedded OpenAPI artifact from the typed Go
// contract. The checked-in JSON is generated output, never the edit source.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"contractaudit/internal/apidocs"
)

func main() {
	check := flag.Bool("check", false, "fail if generated OpenAPI JSON is stale")
	flag.Parse()
	if err := generate(*check); err != nil {
		_, _ = os.Stderr.WriteString("openapi-gen: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func generate(check bool) error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	if err := checkRegisteredRoutes(root); err != nil {
		return err
	}
	data, err := apidocs.Generate()
	if err != nil {
		return err
	}
	target := filepath.Join(root, "internal/apidocs/openapi.json")
	if check {
		current, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, data) {
			return fmt.Errorf("%s is stale; run make generate-openapi", filepath.Join("internal", "apidocs", "openapi.json"))
		}
		return nil
	}
	return os.WriteFile(target, data, 0o644)
}

func repositoryRoot() (string, error) {
	current, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("could not locate go.mod from generator working directory")
		}
		current = parent
	}
}

// checkRegisteredRoutes ensures every documented operation path has a
// corresponding literal ServeMux registration in the HTTP implementation.
func checkRegisteredRoutes(root string) error {
	registered := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "internal/httpapi"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			var literal ast.Expr
			switch value := node.(type) {
			case *ast.CallExpr:
				selector, ok := value.Fun.(*ast.SelectorExpr)
				if ok && (selector.Sel.Name == "HandleFunc" || selector.Sel.Name == "Handle") && len(value.Args) > 0 {
					literal = value.Args[0]
				}
			case *ast.CompositeLit:
				ident, ok := value.Type.(*ast.Ident)
				if ok && ident.Name == "routeBinding" && len(value.Elts) > 0 {
					if kv, ok := value.Elts[0].(*ast.KeyValueExpr); ok {
						literal = kv.Value
					} else {
						literal = value.Elts[0]
					}
				}
			}
			lit, ok := literal.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			parts := strings.Fields(value)
			if len(parts) == 2 && strings.HasPrefix(parts[1], "/") && isHTTPMethod(parts[0]) {
				registered[parts[0]+" "+parts[1]] = true
			} else if len(parts) == 1 && strings.HasPrefix(parts[0], "/") {
				registered["* "+parts[0]] = true
			}
			return true
		})
		return nil
	})
	if err != nil {
		return err
	}
	for path, item := range apidocs.SourceContract().Paths {
		operations := []struct {
			method  string
			present bool
		}{
			{"GET", item.Get != nil}, {"POST", item.Post != nil}, {"PUT", item.Put != nil}, {"PATCH", item.Patch != nil},
			{"DELETE", item.Delete != nil}, {"OPTIONS", item.Options != nil}, {"HEAD", item.Head != nil}, {"TRACE", item.Trace != nil},
		}
		for _, operation := range operations {
			method := operation.method
			if !operation.present {
				continue
			}
			if !registered[method+" "+path] && !registered["* "+path] {
				return fmt.Errorf("documented route %s %s is not registered in internal/httpapi", method, path)
			}
		}
	}
	covered := map[string]bool{}
	for path, item := range apidocs.SourceContract().Paths {
		for _, present := range []struct {
			method string
			yes    bool
		}{{"GET", item.Get != nil}, {"POST", item.Post != nil}, {"PUT", item.Put != nil}, {"PATCH", item.Patch != nil}, {"DELETE", item.Delete != nil}, {"HEAD", item.Head != nil}, {"OPTIONS", item.Options != nil}, {"TRACE", item.Trace != nil}} {
			if present.yes {
				covered[present.method+" "+path] = true
			}
		}
	}
	for route := range registered {
		method, path, _ := strings.Cut(route, " ")
		if !strings.HasPrefix(path, "/api/") {
			continue
		} // health and internal prefix dispatch are checked only document-to-registration.
		if method == "*" {
			found := false
			for candidate := range covered {
				_, p, _ := strings.Cut(candidate, " ")
				if p == path {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("public route %s has no OpenAPI operation", path)
			}
		} else if !covered[route] {
			return fmt.Errorf("public route %s has no OpenAPI operation", route)
		}
	}
	return nil
}

func isHTTPMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD", "TRACE":
		return true
	}
	return false
}
