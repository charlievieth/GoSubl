package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"hash/maphash"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charlievieth/imports"
	"github.com/charlievieth/imports/gocommand"
	"golang.org/x/sync/singleflight"
	"gosubli.me/margo/internal/lru"
)

// TODO: add configurable Timeout for goimports
type FormatRequest struct {
	Filename   string `json:"Fn"`
	Src        string `json:"Src"`
	Tabwidth   int    `json:"TabWidth"`
	TabIndent  bool   `json:"TabIndent"`
	FormatOnly bool   `json:"FormatOnly"` // Disable the insertion and deletion of imports
	FixCGO     bool   `json:"FixCGO"`     // Enable goimports for cgo files (slow)
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

func (f *FormatRequest) callTimeout() (*FormatResponse, error) {
	type Response struct {
		Out     []byte
		Err     error
		Imports bool
	}

	src := []byte(f.Src)

	// TODO: stop if there is an error here? We also might want
	// to use format.Source since it handles code fragments.
	hasCGO := false
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

	// Fast path for cgo files when FixCGO is false
	if hasCGO && !f.FixCGO {
		b, err := f.formatFile(fset, af)
		if err != nil {
			return &FormatResponse{NoChange: true}, err
		}
		if bytes.Equal(src, b) {
			return &FormatResponse{NoChange: true}, nil
		}
		return &FormatResponse{Src: string(b), dontCache: false}, nil
	}

	call := func(ch chan<- *Response, fn func() ([]byte, bool, error)) {
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
				case ch <- &Response{out, err, isImports}:
				default:
				}
			}
			out, isImports, err = fn()
			ch <- &Response{out, err, isImports}
		}()
	}

	resCh := make(chan *Response, 2)
	call(resCh, func() ([]byte, bool, error) {
		// goimports is very slow when "C" is imported
		if !hasCGO || f.FixCGO {
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
		}
		return nil, true, ErrCgoNotSupported
	})
	call(resCh, func() ([]byte, bool, error) {
		// TODO: handle code fragments
		if af != nil && parseErr == nil {
			b, err := f.formatFile(fset, af)
			return b, false, err
		}
		return nil, false, parseErr
	})

	start := time.Now()
	timeout := time.NewTimer(time.Millisecond * 500)
	defer timeout.Stop()

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
		case <-timeout.C:
			if timedOut {
				break Loop
			}
			timedOut = true
			// Wait a little longer for a response
			timeout.Reset(time.Millisecond * 200)
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
	if bytes.Equal(src, res.Out) {
		return &FormatResponse{NoChange: true}, nil
	}
	return &FormatResponse{Src: string(res.Out), dontCache: false}, nil

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
	formatRequestCacheSeed = maphash.MakeSeed()
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

func (f *FormatRequest) cacheKey() string {
	if len(f.Src) <= 512*1024 {
		return f.Src
	}
	// Use a hash for larger files so that we don't
	// store the full source in memory
	var h maphash.Hash
	h.SetSeed(formatRequestCacheSeed)
	h.WriteString(f.Src)
	return fmt.Sprintf("%s|%d|%d", filepath.Base(f.Filename), len(f.Src), h.Sum64())
}

func (f *FormatRequest) cacheGet() (*FormatResponse, string, bool) {
	if v, ok := formatRequestCache.Get(f.cacheKey()); ok {
		ent := v.(*FormatCacheEntry)
		ent.UpdateModTime()
		return ent.Res, ent.ErrStr, true
	}
	return nil, "", false
}

func (f *FormatRequest) cachePut(res *FormatResponse, err error) (*FormatResponse, error) {
	if res.dontCache {
		return res, err
	}
	formatRequestCacheInit.Do(func() { go cleanupFormatRequestCache() })
	e := &FormatCacheEntry{
		Res:    res,
		ErrStr: errStr(err),
	}
	e.UpdateModTime()
	formatRequestCache.Add(f.cacheKey(), e)
	return res, err
}

func (f *FormatRequest) Call() (interface{}, string) {
	if res, errStr, ok := f.cacheGet(); ok {
		return res, errStr
	}

	v, err, _ := formatRequestGroup.Do(f.Src, func() (v interface{}, err error) {
		return f.cachePut(f.callTimeout())
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
