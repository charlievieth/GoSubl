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
	for _, name := range []string{"go.mod", ".git", "vendor", "glide.yaml", "Gopkg.toml"} {
		if _, err := os.Lstat(dir + "/" + name); err == nil {
			return true
		}
	}
	return false
}

func hasPathPrefix(s, prefix string) bool {
	rel, err := filepath.Rel(prefix, s)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// shiftPathElements shifts count path elements from path from to path to.
func shiftPathElements(to, from string, count int) (string, string) {
	a := strings.Split(filepath.ToSlash(from), "/")
	if count > len(a) {
		count = len(a)
	}
	to += "/" + strings.Join(a[:count], "/")
	from = strings.Join(a[count:], "/")

	return filepath.FromSlash(to), filepath.FromSlash(from)
}

func projectRoot(ctxt *build.Context, dirname string) string {
	const sep = string(filepath.Separator)

	// WARN
	if ctxt == nil {
		ctxt = &build.Default
	}

	root := ctxt.GOROOT + sep + "src"
	if hasPathPrefix(dirname, root) {
		return root
	}

	dirname = filepath.Clean(dirname)

	var subpath string
	var rootpath string
	gopaths := strings.Split(ctxt.GOPATH, string(os.PathListSeparator))
	for _, p := range gopaths {
		p += sep + "src"
		s, err := filepath.Rel(p, dirname)
		if err != nil {
			continue
		}
		if s == "." {
			// WARN: you cannot have go files at the root of GOPATH
			// but there's nothing we can do about that here it will
			// simply result in an error from whatever tool we invoke.
			return p
		}
		if s != "" && !strings.HasPrefix(s, "..") {
			subpath = s
			rootpath = p
			break
		}
	}

	// fmt.Printf("subpath: %q\n", subpath)
	// fmt.Printf("rootpath: %q\n", rootpath)
	if subpath == "" {
		// Search for a project dir outside of the GOPATH (basically, search
		// without stopping once we've walked outside the GOPATH)
		//
		d := dirname
		for next := filepath.Dir(d); d != next; d, next = next, filepath.Dir(d) {
			if isRoot(d) {
				return d
			}
		}
		return d
	}

	// CEV: special case for me
	if subpath == "repl" || strings.HasPrefix(subpath, "repl/") ||
		(sep == "\\" && strings.HasPrefix(subpath, "repl\\")) {

		return filepath.Join(rootpath, "repl")
	}

	// CEV: since projects cannot live at $GOPATH and are typically at least two
	// directories removed from the $GOPATH we shift two path elements from the
	// subdir to the rootdir:
	//
	//  ~/go/src ... gh.com/foo/bar
	// 	 =>
	//  ~/src/gh.com/foo ... bar
	//
	rootpath, subpath = shiftPathElements(rootpath, subpath, 2)

	a := strings.Split(subpath, "/")
	for i := len(a); i >= 0; i-- {
		p := rootpath + "/" + strings.Join(a[:i], "/")
		// fmt.Println("p:", p)
		if isRoot(p) {
			return filepath.Clean(p) // WARN: prob don't need clean
		}
	}

	return dirname
}

func importPaths(environ map[string]string, installSuffix, importDir string) ([]string, error) {

	cacheRoot := projectRoot(contextFromEnv(environ), importDir)
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
