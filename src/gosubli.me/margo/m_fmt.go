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
	"time"

	"github.com/charlievieth/imports"
	"github.com/charlievieth/imports/gocommand"
	"github.com/golang/groupcache/lru"
	"golang.org/x/sync/singleflight"
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
	Src      string `json:"src"`
	NoChange bool   `json:"no_change"`
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

func (f *FormatRequest) callTimeout() (*FormatResponse, error) {
	type Response struct {
		Out []byte
		Err error
	}

	src := []byte(f.Src)

	// TODO: stop if there is an error here? We also might want
	// to use format.Source since it handles code fragments.
	hasCGO := false
	fset := token.NewFileSet()
	af, parseErr := parser.ParseFile(fset, f.Filename, src, parser.ParseComments)
	if parseErr == nil {
		for _, imp := range af.Imports {
			if imp.Path != nil && imp.Path.Value == `"C"` {
				hasCGO = true
				break
			}
		}
	}

	call := func(ch chan<- *Response, fn func() ([]byte, error)) {
		go func() {
			var out []byte
			var err error
			if e := recover(); e != nil {
				err = f.recoverErr(e)
				out = src
				select {
				case ch <- &Response{out, err}:
				default:
				}
			}
			out, err = fn()
			ch <- &Response{out, err}
		}()
	}

	fixImportsCh := make(chan *Response, 1)
	formatOnlyCh := make(chan *Response, 1)

	call(fixImportsCh, func() ([]byte, error) {
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
			return imports.Process(f.Filename, src, &opts)
		}
		return nil, errors.New("fmt: cgo not supported")
	})
	call(formatOnlyCh, func() ([]byte, error) {
		if af != nil {
			return f.formatFile(fset, af)
		}
		return nil, parseErr
	})

	start := time.Now()
	timeout := time.NewTimer(time.Millisecond * 500)
	defer timeout.Stop()

	timedOut := false
	var fixImportsRes *Response
	var formatOnlyRes *Response
Loop:
	for {
		select {
		case fixImportsRes = <-fixImportsCh:
			if fixImportsRes.Err == nil || formatOnlyRes != nil {
				break Loop
			}
		case formatOnlyRes = <-formatOnlyCh:
			if formatOnlyRes.Err != nil || fixImportsRes != nil {
				break Loop
			}
		case <-timeout.C:
			if timedOut || formatOnlyRes != nil || fixImportsRes != nil {
				break Loop
			}
			timedOut = true
			// Wait a little longer for a response
			timeout.Reset(time.Millisecond * 200)
		}
	}
	res := fixImportsRes
	if res == nil || (res.Err != nil && formatOnlyRes != nil && formatOnlyRes.Err == nil) {
		res = formatOnlyRes
	}
	if res != nil {
		if res.Err != nil {
			return &FormatResponse{NoChange: true}, res.Err
		}
		if bytes.Equal(src, res.Out) {
			return &FormatResponse{NoChange: true}, nil
		}
		return &FormatResponse{Src: string(res.Out)}, nil
	}
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
	formatRequestGroup   singleflight.Group
	formatRequestCache   = lru.New(128)
	formatRequestCacheMu sync.Mutex
)

type FormatCacheEntry struct {
	Res    *FormatResponse
	ErrStr string
}

func (f *FormatRequest) cacheGet() (*FormatResponse, string, bool) {
	formatRequestCacheMu.Lock()
	v, ok := formatRequestCache.Get(f.Src)
	formatRequestCacheMu.Unlock()
	if ok {
		ent := v.(*FormatCacheEntry)
		return ent.Res, ent.ErrStr, true
	}
	return nil, "", false
}

func (f *FormatRequest) cachePut(res *FormatResponse, err error) (*FormatResponse, error) {
	ent := &FormatCacheEntry{
		Res:    res,
		ErrStr: errStr(err),
	}
	formatRequestCacheMu.Lock()
	formatRequestCache.Add(f.Src, ent)
	formatRequestCacheMu.Unlock()
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
