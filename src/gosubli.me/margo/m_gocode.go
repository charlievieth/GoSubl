package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/charlievieth/gocode"
	"gosubli.me/margo/lru"
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

var calltipCache = NewAstCache(50)

func init() {
	registry.Register("gocode_complete", func(b *Broker) Caller {
		return &GoCode{}
	})

	registry.Register("gocode_calltip", func(b *Broker) Caller {
		return &GoCode{calltip: true}
	})
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
	fset, af, err := calltipCache.ParseFile(filename, src)
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
	if node == nil {
		return v
	}
	if x, ok := node.(*ast.CallExpr); ok {
		pos := x.Pos()
		end := x.End()
		switch {
		case pos <= v.pos && v.pos <= end:
			v.expr = x
		case pos > v.pos:
			return nil
		}
	}
	return v
}

func (g *GoCode) bytePos() (int, error) {
	off := g.Pos
	s := g.Src
	if off < 0 || off > len(s) {
		return -1, errors.New("gocode: nil source")
	}
	for i := range s {
		if off <= 0 {
			return i, nil
		}
		off--
	}
	return -1, fmt.Errorf("gocode: invalid offset: %d", g.Pos)
}

type AstEntry struct {
	Name    string
	Body    []byte
	File    *ast.File
	FileSet *token.FileSet
}

type AstCache struct {
	cache *lru.Cache
}

func NewAstCache(maxEntries int) *AstCache {
	return &AstCache{
		cache: lru.New(maxEntries),
	}
}

func (c *AstCache) Add(filename string, ent *AstEntry) {
	c.cache.Add(filename, ent)
}

func (c *AstCache) Get(filename string) (*AstEntry, bool) {
	if v, ok := c.cache.Get(filename); ok {
		return v.(*AstEntry), true
	}
	return nil, false
}

func (c *AstCache) Remove(filename string) {
	c.cache.Remove(filename)
}

func (c *AstCache) ParseFile(filename string, src interface{}) (*token.FileSet, *ast.File, error) {
	text, err := readSource(filename, src)
	if err != nil {
		return nil, nil, err
	}
	if e, ok := c.Get(filename); ok {
		if bytes.Equal(e.Body, text) {
			return e.FileSet, e.File, nil
		}
		c.Remove(filename)
	}
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, filename, text, 0)
	if af == nil {
		return nil, nil, err
	}
	c.Add(filename, &AstEntry{
		Name:    filename,
		Body:    text,
		File:    af,
		FileSet: fset,
	})
	return fset, af, nil
}

func readSource(filename string, src interface{}) ([]byte, error) {
	if src != nil {
		switch s := src.(type) {
		case string:
			return []byte(s), nil
		case []byte:
			return s, nil
		case *bytes.Buffer:
			// is io.Reader, but src is already available in []byte form
			if s != nil {
				return s.Bytes(), nil
			}
		case io.Reader:
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, s); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		}
		return nil, errors.New("invalid source")
	}
	return ioutil.ReadFile(filename)
}
