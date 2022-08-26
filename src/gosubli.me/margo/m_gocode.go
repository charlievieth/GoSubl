package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charlievieth/gocode"
	"github.com/mdempsky/gocode/pkg/cache"
	"github.com/mdempsky/gocode/pkg/suggest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gosubli.me/margo/internal/lru"
)

const GocodeDebugLogger = false

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
	Candidates []suggest.Candidate
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

var (
	suggestConfig        *suggest.Config
	initGocodeConfigOnce sync.Once
	initGocodeConfigErr  error
)

func noopLogger(string, ...interface{}) {}

func initGocodeConfig() (*suggest.Config, error) {
	initGocodeConfigOnce.Do(func() {
		if !GocodeDebugLogger {
			suggestConfig = &suggest.Config{
				Builtin:            true,
				IgnoreCase:         true,
				UnimportedPackages: false,
				Logf:               noopLogger,
			}
		} else {
			ll, err := zap.NewStdLogAt(logger.Named("gocode"), zap.InfoLevel)
			if err != nil {
				initGocodeConfigErr = err
				return
			}
			suggestConfig = &suggest.Config{
				Builtin:            true,
				IgnoreCase:         true,
				UnimportedPackages: false,
				Logf:               ll.Printf,
			}
		}
	})
	return suggestConfig, initGocodeConfigErr
}

func (*GoCode) newStdLog(log *zap.Logger, lvl zapcore.Level) *log.Logger {
	std, err := zap.NewStdLogAt(log, lvl)
	if err != nil {
		log.Error("gocode: error creating std logger", zap.Error(err))
		lvl = zap.InfoLevel
		std, _ = zap.NewStdLogAt(log, lvl)
	}
	return std
}

func (g *GoCode) doCall() (res []suggest.Candidate, err error) {
	start := time.Now()
	cursor, err := g.bytePos()
	if err != nil {
		return nil, err
	}

	log := logger.Named("gocode").WithOptions(
		zap.AddStacktrace(zap.ErrorLevel),
	).With(zap.String("filename", g.shortFilename()), zap.Int("cursor", cursor))

	defer func() {
		if e := recover(); e != nil {
			log.Error("suggest panicked",
				zap.String("panic", fmt.Sprintf("%v", e)),
				zap.String("context", extractCursorLine(g.Src, cursor)),
			)
			var perr error
			if ee, ok := e.(error); ok {
				perr = fmt.Errorf("panic: gocode: %w", ee)
			} else {
				perr = fmt.Errorf("panic: gocode: %v", e)
			}
			if err == nil {
				err = perr
			} else {
				err = fmt.Errorf("%w: %v", err, perr)
			}

		}
	}()
	cfg := suggest.Config{
		Builtin:            true,
		IgnoreCase:         true,
		UnimportedPackages: false,
		Logf:               noopLogger,
	}
	if GocodeDebugLogger {
		cfg.Logf = g.newStdLog(log.Named("suggest"), zap.InfoLevel).Printf
	}

	// TODO: record completion time as a histogram and print
	// the relevent percentiles every N completion requests

	ctxt := build.Default
	if GocodeDebugLogger {
		cfg.Importer = cache.NewIImporter(&ctxt, g.newStdLog(log.Named("cache"), zap.InfoLevel).Printf)
	} else {
		cfg.Importer = cache.NewIImporter(&ctxt, noopLogger)
	}

	candidates, d := cfg.Suggest(g.Fn, []byte(g.Src), cursor)
	_ = d
	if len(candidates) > 0 {
		log.Info("completion time",
			zap.Duration("duration", time.Since(start)),
			zap.Int("candidates", len(candidates)),
		)
	} else {
		log.Info("no candidates",
			zap.Duration("duration", time.Since(start)),
			zap.String("context", extractCursorLine(g.Fn, cursor)),
		)
	}
	return candidates, nil
}

func (g *GoCode) doCalltips() ([]suggest.Candidate, error) {
	cursor, err := g.bytePos()
	if err != nil {
		return nil, err
	}
	// TODO: this is a mess - consider removing useLastCalltip since
	// we guard against that in the Python code.
	path := g.filepath()
	if res, ok := useLastCalltip(path, g.Src, cursor); ok {
		return res.Candidates, nil
	}
	cl, err := g.calltips(path, []byte(g.Src), cursor)
	setLastCalltip(path, g.Src, cursor, GoCodeResponse{Candidates: cl})
	return cl, err
}

func (g *GoCode) Call() (response interface{}, errStr string) {
	var candidates []suggest.Candidate
	var err error
	if g.calltip {
		candidates, err = g.doCalltips()
	} else {
		candidates, err = g.doCall()
	}
	if err != nil {
		return GoCodeResponse{NoGocodeCandidates}, err.Error()
	}
	// TODO: use a pointer
	return GoCodeResponse{Candidates: candidates}, ""
}

// WARN: dev only
func (*GoCode) candidatesEqual(a1 []suggest.Candidate, a2 []gocode.Candidate) bool {
	if len(a1) != len(a2) {
		return false
	}
	for i := 0; i < len(a1) && i < len(a2); i++ {
		c1 := a1[i]
		c2 := a2[i]
		if c1.Name != c2.Name || c1.Class != c2.Class {
			return false
		}
		if c2.Type != "" && c1.Type != c2.Type {
			return false
		}
	}
	return true
}

type candidatesByClassAndName []gocode.Candidate

func (s candidatesByClassAndName) Len() int      { return len(s) }
func (s candidatesByClassAndName) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (s candidatesByClassAndName) Less(i, j int) bool {
	if s[i].Class != s[j].Class {
		return s[i].Class < s[j].Class
	}
	return s[i].Name < s[j].Name
}

type GoCodeResponseDelta struct {
	Extra    []gocode.Candidate `json:"extra,omitempty"`
	Missing  []gocode.Candidate `json:"missing,omitempty"`
	NotEqual []gocode.Candidate `json:"notequal,omitempty"`
}

func (*GoCode) candidatesDelta(a1, a2 []gocode.Candidate) GoCodeResponseDelta {
	// mdempsky gocode has more complete type info
	const removeTypeInfo = true
	if removeTypeInfo {
		for i := range a1 {
			a1[i].Type = ""
		}
		for i := range a2 {
			a2[i].Type = ""
		}
	}

	m1 := make(map[gocode.Candidate]gocode.Candidate, len(a1))
	m2 := make(map[gocode.Candidate]gocode.Candidate, len(a2))
	for _, c := range a1 {
		m1[c] = c
	}
	for _, c := range a2 {
		m2[c] = c
	}
	var delta GoCodeResponseDelta
	for c1 := range m1 {
		c2, ok := m2[c1]
		if !ok {
			delta.Extra = append(delta.Extra, c1)
		} else if c1 != c2 {
			delta.NotEqual = append(delta.NotEqual, c1)
		}
	}
	for c2 := range m2 {
		if _, ok := m1[c2]; !ok {
			delta.Missing = append(delta.Missing, c2)
		}
	}
	sort.Sort(candidatesByClassAndName(delta.Extra))
	sort.Sort(candidatesByClassAndName(delta.Missing))
	sort.Sort(candidatesByClassAndName(delta.NotEqual))
	return delta
}

func extractCursorLine(src string, cursor int) (line string) {
	if cursor == len(src) {
		if i := strings.LastIndexByte(src, '\n'); i >= 0 {
			src = src[i+1:]
		}
		return src + "$"
	}
	if cursor < 0 || cursor >= len(src) {
		return
	}
	if i := strings.LastIndexByte(src[:cursor], '\n'); i > 0 {
		i++
		src = src[i:]
		cursor -= i
	}
	if i := strings.IndexByte(src, '\n'); i >= 0 {
		src = src[:i]
	}
	return src[:cursor] + "$" + src[cursor:]
}

var defaultSrcDirs = build.Default.SrcDirs()

func init() {
	for i, s := range defaultSrcDirs {
		defaultSrcDirs[i] = filepath.Clean(s)
	}
}

func (g *GoCode) shortFilename() string {
	for _, src := range defaultSrcDirs {
		if strings.HasPrefix(g.Fn, src) {
			if filepath.Separator == '/' {
				return strings.TrimLeft(strings.TrimPrefix(g.Fn, src), "/")
			}
			return strings.TrimLeft(strings.TrimPrefix(g.Fn, src), "/"+string(filepath.Separator))
		}
	}
	if strings.Contains(filepath.ToSlash(g.Fn), "/src/") {
		a := strings.Split(g.Fn, string(filepath.Separator))
		for i := len(a) - 1; i >= 0; i-- {
			if a[i] == "src" {
				return filepath.Join(a[i+1:]...)
			}
		}
	}
	return g.Fn
}

func (g *GoCode) Call_OLD() (interface{}, string) {
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

	// WARN: compare completion results

	type CompRes struct {
		Candidates []gocode.Candidate
		Duration   time.Duration
	}
	ch := make(chan *CompRes, 1)
	defer close(ch)

	go func() {
		log := logger.Named("gocode.test").WithOptions(
			zap.AddStacktrace(zap.ErrorLevel),
		).With(zap.String("filename", g.shortFilename()), zap.Int("cursor", g.Pos))

		start := time.Now()
		candidates, err := g.doCall()
		if err != nil {
			log.Error("gocode: response error", zap.Error(err),
				zap.String("line", extractCursorLine(g.Src, g.Pos)))
			return
		}
		dur := time.Since(start)

		to := time.NewTimer(time.Second)
		defer to.Stop()
		select {
		case res := <-ch:
			if res == nil {
				return
			}
			log.Info("gocode: complete time", zap.Duration("duration", dur),
				zap.Duration("duration_base", res.Duration))

			want := res.Candidates
			if !g.candidatesEqual(candidates, want) {
				got := make([]gocode.Candidate, len(candidates))
				for i, c := range candidates {
					got[i] = gocode.Candidate{
						Name:  c.Name,
						Type:  c.Type,
						Class: c.Class,
					}
				}
				log.Warn("gocode: response mismatch",
					zap.Int("got_len", len(got)), zap.Int("want_len", len(want)),
					zap.Reflect("delta", g.candidatesDelta(got, want)),
				)
			}
		case <-to.C:
			log.Warn("timed out waiting for GoCode candidates")
		}
	}()

	start := time.Now()
	candidates := g.complete(path, []byte(g.Src), off)
	dur := time.Since(start)
	select {
	case ch <- &CompRes{Candidates: candidates, Duration: dur}:
	default:
	}

	// WARN
	cl := make([]suggest.Candidate, len(candidates))
	for i, c := range candidates {
		cl[i] = suggest.Candidate{
			Name:  c.Name,
			Type:  c.Type,
			Class: c.Class,
		}
	}
	return g.response(cl, nil, true)
}

var NoGocodeCandidates = []suggest.Candidate{}

func (g *GoCode) response(res []suggest.Candidate, err error, install bool) (GoCodeResponse, string) {
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
func (g *GoCode) calltips(filename string, src []byte, cursor int) ([]suggest.Candidate, error) {
	// WARN WARN WARN
	// for i := cursor; i > 0; i-- {
	// 	c := src[cursor]
	// 	if c == ' ' || c == '.' {
	// 		cursor = i + 1
	// 		break
	// 	}
	// }
	//
	// for ; cursor > 0; cursor-- {
	// 	c := src[cursor]
	// 	if c == ' ' || c == '.' {
	// 		break
	// 	}
	// }
	//

	fset, af, err := calltipCache.ParseFile(filename, src)
	if af == nil {
		return nil, err
	}
	pos, err := g.convertOffset(af, fset, cursor)
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
	cfg := suggest.Config{
		Builtin:            true,
		IgnoreCase:         true,
		UnimportedPackages: false,
		Logf:               noopLogger,
	}
	cl, _ := cfg.Suggest(g.Fn, []byte(g.Src), cursor)

	// // WARN WARN WARN
	// {
	// 	typs := make([]string, len(cl))
	// 	for i, c := range cl {
	// 		typs[i] = c.String()
	// 	}
	// 	// line := extractCursorLine(string(src), cursor)
	// 	logger.Named("gocode_calltips").With(
	// 		zap.String("filename", g.shortFilename()), zap.Int("cursor", cursor),
	// 		zap.String("ident", fmt.Sprintf("%s", id.Name)),
	// 		// zap.String("line", line),
	// 	).Warn("calltip candidates", zap.Strings("candidates", typs))
	// }

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
