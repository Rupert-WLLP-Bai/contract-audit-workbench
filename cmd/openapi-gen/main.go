// Command openapi-gen emits the embedded OpenAPI artifact from the typed Go
// contract. The checked-in JSON is generated output, never the edit source.
package main

import (
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
	if err := generate(); err != nil {
		_, _ = os.Stderr.WriteString("openapi-gen: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func generate() error {
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
	return os.WriteFile(filepath.Join(root, "internal/apidocs/openapi.json"), data, 0o644)
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
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			parts := strings.Fields(value)
			if len(parts) == 2 && strings.HasPrefix(parts[1], "/") {
				switch parts[0] {
				case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD", "TRACE":
					registered[parts[0]+" "+parts[1]] = true
				}
			} else if len(parts) == 1 && strings.HasPrefix(parts[0], "/") {
				// Plain paths are ServeMux registrations that accept all methods.
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
	return nil
}
