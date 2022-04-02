package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/charlievieth/buildutil"
	"github.com/charlievieth/xtools/span"
)

func init() {
	registry.Register("doc", func(_ *Broker) Caller {
		return &FindRequest{
			Env: map[string]string{},
		}
	})
}

type FindRequest struct {
	Fn        string            `json:"Fn"`
	Src       string            `json:"Src"`
	Env       map[string]string `json:"Env"`
	Offset    int               `json:"Offset"`
	TabIndent bool              `json:"TabIndent"`
	TabWidth  int               `json:"TabWidth"`
}

type FindResponse struct {
	// CEV: we removed support for doc hints since they're broken
	// Src  string `json:"src"`

	// TODO: remove unused fields Pkg, Name, and Kind ???
	Pkg     string `json:"pkg"`  // Ignored for now
	Name    string `json:"name"` // Ignored for now
	Kind    string `json:"kind"` // Ignored for now
	Fn      string `json:"fn"`
	Row     int    `json:"row"`
	Col     int    `json:"col"`
	Program string `json:"program"`
}

func ReadersEqual(r1, r2 io.Reader) bool {
	b1 := make([]byte, 32*1024)
	b2 := make([]byte, 32*1024)
	for {
		n1, e1 := io.ReadFull(r1, b1)
		n2, e2 := io.ReadFull(r2, b2)
		if n1 != n2 || (n1 > 0 && !bytes.Equal(b1[:n1], b2[:n2])) {
			return false
		}
		if e1 != nil || e2 != nil {
			return e1 == e2 && (e1 == io.EOF || e1 == io.ErrUnexpectedEOF)
		}
	}
}

func (f *FindRequest) FileModified() bool {
	if f.Src == "" {
		return false
	}

	fp, err := os.Open(f.Fn)
	if err != nil {
		return true
	}
	defer fp.Close()

	fi, err := fp.Stat()
	if err != nil {
		return true
	}
	if fi.Size() != int64(len(f.Src)) {
		return true
	}
	return !ReadersEqual(fp, strings.NewReader(f.Src))
}

var ErrFileModified = errors.New("gopls not supported for modified files")

type GoplsDefinitionResponse struct {
	Span        span.Span `json:"span"`        // span of the definition
	Description string    `json:"description"` // description of the denoted object
}

func (f *FindRequest) Gopls(ctx context.Context, buildCtxt *build.Context) (*FindResponse, error) {
	if f.FileModified() {
		return nil, ErrFileModified
	}

	goplsExe, err := exec.LookPath("gopls")
	if err != nil {
		return nil, errors.New("please install gopls: `go get -u golang.org/x/tools/gopls`")
	}

	cmd := buildutil.GoCommandContext(
		ctx, buildCtxt,
		goplsExe, "definition", "-json",
		fmt.Sprintf("%s:#%d", f.Fn, f.Offset),
	)
	cmd.Dir = filepath.Dir(f.Fn)

	output, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg := string(bytes.TrimSpace(ee.Stderr))
			if !strings.HasPrefix(msg, "gopls: ") {
				msg = "gopls: " + msg
			}
			return nil, errors.New(msg)
		}
		return nil, fmt.Errorf("gopls: %w", err)
	}

	var def GoplsDefinitionResponse
	if err := json.Unmarshal(output, &def); err != nil {
		return nil, err
	}

	if !def.Span.URI().IsFile() {
		return nil, fmt.Errorf("span is not a file: %s", def.Span.URI())
	}
	row := def.Span.Start().Line()
	if row > 0 {
		row--
	}
	col := def.Span.Start().Column()
	if col > 0 {
		col--
	}
	res := &FindResponse{
		Fn:  def.Span.URI().Filename(),
		Row: row,
		Col: col,
	}
	return res, nil
}

func replaceEnvVar(env []string, key, val string) []string {
	pfx := key + "="
	for i, s := range env {
		if strings.HasPrefix(s, pfx) {
			env[i] = key + "=" + val
			return env
		}
	}
	return append(env, key+"="+val)
}

func (f *FindRequest) Guru(ctx context.Context, buildCtxt *build.Context) (*FindResponse, error) {
	guruExe, err := exec.LookPath("guru")
	if err != nil {
		return nil, errors.New("please install guru: `go get -u golang.org/x/tools/cmd/guru`")
	}

	cmd := buildutil.GoCommandContext(
		ctx, buildCtxt,
		guruExe, "-modified", "-json",
		"definition", fmt.Sprintf("%s:#%d", f.Fn, f.Offset),
	)
	cmd.Dir = filepath.Dir(f.Fn)

	// Cap guru CPU usage so that gopls can run
	numCPU := runtime.NumCPU()
	if numCPU > 4 {
		numCPU /= 2
	}
	cmd.Env = replaceEnvVar(cmd.Env, "GOMAXPROCS", strconv.Itoa(numCPU))

	var (
		stdin  bytes.Buffer
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	stdin.Grow(len(f.Fn) + 32 + len(f.Src))
	stdin.WriteString(f.Fn)
	stdin.WriteByte('\n')
	stdin.WriteString(strconv.Itoa(len(f.Src)))
	stdin.WriteByte('\n')
	stdin.WriteString(f.Src)

	cmd.Stdin = &stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %s", bytes.TrimSpace(stderr.Bytes()), err)
	}

	var x struct {
		Pos string `json:"objpos"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &x); err != nil {
		return nil, err
	}

	loc, err := ParseSourceLocation(x.Pos)
	if err != nil {
		return nil, err
	}

	res := &FindResponse{
		Fn:  loc.Filename,
		Row: loc.Line - 1,
		Col: loc.ColStart - 1,
	}
	return res, nil
}

func fileExists(name string) bool {
	fi, err := os.Stat(name)
	return err == nil && fi.Mode().IsRegular()
}

// TODO: export this form github.com/charlievieth/godef
func (*FindRequest) updateFilename(ctxt *build.Context, filename string) (string, string, bool) {
	const Separator = string(filepath.Separator)

	if strings.HasPrefix(filename, ctxt.GOROOT) ||
		strings.HasPrefix(filename, ctxt.GOPATH) {
		return filename, "", false
	}

	dirs := strings.Split(filename, Separator)
	for i := len(dirs) - 1; i > 0; i-- {
		fakeRoot := strings.Join(dirs[:i], Separator)
		if !fileExists(fakeRoot + Separator + ".fake_goroot") {
			continue
		}
		path := filepath.Join(ctxt.GOROOT, "src", strings.Join(dirs[i:], Separator))
		if fileExists(path) {
			return path, fakeRoot, true
		}
		break // failed to find a match in GOROOT
	}

	return filename, "", false
}

func contextFromEnv(env map[string]string) *build.Context {
	ctx := copyContext(&build.Default)
	if s := env["GOARCH"]; s != "" {
		ctx.GOARCH = s
	}
	if s := env["GOOS"]; s != "" {
		ctx.GOOS = s
	}
	if s := env["GOROOT"]; s != "" {
		ctx.GOROOT = s
	}
	if s := env["GOPATH"]; s != "" {
		ctx.GOPATH = s
	}
	if s := env["CGO_ENABLED"]; s != "" {
		if enabled, err := strconv.ParseBool(s); err == nil {
			ctx.CgoEnabled = enabled
		}
	}
	return ctx
}

func copyContext(orig *build.Context) *build.Context {
	tmp := *orig // make a copy
	ctxt := &tmp
	ctxt.BuildTags = append([]string(nil), orig.BuildTags...)
	ctxt.ReleaseTags = append([]string(nil), orig.ReleaseTags...)
	return ctxt
}

func (f *FindRequest) Call() (interface{}, string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	parent := contextFromEnv(f.Env)
	name, fake, replaceRoot := f.updateFilename(parent, f.Fn)

	ctxt, err := buildutil.MatchContext(parent, name, f.Src)
	if err != nil {
		return []FindResponse{}, err.Error()
	}

	type Result struct {
		Res     *FindResponse
		Err     error
		Program string
	}

	ch := make(chan *Result, 2)

	go func() {
		res, err := f.Gopls(ctx, ctxt)
		select {
		case ch <- &Result{res, err, "gopls"}:
		case <-ctx.Done():
		}
	}()

	go func() {
		res, err := f.Guru(ctx, ctxt)
		select {
		case ch <- &Result{res, err, "guru"}:
		case <-ctx.Done():
		}
	}()

	var errs []error
	for i := 0; i < 2; i++ {
		res := <-ch
		if res.Err != nil {
			errs = append(errs, res.Err)
			continue
		}
		if res.Res != nil {
			fn := res.Res.Fn
			if replaceRoot && fake != "" {
				old := ctxt.GOROOT + string(filepath.Separator) + "src"
				res.Res.Fn = strings.Replace(fn, old, fake, 1)
			}
			res.Res.Program = res.Program
			return []FindResponse{*res.Res}, ""
		}
	}
	if len(errs) > 0 {
		a := make([]string, len(errs))
		for i, e := range errs {
			a[i] = e.Error()
		}
		sort.Strings(a)
		return []FindResponse{}, strings.Join(a, "; ")
	}

	// This should not happen
	return []FindResponse{}, "doc: internal error: no match and no error"
}
