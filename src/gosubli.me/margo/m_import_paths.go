package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/charlievieth/pkgs"
)

type mImportPaths struct {
	Fn            string
	Src           string
	Env           map[string]string
	InstallSuffix string
}

type mImportPathsDecl struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type mImportPathsResponse struct {
	Imports []mImportPathsDecl `json:"imports"`
	Paths   map[string]string  `json:"paths"`
}

func (m *mImportPaths) FileImports() ([]mImportPathsDecl, error) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, m.Fn, m.Src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}

	imports := make([]mImportPathsDecl, 0, 8)
	for _, decl := range af.Decls {
		d, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, dspec := range d.Specs {
			spec, ok := dspec.(*ast.ImportSpec)
			if !ok {
				continue
			}
			quoted := spec.Path.Value
			path, err := strconv.Unquote(quoted)
			if err != nil {
				return nil, fmt.Errorf("%s: parser returned invalid quoted string: <%s>", m.Fn, quoted)
			}
			var name string
			if spec.Name != nil {
				name = spec.Name.String()
			}
			imports = append(imports, mImportPathsDecl{
				Path: path,
				Name: name,
			})
		}
	}

	return imports, nil
}

func (m *mImportPaths) Call() (interface{}, string) {
	imports, err := m.FileImports()
	if err != nil {
		return M{}, errStr(err)
	}

	names, err := importPaths(m.Env, m.InstallSuffix, filepath.Dir(m.Fn))
	if err != nil {
		return nil, errStr(err)
	}
	paths := make(map[string]string, len(names))
	for _, p := range names {
		paths[p] = ""
	}

	res := mImportPathsResponse{
		Imports: imports,
		Paths:   paths,
	}
	return res, ""
}

func importPaths(environ map[string]string, installSuffix, importDir string) ([]string, error) {
	// TODO:
	// 	- Consider adding os.GOROOT and os.GOPATH to environ
	// 	- Check for duplicate paths
	var root string
	if s := environ["GOROOT"]; s != "" {
		root = s
	} else {
		root = runtime.GOROOT()
	}
	var path string
	if s := environ["GOPATH"]; s != "" {
		path = s
	} else {
		path = os.Getenv("GOPATH")
	}
	ctxt := build.Default
	if root != "" {
		ctxt.GOROOT = root
	}
	if path != "" {
		ctxt.GOPATH = path
	}
	if installSuffix != "" {
		ctxt.InstallSuffix = installSuffix
	}

	return pkgs.Walk(&ctxt, importDir)
}

func init() {
	registry.Register("import_paths", func(_ *Broker) Caller {
		return &mImportPaths{
			Env: map[string]string{},
		}
	})
}
