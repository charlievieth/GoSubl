package main

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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

type mImportPathsDeclByName []mImportPathsDecl

func (m mImportPathsDeclByName) Len() int           { return len(m) }
func (m mImportPathsDeclByName) Swap(i, j int)      { m[i], m[j] = m[j], m[i] }
func (m mImportPathsDeclByName) Less(i, j int) bool { return m[i].Name < m[j].Name }

type mImportPathsResponse struct {
	Imports []mImportPathsDecl `json:"imports"`
	Paths   []string           `json:"paths"`
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
			} else {
				name = pathpkg.Base(path)
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
	if err != nil && len(names) == 0 {
		return M{}, errStr(err)
	}

	// dedupe since there may be duplicate vendored imports
	// names is sorted
	if len(names) > 0 {
		i := 0
		s := ""
		for _, x := range names {
			if x != s {
				names[i] = x
				s = x
				i++
			}
		}
		names = names[:i]
	}
	sort.Sort(mImportPathsDeclByName(imports))

	return &mImportPathsResponse{Imports: imports, Paths: names}, ""
}

type importsPathCacheEntry struct {
	Created time.Time
	Imports []string
}

// project root => *importsPathCacheEntry
var importsPathCache sync.Map

func init() {
	const TTL = time.Minute * 2

	go func() {
		for {
			time.Sleep(TTL / 4)

			importsPathCache.Range(func(key, value interface{}) bool {
				if e, ok := value.(*importsPathCacheEntry); ok {
					if time.Since(e.Created) > TTL {
						importsPathCache.Delete(key)
					}
				}
				return true
			})
		}
	}()
}

func isRoot(dir string) bool {
	// TODO: add ".svn" ".hg" ???
	for _, name := range []string{"vendor", "go.mod", ".git", "glide.yaml", "Gopkg.toml"} {
		if _, err := os.Lstat(dir + "/" + name); err == nil {
			return true
		}
	}
	return false
}

func projectRoot(dirname string) string {
	const sep = string(os.PathListSeparator)

	// special case for me
	for _, path := range strings.Split(build.Default.GOPATH, sep) {
		pfx := filepath.Join(path, "src", "repl")
		if strings.HasPrefix(dirname, pfx) {
			return pfx
		}
	}

	dir := filepath.ToSlash(dirname)
	for !isRoot(dir) {
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return dir
}

func importPaths(environ map[string]string, installSuffix, importDir string) ([]string, error) {
	cacheRoot := projectRoot(importDir)
	if v, ok := importsPathCache.Load(cacheRoot); ok {
		if e, _ := v.(*importsPathCacheEntry); e != nil {
			a := make([]string, len(e.Imports))
			copy(a, e.Imports)
			return a, nil
		}
	}

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

	paths, err := pkgs.Walk(&ctxt, importDir)
	if len(paths) != 0 {
		sort.Strings(paths)
	}
	if err != nil {
		return paths, err
	}

	importsPathCache.Store(cacheRoot, &importsPathCacheEntry{
		Created: time.Now(),
		Imports: append([]string(nil), paths...),
	})
	return paths, nil
}

func init() {
	registry.Register("import_paths", func(_ *Broker) Caller {
		return &mImportPaths{
			Env: map[string]string{},
		}
	})
}
