package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charlievieth/imports"
	"github.com/charlievieth/imports/gocommand"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
	"gosubli.me/margo/internal/lru"
)

const DefaultFormatTimeout = time.Second

// TODO: add configurable Timeout for goimports
type FormatRequest struct {
	Filename  string   `json:"filename"`
	Src       string   `json:"source"`
	Tabwidth  int      `json:"tab_width"`
	TabIndent bool     `json:"tab_indent"`
	Timeout   *float64 `json:"timeout"`
}

func (r *FormatRequest) GetTimeout() time.Duration {
	d := DefaultFormatTimeout
	if r.Timeout != nil {
		dd := time.Duration(float64(time.Second) * *r.Timeout)
		if dd > 0 {
			d = dd
		}
	}
	if d > time.Millisecond*100 {
		d -= time.Millisecond * 100 // Give ourselves 100ms to respond
	}
	return d
}

type FormatResponse struct {
	Src       string `json:"src"`
	NoChange  bool   `json:"no_change"`
	dontCache bool
}

func (f *FormatRequest) formatFile(fset *token.FileSet, af *ast.File) ([]byte, error) {
	// Keep these in sync with cmd/gofmt/gofmt.go.
	const (
		// printerNormalizeNumbers means to canonicalize number literal prefixes
		// and exponents while printing. See https://golang.org/doc/go1.13#gofmt.
		//
		// This value is defined in go/printer specifically for go/format and cmd/gofmt.
		printerNormalizeNumbers = 1 << 30

		tabWidth    = 8
		printerMode = printer.UseSpaces | printer.TabIndent | printerNormalizeNumbers
	)
	config := printer.Config{Mode: printerMode, Tabwidth: tabWidth}

	imports.Simplify(af)
	if f.hasUnsortedImports(af) {
		ast.SortImports(fset, af)
	}

	var buf bytes.Buffer
	buf.Grow(len(f.Src) + 512) // 512 is totally arbitrary, but seems about right
	if err := config.Fprint(&buf, fset, af); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (*FormatRequest) hasUnsortedImports(file *ast.File) bool {
	for _, d := range file.Decls {
		d, ok := d.(*ast.GenDecl)
		if !ok || d.Tok != token.IMPORT {
			// Not an import declaration, so we're done.
			// Imports are always first.
			return false
		}
		if d.Lparen.IsValid() {
			// For now assume all grouped imports are unsorted.
			// TODO(gri) Should check if they are sorted already.
			return true
		}
		// Ungrouped imports are sorted by default.
	}
	return false
}

var ErrCgoNotSupported = errors.New("fmt: cgo not supported")

func (f *FormatRequest) callTimeout(key string) (*FormatResponse, error) {
	type Response struct {
		Out     []byte
		Err     error
		Imports bool
	}

	src := []byte(f.Src)

	// TODO: stop if there is an error here? We also might want
	// to use format.Source since it handles code fragments.
	hasCGO := false
	_ = hasCGO // WARN WARN WARN

	fset := token.NewFileSet()
	af, parseErr := parser.ParseFile(fset, f.Filename, src, parser.ParseComments)
	if af != nil {
		for _, imp := range af.Imports {
			if imp.Path != nil && imp.Path.Value == `"C"` {
				hasCGO = true
				break
			}
		}
	}

	resCh := make(chan *Response, 2)
	done := make(chan struct{})
	defer close(done)

	call := func(fn func() ([]byte, bool, error)) {
		go func() {
			var (
				out       []byte
				err       error
				isImports bool
			)
			if e := recover(); e != nil {
				err = f.recoverErr(e)
				out = src
				select {
				case resCh <- &Response{out, err, isImports}:
				default:
				}
			}
			out, isImports, err = fn()
			resCh <- &Response{out, err, isImports}

			// Cache responses even if the response timeout has expired
			// this makes subsequent responses faster.
			if isImports {
				select {
				case <-done:
					// Timed out before we could send our response
					res := &FormatResponse{
						Src:      string(out),
						NoChange: bytes.Equal(src, out),
					}
					f.cachePut(key, res, err)
				default:
					return
				}
			}
		}()
	}

	call(func() ([]byte, bool, error) {
		// goimports is very slow when "C" is imported
		// TODO: allow configuring env var overrides
		opts := imports.Options{
			TabWidth:    8,
			TabIndent:   true,
			Comments:    true,
			Fragment:    true,
			SimplifyAST: true,
			Env: &imports.ProcessEnv{
				GocmdRunner: &gocommand.Runner{},
				WorkingDir:  filepath.Dir(f.Filename),
				// TODO: set the Env field
			},
		}
		b, err := imports.Process(f.Filename, src, &opts)
		return b, true, err
	})

	call(func() ([]byte, bool, error) {
		// TODO: handle code fragments
		if af != nil && parseErr == nil {
			b, err := f.formatFile(fset, af)
			return b, false, err
		}
		return nil, false, parseErr
	})

	timeout := f.GetTimeout()
	start := time.Now()
	to := time.NewTimer(timeout)
	defer to.Stop()

	timedOut := false
	respones := make([]*Response, 0, 2)
Loop:
	for i := 0; i < 2; i++ {
		select {
		case r := <-resCh:
			respones = append(respones, r)
			if r.Imports && r.Err == nil {
				break Loop
			}
		case <-to.C:
			if timedOut {
				break Loop
			}
			timedOut = true
			// Wait a little longer for a response
			to.Reset(time.Millisecond * 200)
		}
	}

	if len(respones) == 0 {
		if timedOut {
			return &FormatResponse{}, fmt.Errorf("fmt: timed out after: %s", time.Since(start))
		}
		return &FormatResponse{}, errors.New("failed to format file: nil response")
	}

	var res *Response
	for _, r := range respones {
		if res == nil {
			res = r // Set to the first response
		}
		if r.Imports && len(r.Out) != 0 {
			res = r // Change to the Imports response if we have it
		}
	}
	if res == nil {
		// This should never happen
		return &FormatResponse{}, errors.New("internal error: nil response")
	}
	if res.Err != nil {
		// See if we have a response without an error
		for _, r := range respones {
			if r.Err == nil && r.Out != nil {
				res = r
				break
			}
		}
	}
	if res.Err != nil {
		return &FormatResponse{NoChange: true}, res.Err
	}

	dontCache := false
	if !res.Imports {
		if v, ok := compLintCache.Get(key); ok {
			noBuildErrors, _ := v.(bool)
			dontCache = !noBuildErrors
		}
	}
	if bytes.Equal(src, res.Out) {
		return &FormatResponse{NoChange: true, dontCache: dontCache}, nil
	}
	return &FormatResponse{Src: string(res.Out), dontCache: dontCache}, nil

	if timedOut {
		return &FormatResponse{}, fmt.Errorf("fmt: timed out after: %s", time.Since(start))
	}
	return &FormatResponse{}, errors.New("failed to format file: nil response")
}

func (*FormatRequest) recoverErr(err interface{}) error {
	if err == nil {
		return errors.New("panic: nil error!")
	}
	switch v := err.(type) {
	case string:
		return errors.New(v)
	case error:
		return v
	case fmt.Stringer:
		return errors.New(v.String())
	default:
		return fmt.Errorf("%#v", err)
	}
}

var (
	formatRequestGroup     singleflight.Group
	formatRequestCache     = lru.New(128)
	formatRequestCacheInit sync.Once
)

func cleanupFormatRequestCache() {
	const maxAge = time.Minute * 2
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for range tick.C {
		formatRequestCache.RemoveFunc(func(_ lru.Key, value interface{}) bool {
			if e, ok := value.(*FormatCacheEntry); ok {
				if mod := e.ModTime(); !mod.IsZero() && time.Since(mod) >= maxAge {
					return true
				}
			}
			return false
		})
	}
}

type FormatCacheEntry struct {
	Res    *FormatResponse
	tval   atomic.Value // *time.Time
	ErrStr string
}

func (e *FormatCacheEntry) UpdateModTime() {
	now := time.Now()
	e.tval.Store(&now)
}

func (e *FormatCacheEntry) ModTime() time.Time {
	if e != nil {
		if v := e.tval.Load(); v != nil {
			return *v.(*time.Time)
		}
	}
	return time.Time{}
}

func (f *FormatRequest) cacheGet(key string) (*FormatResponse, string, bool) {
	if v, ok := formatRequestCache.Get(key); ok {
		ent := v.(*FormatCacheEntry)
		ent.UpdateModTime()
		return ent.Res, ent.ErrStr, true
	}
	return nil, "", false
}

func (f *FormatRequest) cachePut(key string, res *FormatResponse, err error) (*FormatResponse, error) {
	if res.dontCache {
		return res, err
	}
	formatRequestCacheInit.Do(func() { go cleanupFormatRequestCache() })
	e := &FormatCacheEntry{
		Res:    res,
		ErrStr: errStr(err),
	}
	e.UpdateModTime()
	formatRequestCache.Add(key, e)
	return res, err
}

func (f *FormatRequest) Call() (interface{}, string) {
	log := logger.With(zap.String("filename", filepath.Base(f.Filename)))

	key := fileCacheKey(f.Filename, f.Src)
	if res, errStr, ok := f.cacheGet(key); ok {
		log.Debug("format: cache hit")
		return res, errStr
	}

	v, err, _ := formatRequestGroup.Do(key, func() (v interface{}, err error) {
		start := time.Now()
		res, err := f.callTimeout(key)

		log.Debug("format: format response", zap.Bool("no_change", res.NoChange),
			zap.Bool("dont_cache", res.dontCache), zap.Error(err),
			zap.Duration("time", time.Since(start)))

		return f.cachePut(key, res, err)
	})
	res, ok := v.(*FormatResponse)
	if !ok && err == nil {
		err = fmt.Errorf("m_fmt: invalid response type: %T", v)
	}
	// make sure this is never nil
	if res == nil {
		res = &FormatResponse{NoChange: true}
	}
	if err != nil {
		return res, err.Error()
	}
	return res, ""
}

func init() {
	registry.Register("fmt", func(b *Broker) Caller {
		return &FormatRequest{
			TabIndent: true,
			Tabwidth:  8,
		}
	})
}
