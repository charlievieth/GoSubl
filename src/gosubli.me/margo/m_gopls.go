package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charlievieth/buildutil"
	"github.com/charlievieth/buildutil/contextutil"
)

// TODO: include src one we start talking to gopls directly
type ReferencesRequest struct {
	Filename string            `json:"filename"`
	Offset   int               `json:"offset"`
	Env      map[string]string `json:"env"`
}

type SourceLocation struct {
	Filename string `json:"filename"`
	Relname  string `json:"relname,omitempty"` // relative path
	Line     int    `json:"line"`
	ColStart int    `json:"col_start"`
	ColEnd   int    `json:"col_end"`
}

func sortSourceLocations(a []*SourceLocation) []*SourceLocation {
	if len(a) == 0 {
		return a
	}
	sort.Slice(a, func(i, j int) bool {
		return a[i].ColStart < a[j].ColStart
	})
	sort.SliceStable(a, func(i, j int) bool {
		return a[i].Line < a[j].Line
	})
	sort.SliceStable(a, func(i, j int) bool {
		return a[i].Filename < a[j].Filename
	})
	return a
}

func ParseSourceLocation(pos string) (*SourceLocation, error) {
	s := pos
	var ci int // column i
	var i int
	for i = len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			if ci != 0 {
				break
			}
			ci = i
		}
	}
	if i < 0 {
		return nil, errors.New("invalid location: " + pos)
	}

	loc := SourceLocation{Filename: s[:i]}

	var err error
	if loc.Line, err = strconv.Atoi(s[i+1 : ci]); err != nil {
		return nil, fmt.Errorf("parsing line: %w", err)
	}

	cols := s[ci+1:]
	if j := strings.IndexByte(cols, '-'); j != -1 {
		if loc.ColStart, err = strconv.Atoi(cols[:j]); err != nil {
			return nil, fmt.Errorf("parsing col start: %w", err)
		}
		if loc.ColEnd, err = strconv.Atoi(cols[j+1:]); err != nil {
			return nil, fmt.Errorf("parsing col end: %w", err)
		}
	} else {
		if loc.ColStart, err = strconv.Atoi(cols); err != nil {
			return nil, fmt.Errorf("parsing col start: %w", err)
		}
	}

	return &loc, nil
}

// type ReferencesResponse struct {
// 	Env      map[string]string
// 	Filename string
// 	Pos      int
// }

// better?
// var re = regexp.MustCompile(`(?m)^(?:/.*/)+[-_. [:alnum:]]+\.go:(\d+):(\d+(?:-\d+)?):?\b`)
//
// var re = regexp.MustCompile(`(?m)^(?:/[^/\n]+)+\.go:(\d+):(\d+(?:-\d+)?):?\b`)

type GoplsServer struct{}

func (s *GoplsServer) CreateSocket() error {
	return nil
}

func StartGoplsServer() error {
	// TODO: attempt to install ???
	goplsPath, err := exec.LookPath("gopls")
	if err != nil {
		return err
	}
	_ = goplsPath
	// -listen.timeout
	// -listen
	return nil
}

type GuruWhatResponse struct {
	Modes      []string `json:"modes"`
	SrcDir     string   `json:"srcdir"`
	ImportPath string   `json:"importpath"`
	Object     string   `json:"object"`
	SameIDs    []string `json:"sameids"`
}

var ErrGuruNotInstalled = errors.New("please install guru: `go get -u golang.org/x/tools/cmd/guru`")

func GuruCmd(ctx context.Context, cmd, filename string, env map[string]string, src, dst interface{}) error {
	guruExe, err := exec.LookPath("guru")
	if err != nil {
		return ErrGuruNotInstalled
	}

	if ctx == nil {
		ctx = context.Background()
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	parent := build.Default
	if s, ok := env["GOPATH"]; ok && isDir(s) {
		parent.GOPATH = s
	}
	if s, ok := env["GOROOT"]; ok && isDir(s) {
		parent.GOROOT = s
	}
	if s := env["GOOS"]; s != "" {
		parent.GOOS = s
	}

	ctxt, err := buildutil.MatchContext(&parent, filename, nil)
	if err != nil {
		return err
	}

	_ = guruExe
	_ = ctxt
	// cmd := buildutil.GoCommandContext(
	// 	ctx, ctxt,
	// 	guruExe, "-json",
	// 	"what", fmt.Sprintf("%s:#%d", r.Filename, r.Offset),
	// )
	// _ = cmd
	// cmd.Dir = filepath.Dir(r.Filename)

	return nil
}

// TODO: use this to determine is a symbol is exported. If not, we
// can maybe use gopls to make it faster.
//
// func isBoundary(r byte) bool {
// 	switch r {
// 	case ' ', '\t', '\n', '\r', '!', '%', '&', '(', ')', '*', '+', ',', '-',
// 		'.', '/', ':', ';', '<', '=', '>', '[', ']', '^', '{', '|', '}':
// 		return true
// 	}
// 	return false
// }
//
// func SymbolName(src string, offset int) (string, error) {
// 	if uint(offset) >= uint(len(src)) {
// 		return "", errors.New("invalid offset")
// 	}
// 	var start, end int
// 	for i := offset; i < len(src); i++ {
// 		if isBoundary(src[i]) {
// 			end = i
// 			break
// 		}
// 	}
// 	for i := offset; i >= 0; i-- {
// 		if isBoundary(src[i]) {
// 			start = i + 1
// 			break
// 		}
// 	}
// 	if start >= end {
// 		return "", errors.New("no symbol at offset")
// 	}
// 	return src[start:end], nil
// }

func (r *ReferencesRequest) GuruWhat() (*GuruWhatResponse, error) {
	// TODO (CEV): support the source being modified

	guruExe, err := exec.LookPath("guru")
	if err != nil {
		return nil, errors.New("please install guru: " +
			"`go get -u golang.org/x/tools/cmd/guru`")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	parent := build.Default
	if r.Env != nil {
		if s, ok := r.Env["GOPATH"]; ok && isDir(s) {
			parent.GOPATH = s
		}
		if s, ok := r.Env["GOROOT"]; ok && isDir(s) {
			parent.GOROOT = s
		}
		if s := r.Env["GOOS"]; s != "" {
			parent.GOOS = s
		}
	}

	ctxt, err := buildutil.MatchContext(&parent, r.Filename, nil)
	if err != nil {
		return nil, err
	}
	cmd := buildutil.GoCommandContext(
		ctx, ctxt,
		guruExe, "-json",
		"what", fmt.Sprintf("%s:#%d", r.Filename, r.Offset),
	)
	cmd.Dir = filepath.Dir(r.Filename)

	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("command %q failed: %s: STDERR: %s\n",
				cmd.Args, err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("command %q failed: %s", cmd.Args, err)
	}

	var res GuruWhatResponse
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

func (r *ReferencesRequest) Call() (interface{}, string) {
	// CEV: we probably don't need this
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir := filepath.ToSlash(filepath.Dir(r.Filename))
	root, _ := contextutil.FindProjectRoot(&build.Default, dir)

	// TODO: use line/column (note: unicode is handled on the python side)

	// TODO: add back "-remote=auto" when it's working again. Currently,
	// it fails if a gopls instance is not currently serving. Previousy,
	// it would spawn a new server.
	cmd := exec.CommandContext(ctx, "gopls", "references", "-d",
		fmt.Sprintf("%s:#%d", r.Filename, r.Offset))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = root

	// this is dumb fix this
	id := numbers.nextString()
	watchCmd(id, cmd)
	defer unwatchCmd(id)

	res := []*SourceLocation{}
	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Run()
	}()
	select {
	case err := <-errCh:
		if err != nil {
			return res, fmt.Sprintf("gopls: references: %s: %s", err.Error(),
				strings.TrimSpace(stderr.String()))
		}
	case <-time.After(time.Second * 30):
		return res, fmt.Sprintf("gopls: references: timed out")
	}

	var first error
	lines := bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte{'\n'})
	for _, line := range lines {
		loc, err := ParseSourceLocation(string(line))
		if err != nil && first == nil {
			first = err
		} else {
			res = append(res, loc)
		}
	}
	if first != nil {
		return res, first.Error()
	}

	var (
		local       []*SourceLocation
		samePkg     []*SourceLocation
		sameProject []*SourceLocation
		other       []*SourceLocation
	)

	if root == "/" || root == filepath.VolumeName(dir) {
		root = ""
	}
	root = filepath.ToSlash(root) + "/"

	for _, rr := range res {
		name := filepath.ToSlash(rr.Filename)
		switch {
		case name == r.Filename:
			local = append(local, rr)
		case filepath.ToSlash(filepath.Dir(name)) == dir:
			samePkg = append(samePkg, rr)
		case root != "" && strings.HasPrefix(name, root):
			sameProject = append(sameProject, rr)
		default:
			other = append(other, rr)
		}
	}

	local = sortSourceLocations(local)
	samePkg = sortSourceLocations(samePkg)
	sameProject = sortSourceLocations(sameProject)
	other = sortSourceLocations(other)

	all := append(local, samePkg...)
	all = append(all, sameProject...)
	all = append(all, other...)

	if root != "" {
		for i, s := range all {
			name := filepath.ToSlash(s.Filename)
			if strings.HasPrefix(name, root) {
				rel, err := filepath.Rel(root, name)
				if err != nil {
					continue
				}
				all[i].Relname = rel
			}
		}
	}

	return all, ""
}

/*
func (f *FindRequest) XCall() (interface{}, string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// TODO: use buildutil.MatchContext
	parent := build.Default
	if f.Env != nil {
		if s, ok := f.Env["GOPATH"]; ok && isDir(s) {
			parent.GOPATH = s
		}
		if s, ok := f.Env["GOROOT"]; ok && isDir(s) {
			parent.GOROOT = s
		}
		if s := f.Env["GOOS"]; s != "" {
			parent.GOOS = s
		}
	}

	type Response struct {
		FindResponse
		Err error
	}
	resp := make(chan *Response, 2)
	// guru
	{
		go func() {
			ctxt, err := buildutil.MatchContext(&parent, f.Fn, f.Src)
			if err != nil {
				resp <- &Response{Err: err}
				return
			}
			cmd := buildutil.GoCommandContext(
				ctx, ctxt,
				"guru", "-modified", "-json",
				"definition", fmt.Sprintf("%s:#%d", f.Fn, f.Offset),
			)
			cmd.Dir = filepath.Dir(f.Fn)

			var (
				stdin  bytes.Buffer
				stdout bytes.Buffer
				stderr bytes.Buffer
			)
			stdin.Grow(len(f.Src) + 4096)
			fmt.Sprintf("%s\n%d\n%s", f.Fn, len(f.Src), f.Src)
			cmd.Stdin = &stdin
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			type Guru struct {
				Pos string `json:"objpos"`
			}
			if err := cmd.Run(); err != nil {
				resp <- &Response{Err: fmt.Errorf("%w: %s", err, stderr.String())}
				return
			}

		}()
	}
	conf := godef.Config{
		Context: ctxt,
	}
	pos, src, err := conf.Define(f.Fn, f.Offset, f.Src)
	if err != nil {
		return nil, err.Error()
	}
	res := FindResponse{
		Src: string(src),
		Fn:  pos.Filename,
		Row: pos.Line - 1,
		Col: pos.Column - 1,
	}
	return []FindResponse{res}, ""
}
*/

func init() {
	registry.Register("references", func(_ *Broker) Caller {
		return &ReferencesRequest{
			Env: map[string]string{},
		}
	})
}
