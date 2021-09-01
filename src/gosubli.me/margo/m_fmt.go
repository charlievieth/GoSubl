package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charlievieth/imports"
	"github.com/charlievieth/imports/gocommand"
	"github.com/golang/groupcache/lru"
	"golang.org/x/sync/singleflight"
)

type FormatRequest struct {
	Filename   string `json:"Fn"`
	Src        string `json:"Src"`
	Tabwidth   int    `json:"TabWidth"`
	TabIndent  bool   `json:"TabIndent"`
	FormatOnly bool   `json:"FormatOnly"` // Disable the insertion and deletion of imports
}

type FormatResponse struct {
	Src      string `json:"src"`
	NoChange bool   `json:"no_change"`
}

func (f *FormatRequest) formatOnly() (*FormatResponse, error) {
	src := []byte(f.Src)
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, f.Filename, src, parser.ParseComments)
	if err != nil && af == nil {
		return nil, err
	}
	imports.Simplify(af)

	cfg := printer.Config{
		Mode:     printer.UseSpaces | printer.TabIndent,
		Tabwidth: f.Tabwidth,
	}
	var buf bytes.Buffer
	buf.Grow(len(src) + 512) // 512 is totally arbitrary, but seems about right

	err = cfg.Fprint(&buf, fset, af)
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func (f *FormatRequest) callTimeout() (*FormatResponse, string) {
	type Response struct {
		Out []byte
		Err error
	}
	opts := imports.Options{
		TabWidth:    f.Tabwidth,
		TabIndent:   f.TabIndent,
		Comments:    true,
		Fragment:    true,
		SimplifyAST: true,
		Env: &imports.ProcessEnv{
			GocmdRunner: &gocommand.Runner{},
		},
	}
	start := time.Now()
	src := []byte(f.Src)
	ch := make(chan Response, 2)
	go func() {
		out, err := imports.Process(f.Filename, src, &opts)
		ch <- Response{out, err}
	}()

	var done int64
	timer := time.NewTimer(time.Millisecond * 500)
	defer func() {
		atomic.StoreInt64(&done, 1)
		timer.Stop()
	}()

	for i := 0; i < 2; i++ {
		select {
		case res := <-ch:
			if res.Out == nil && res.Err != nil {
				return &FormatResponse{NoChange: true}, res.Err.Error()
			}
			if bytes.Equal(src, res.Out) {
				return &FormatResponse{NoChange: true}, ""
			}
			return &FormatResponse{Src: string(res.Out)}, ""
		case <-timer.C:
			go func() {
				fset := token.NewFileSet()
				af, err := parser.ParseFile(fset, f.Filename, src, parser.ParseComments)
				if err != nil && af == nil {
					ch <- Response{Err: err}
					return
				}
				if atomic.LoadInt64(&done) != 0 {
					return // bail
				}
				cfg := printer.Config{
					Mode:     printer.UseSpaces | printer.TabIndent,
					Tabwidth: f.Tabwidth,
				}
				var buf bytes.Buffer
				cfg.Fprint(&buf, fset, af)
				ch <- Response{buf.Bytes(), err}
			}()
		}
	}
	return &FormatResponse{NoChange: true}, "Timeout formatting file: " +
		time.Since(start).String()
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

// var importsOptions = &imports.Options{
// 	TabWidth:    8,
// 	TabIndent:   true,
// 	Comments:    true,
// 	Fragment:    true,
// 	SimplifyAST: true,
// 	Env: &imports.ProcessEnv{
// 		GocmdRunner: &gocommand.Runner{},
// 	},
// }

func (f *FormatRequest) doCall() (res *FormatResponse, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = f.recoverErr(e)
			res = &FormatResponse{NoChange: true}
		}
	}()

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
		},
	}
	src := []byte(f.Src)

	var out []byte
	out, err = imports.Process(f.Filename, src, &opts)
	if out == nil && err != nil {
		return &FormatResponse{NoChange: true}, err
	}

	if bytes.Equal(src, out) {
		return &FormatResponse{NoChange: true}, nil
	}
	return &FormatResponse{Src: string(out)}, nil
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
		return f.cachePut(f.doCall())
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
