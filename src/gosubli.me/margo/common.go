package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"hash/maphash"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type JsonString string

func (s JsonString) String() string {
	return string(s)
}

func (s *JsonString) UnmarshalJSON(p []byte) error {
	if bytes.Equal(p, []byte("null")) {
		return nil
	}
	return json.Unmarshal(p, (*string)(s))
}

type JsonData []byte

func (d JsonData) String() string {
	return string(d)
}

func (d JsonData) MarshalJSON() ([]byte, error) {
	const pad = len(`"base64:"`)
	if len(d) == 0 {
		return []byte(`""`), nil
	}
	b := make([]byte, base64.StdEncoding.EncodedLen(len(d))+pad)
	base64.StdEncoding.Encode(b[copy(b, `"base64:`):], d)
	b[len(b)-1] = '"'
	return b, nil
}

func errStr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func envSlice(envMap map[string]string) []string {
	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}
	if len(env) == 0 {
		env = os.Environ()
	}
	return env
}

func defaultEnv() map[string]string {
	return map[string]string{
		"GOROOT": runtime.GOROOT(),
		"GOARCH": runtime.GOARCH,
		"GOOS":   runtime.GOOS,
	}
}

func parseAstFile(fn string, s string, mode parser.Mode) (fset *token.FileSet, af *ast.File, err error) {
	fset = token.NewFileSet()
	var src interface{}
	if s != "" {
		src = s
	}
	if fn == "" {
		fn = "<stdin>"
	}
	af, err = parser.ParseFile(fset, fn, src, mode)
	return
}

func fiHasGoExt(fi os.FileInfo) bool {
	return strings.HasSuffix(fi.Name(), ".go")
}

func parsePkg(fset *token.FileSet, srcDir string, mode parser.Mode) (pkg *ast.Package, pkgs map[string]*ast.Package, err error) {
	if pkgs, err = parser.ParseDir(fset, srcDir, fiHasGoExt, mode); pkgs != nil {
		_, pkgName := filepath.Split(srcDir)
		// we aren't going to support package whose name don't match the directory unless it's main
		p, ok := pkgs[pkgName]
		if !ok {
			p, ok = pkgs["main"]
		}
		if ok {
			pkg, err = ast.NewPackage(fset, p.Files, nil, nil)
		}
	}
	return
}

func rootDirs(env map[string]string) []string {
	dirs := []string{}
	gopath := ""
	if len(env) == 0 {
		gopath = os.Getenv("GOPATH")
	} else {
		gopath = env["GOPATH"]
	}

	gorootBase := runtime.GOROOT()
	if len(env) > 0 && env["GOROOT"] != "" {
		gorootBase = env["GOROOT"]
	} else if fn := os.Getenv("GOROOT"); fn != "" {
		gorootBase = fn
	}
	goroot := filepath.Join(gorootBase, SrcPkg)

	dirsSeen := map[string]bool{}
	for _, fn := range filepath.SplitList(gopath) {
		if dirsSeen[fn] {
			continue
		}
		dirsSeen[fn] = true

		// goroot may be a part of gopath and we don't want that
		if fn != "" && !strings.HasPrefix(fn, gorootBase) {
			fn := filepath.Join(fn, "src")
			if fi, err := os.Stat(fn); err == nil && fi.IsDir() {
				dirs = append(dirs, fn)
			}
		}
	}

	if fi, err := os.Stat(goroot); err == nil && fi.IsDir() {
		dirs = append(dirs, goroot)
	}

	return dirs
}

func findPkg(fset *token.FileSet, importPath string, dirs []string, mode parser.Mode) (pkg *ast.Package, pkgs map[string]*ast.Package, err error) {
	for _, dir := range dirs {
		srcDir := filepath.Join(dir, importPath)
		if pkg, pkgs, err = parsePkg(fset, srcDir, mode); pkg != nil {
			return
		}
	}
	return
}

func pathList(p string) []string {
	a := strings.Split(p, string(filepath.ListSeparator))
	n := 0
	for i, s := range a {
		if len(s) > 0 {
			a[n] = a[i]
			n++
		}
	}
	return a[:n]
}

func envRootList(env map[string]string) (string, []string) {
	if env == nil {
		return "", []string{}
	}
	return env["GOROOT"], pathList(env["GOPATH"])
}

func isDir(name string) bool {
	if name == "" {
		return false
	}
	fi, err := os.Stat(name)
	return err == nil && fi.IsDir()
}

var maphashSeed = maphash.MakeSeed()

func fileCacheKey(name, source string) string {
	if len(source) <= 32*1024 {
		return source
	}
	// Use a hash for larger files so that we don't
	// store the full source in memory
	var h maphash.Hash
	h.SetSeed(maphashSeed)
	h.WriteString(source)
	return fmt.Sprintf("%s|%d|%d", filepath.Clean(name), len(source), h.Sum64())
}
