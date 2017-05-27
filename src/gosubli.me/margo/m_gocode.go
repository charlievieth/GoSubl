package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charlievieth/gocode"
)

type GoCode struct {
	Autoinst      bool
	InstallSuffix string
	Env           map[string]string
	Home          string
	Dir           string
	Builtins      bool
	Fn            string
	Src           string
	Pos           int
	calltip       bool `json:"-"` // ignore
}

// Separate type so we can init Calltip to true
type GoCodeCalltip struct {
	GoCode
}

func (g *GoCodeCalltip) Init() {
	g.calltip = true
}

type GoCodeResponse struct {
	Candidates []gocode.Candidate
}

var lastCalltip struct {
	sync.Mutex
	Path string
	Src  string
	Off  int
	Res  GoCodeResponse
}

func useLastCalltip(path, src string, off int) (res GoCodeResponse, ok bool) {
	lastCalltip.Lock()
	if off == lastCalltip.Off && path == lastCalltip.Path && src == lastCalltip.Src {
		res = lastCalltip.Res
		ok = true
	}
	lastCalltip.Unlock()
	return
}

func setLastCalltip(path, src string, off int, res GoCodeResponse) {
	lastCalltip.Lock()
	if off != lastCalltip.Off || path != lastCalltip.Path || src != lastCalltip.Src {
		lastCalltip.Off = off
		lastCalltip.Path = path
		lastCalltip.Src = src
		lastCalltip.Res = res
	}
	lastCalltip.Unlock()
	return
}

func (g *GoCode) Call() (interface{}, string) {
	off, err := g.bytePos()
	if err != nil {
		return g.response(nil, err, false)
	}
	path := g.filepath()
	if g.calltip {
		if res, ok := useLastCalltip(path, g.Src, off); ok {
			return res, ""
		}
		gr, err := g.calltips(path, []byte(g.Src), off)
		res, errStr := g.response(gr, err, false)
		setLastCalltip(path, g.Src, off, res)
		return res, errStr
	}
	return g.response(g.complete(path, []byte(g.Src), off), nil, true)
}

var NoGocodeCandidates = []gocode.Candidate{}

func (g *GoCode) response(res []gocode.Candidate, err error, install bool) (GoCodeResponse, string) {
	if res == nil || len(res) == 0 {
		if install && g.Autoinst {
			autoInstall(AutoInstOptions{
				Src:           g.Src,
				Env:           g.Env,
				InstallSuffix: g.InstallSuffix,
			})
		}
		res = NoGocodeCandidates
	}
	var errStr string
	if err != nil {
		errStr = err.Error()
	}
	return GoCodeResponse{Candidates: res}, errStr
}

// Matching GoSublime's behavior here...
func (g *GoCode) filepath() string {
	if filepath.IsAbs(g.Fn) {
		return filepath.Clean(g.Fn)
	}
	var base string
	if g.Dir != "" {
		base = g.Dir
	} else {
		base = g.Home
	}
	var name string
	if g.Fn != "" {
		name = g.Fn
	} else {
		name = "_.go"
	}
	return filepath.Join(base, name)
}

func (g *GoCode) complete(filename string, src []byte, offset int) []gocode.Candidate {
	conf := gocode.Config{
		GOROOT:        g.GOROOT(),
		GOPATH:        g.GOPATH(),
		InstallSuffix: g.InstallSuffix,
		Builtins:      g.Builtins,
	}
	return conf.Complete(src, filename, offset)
}

func (g *GoCode) GOPATH() string {
	if g.Env != nil {
		if s, ok := g.Env["GOPATH"]; ok && s != "" {
			return s
		}
	}
	return os.Getenv("GOPATH")
}

func (g *GoCode) GOROOT() string {
	if g.Env != nil {
		if s, ok := g.Env["GOROOT"]; ok && s != "" {
			return s
		}
	}
	return runtime.GOROOT()
}

// TODO: cache results and potentially cache AST ad file set.
func (g *GoCode) calltips(filename string, src []byte, offset int) ([]gocode.Candidate, error) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, filename, src, 0)
	if af == nil {
		return nil, err
	}
	pos, err := g.convertOffset(af, fset, offset)
	if err != nil {
		return nil, err
	}
	id, err := g.findCaller(af, fset, pos)
	if err != nil {
		return nil, err
	}
	end := fset.Position(id.End())
	if !end.IsValid() {
		return nil, errors.New("calltip: invalid end pos")
	}
	if err := g.validLine(fset, pos, end); err != nil {
		return nil, err
	}
	cl := g.complete(filename, src, end.Offset)
	for i := 0; i < len(cl); i++ {
		if strings.EqualFold(id.Name, cl[i].Name) {
			return cl[i : i+1], nil
		}
	}
	return nil, errors.New("calltip: no candidates")
}

// Matching GoSublime's behavior here, see if this can be removed.
func (g *GoCode) validLine(fset *token.FileSet, off token.Pos, end token.Position) error {
	line := fset.Position(off).Line
	if end.Line == line || line == 0 {
		return nil
	}
	return fmt.Errorf("calltip: line mismatch end (%d) line (%d)", end.Line, line)
}

func (g *GoCode) convertOffset(af *ast.File, fset *token.FileSet, offset int) (token.Pos, error) {
	f := fset.File(af.Pos())
	if f == nil {
		return token.NoPos, errors.New("calltip: ast file not in fileset")
	}
	// Prevent Pos from panicking
	if offset > f.Size() {
		return token.NoPos, errors.New("calltip: illegal file offset")
	}
	return f.Pos(offset), nil
}

func (g *GoCode) findCaller(af *ast.File, fset *token.FileSet, pos token.Pos) (*ast.Ident, error) {
	v := calltipVisitor{
		pos:  pos,
		fset: fset,
	}
	ast.Walk(&v, af)
	if v.expr == nil || v.expr.Fun == nil {
		return nil, errors.New("calltip: nil CallExpr")
	}
	switch x := v.expr.Fun.(type) {
	case *ast.Ident:
		return x, nil
	case *ast.SelectorExpr:
		return x.Sel, nil
	}
	return nil, errors.New("calltip: invalid CallExpr.Fun")
}

// calltipVisitor satisfies the ast.Visitor interface.
type calltipVisitor struct {
	pos  token.Pos
	fset *token.FileSet
	expr *ast.CallExpr
}

func (v *calltipVisitor) Visit(node ast.Node) (w ast.Visitor) {
	if node == nil || v.expr != nil {
		return v
	}
	if x, ok := node.(*ast.CallExpr); ok {
		pos := x.Pos()
		end := x.End()
		if pos.IsValid() && end.IsValid() {
			if pos <= v.pos && v.pos <= end {
				v.expr = x
				return nil
			}
		}
	}
	return v
}

func (g *GoCode) bytePos() (int, error) {
	s := g.Src
	off := g.Pos
	if len(s) == 0 || len(s) <= off || off < 0 {
		return -1, errors.New("gocode: nil source")
	}
	i := 0
	var n int
	for n = 0; i < len(s) && n < off; n++ {
		if s[i] < utf8.RuneSelf {
			i++
		} else {
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
		}
	}
	if n == off && i < len(s) {
		return i, nil
	}
	return -1, fmt.Errorf("gocode: invalid offset: %d", g.Pos)
}

func init() {
	registry.Register("gocode_complete", func(b *Broker) Caller {
		return &GoCode{}
	})

	registry.Register("gocode_calltip", func(b *Broker) Caller {
		return &GoCode{calltip: true}
	})
}

// Initial implementation of calltips cache
/*

import (
	"fmt"
	"sync"

	"github.com/golang/groupcache/lru"
)

type Candidate struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Class string `json:"class"`
}

type FileEntry struct {
	Name       string            // file name
	Source     string            // file source
	Candidates map[int]Candidate // decl offset => Candidate
	mu         sync.RWMutex
}

func NewFileEntry(name, source string) *FileEntry {
	return &FileEntry{
		Name:       name,
		Source:     source,
		Candidates: make(map[int]Candidate),
	}
}

func (f *FileEntry) Get(pos int) (c Candidate, ok bool) {
	f.mu.RLock()
	if f.Candidates != nil {
		c, ok = f.Candidates[pos]
	}
	f.mu.RUnlock()
	return
}

func (f *FileEntry) Add(pos int, c Candidate) {
	f.mu.Lock()
	if f.Candidates == nil {
		f.Candidates = make(map[int]Candidate)
	}
	f.Candidates[pos] = c
	f.mu.Unlock()
}

type Cache struct {
	cache *lru.Cache
	mu    sync.Mutex
}

func New() *Cache {
	return &Cache{cache: lru.New(128)}
}

func (c *Cache) Get(name, source string, pos int) (Candidate, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.cache.Get(name)
	if !ok {
		return Candidate{}, false
	}
	e := v.(*FileEntry)
	if e.Source != source {
		return Candidate{}, false
	}
	return e.Get(pos)
}

func (c *Cache) Add(name, source string, pos int, can Candidate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var e *FileEntry
	if v, ok := c.cache.Get(name); ok {
		e = v.(*FileEntry)
		if e.Source != source {
			c.cache.Remove(name)
			e = nil
		}
	}
	if e == nil {
		e = NewFileEntry(name, source)
	}
	e.Add(pos, can)
	c.cache.Add(name, e)
}

func main() {
	c := New()
	c.Add("name", "source", 1, Candidate{Name: "c1", Type: "t1", Class: "c1"})
	fmt.Println(c.Get("name", "source", 1))
	c.Add("name", "source new", 1, Candidate{Name: "c1", Type: "t1", Class: "c1"})
	fmt.Println(c.Get("name", "source", 1))
	fmt.Println(c.Get("name", "source new", 1))
}
*/
