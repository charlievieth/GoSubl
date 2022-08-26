package testutil

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/charlievieth/buildutil"
)

var ErrNoTestFiles = errors.New("no test files")

type MultiError struct {
	Errors []error
}

func newMultiError(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return &MultiError{Errors: append([]error(nil), errs...)}
}

func (e *MultiError) UnWrap() error {
	if len(e.Errors) == 0 {
		return nil
	}
	return e.Errors[0]
}

func (e *MultiError) Error() string {
	switch len(e.Errors) {
	case 0:
		return "testutil: MultiError: no errors"
	case 1:
		return e.Errors[0].Error()
	default:
		return fmt.Sprintf("testutil: %s: and %d other errors", e.Errors[0], len(e.Errors)-1)
	}
}

type visitor struct {
	Tests      []*ast.FuncDecl
	Benchmarks []*ast.FuncDecl
	Examples   []*ast.FuncDecl
	FuzzTests  []*ast.FuncDecl
}

func (v *visitor) IsEmpty() bool {
	return len(v.Tests) == 0 && len(v.Benchmarks) == 0 &&
		len(v.Examples) == 0 && len(v.FuzzTests) == 0
}

type funcDeclByName []*ast.FuncDecl

func (d funcDeclByName) Len() int           { return len(d) }
func (d funcDeclByName) Less(i, j int) bool { return d[i].Name.Name < d[j].Name.Name }
func (d funcDeclByName) Swap(i, j int)      { d[i], d[j] = d[i], d[j] }

func (v *visitor) Visit(node ast.Node) (w ast.Visitor) {
	if d, ok := node.(*ast.FuncDecl); ok && d != nil && d.Name != nil {
		switch name := d.Name.Name; {
		case strings.HasPrefix(name, "Test"):
			v.Tests = append(v.Tests, d)
		case strings.HasPrefix(name, "Benchmark"):
			v.Benchmarks = append(v.Benchmarks, d)
		case strings.HasPrefix(name, "Example"):
			v.Examples = append(v.Examples, d)
		case strings.HasPrefix(name, "Fuzz"):
			v.FuzzTests = append(v.FuzzTests, d)
		}
	}
	return v
}

type TestFunc struct {
	Name     string `json:"name"`
	Filename string `json:"filename"`
	Line     int    `json:"line"`
}

func FuncDeclsToTestFuncs(fset *token.FileSet, decls []*ast.FuncDecl) []TestFunc {
	if len(decls) == 0 {
		return nil
	}
	fns := make([]TestFunc, 0, len(decls))
	for _, d := range decls {
		if d.Name == nil || !d.Pos().IsValid() {
			continue
		}
		pos := fset.Position(d.Pos())
		fns = append(fns, TestFunc{
			Name:     d.Name.Name,
			Filename: pos.Filename,
			Line:     pos.Line,
		})
	}
	return fns
}

type TestFunctions struct {
	Tests      []TestFunc `json:"tests,omitempty"`
	Benchmarks []TestFunc `json:"benchmarks,omitempty"`
	Examples   []TestFunc `json:"examples,omitempty"`
	FuzzTests  []TestFunc `json:"fuzz_tests,omitempty"`
}

func (t *TestFunctions) IsEmpty() bool {
	return len(t.Tests) == 0 && len(t.Benchmarks) == 0 &&
		len(t.Examples) == 0 && len(t.FuzzTests) == 0
}

func ListTestFunctions(ctxt *build.Context, dirname string) (*TestFunctions, error) {
	dirname = filepath.Clean(dirname)
	names, err := readdirnames(dirname)
	if err != nil && len(names) == 0 {
		return nil, err
	}

	var (
		visitors []*visitor
		errs     []error
		wg       sync.WaitGroup
		mu       sync.Mutex
	)
	fset := token.NewFileSet()
	for _, name := range names {
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}
		if !buildutil.GoodOSArchFile(ctxt, name, nil) {
			continue
		}
		wg.Add(1)
		go func(filename string) {
			defer wg.Done()
			if !buildutil.Include(ctxt, filename) {
				return
			}
			af, err := parser.ParseFile(fset, filename, nil, parser.SkipObjectResolution)
			if err != nil && af == nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			} else {
				v := new(visitor)
				ast.Walk(v, af)
				if !v.IsEmpty() {
					mu.Lock()
					visitors = append(visitors, v)
					mu.Unlock()
				}
			}
		}(dirname + string(os.PathSeparator) + name)
	}
	wg.Wait()

	res := &TestFunctions{}
	for _, v := range visitors {
		res.Tests = append(res.Tests, FuncDeclsToTestFuncs(fset, v.Tests)...)
		res.Benchmarks = append(res.Benchmarks, FuncDeclsToTestFuncs(fset, v.Benchmarks)...)
		res.Examples = append(res.Examples, FuncDeclsToTestFuncs(fset, v.Examples)...)
		res.FuzzTests = append(res.FuzzTests, FuncDeclsToTestFuncs(fset, v.FuzzTests)...)
	}
	for _, p := range []*[]TestFunc{&res.Tests, &res.Benchmarks, &res.Examples, &res.FuzzTests} {
		if len(*p) != 0 {
			sort.Slice(*p, func(i, j int) bool {
				return (*p)[i].Name < (*p)[j].Name
			})
		}
	}

	return res, newMultiError(errs)
}

type VisitorFunc func(node ast.Node) bool

func (fn VisitorFunc) Visit(node ast.Node) ast.Visitor {
	if fn(node) {
		return fn
	}
	return nil
}

func ContainingFunction(filename string, src interface{}, line int) (string, error) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil && af == nil {
		return "", err
	}

	file := fset.File(af.Pos())
	if file == nil {
		return "", errors.New("ast: no pos for file")
	}
	if n := file.LineCount(); line < 1 || line > n {
		return "", fmt.Errorf("ast: invalid line number %d (should be between 1 and %d)", line, n)
	}
	pos := file.LineStart(line)
	if !pos.IsValid() {
		return "", fmt.Errorf("ast: invalid pos for line: %d", line)
	}

	// Fast check
	for _, node := range af.Decls {
		if d, ok := node.(*ast.FuncDecl); ok && d != nil {
			if d.Pos() <= pos && pos <= d.End() {
				if d.Name != nil {
					return d.Name.Name, nil
				}
			}
		}
	}

	var fn *ast.FuncDecl
	v := VisitorFunc(func(node ast.Node) bool {
		if d, ok := node.(*ast.FuncDecl); ok && d != nil {
			if d.Pos() <= pos && pos <= d.End() {
				fn = d
				return false // stop
			}
		}
		return true
	})
	ast.Walk(v, af)

	if fn != nil && fn.Name != nil {
		return fn.Name.Name, nil
	}
	return "", fmt.Errorf("ast: no containing function at: %s:%d", filename, line)
}

func readdirnames(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	return names, err
}

func readGoTestFiles(dir string) ([]string, error) {
	names, err := readdirnames(dir)
	if len(names) != 0 {
		a := names[:0]
		for _, s := range names {
			if strings.HasSuffix(s, "_test.go") {
				a = append(a, s)
			}
		}
		return a, err
	}
	return names, err
}
