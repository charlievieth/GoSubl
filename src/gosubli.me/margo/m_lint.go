package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/build"
	"go/token"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/charlievieth/buildutil"
	"github.com/charlievieth/gotype"
)

type mLintReport struct {
	Fn      string
	Row     int
	Col     int
	Message string
	Kind    string
}

type mLint struct {
	Dir JsonString
	Fn  JsonString
	Src JsonString
	v   struct {
		dir string
		fn  string
		src string
	}
	Filter []string

	fset    *token.FileSet
	af      *ast.File
	reports []mLintReport
}

var (
	mLintErrPat = regexp.MustCompile(`(.+?):(\d+):(\d+): (.+)`)
	// mLinters    = map[string]func(kind string, m *mLint){
	// 	"gs.flag.parse": mLintCheckFlagParse,
	// 	"gs.types":      mLintCheckTypes,
	// }
)

func (m *mLint) Call() (interface{}, string) {
	const allErrors = false

	src := []byte(m.Src.String())
	filename := m.Fn.String()

	var allFiles bool
	if strings.HasSuffix(filename, "_test.go") {
		allFiles = true
	}

	context := func() *build.Context {
		var ctxt = build.Default
		if p := os.Getenv("GOPATH"); p != "" {
			ctxt.GOPATH = p
		}
		if p := runtime.GOROOT(); p != "" {
			ctxt.GOROOT = p
		}
		return &ctxt
	}

	list, err := gotype.Check(context(), filename, src, allFiles, allErrors)
	if err != nil {
		return nil, err.Error()
	}
	rep := make([]mLintReport, len(list))
	for i, e := range list {
		if e.Filename != filename {
			continue
		}
		rep[i] = mLintReport{
			Fn:      e.Filename,
			Row:     e.Row - 1,
			Col:     e.Col - 1,
			Message: e.Message,
			Kind:    e.Kind,
		}
	}
	res := M{
		"reports": rep,
	}
	return res, ""
}

var (
	compLintReRowCol  = regexp.MustCompile(`^\d+:\d+:\s+`)
	compLintReRow     = regexp.MustCompile(`^\d+:\s+`)
	NoCompileErrors   = []CompLintReport{}
	ErrCompLintIgnore = errors.New("CompLint: ignore line")
)

type CompLintReport struct {
	Row int    `json:"row"`
	Col int    `json:"col"`
	Msg string `json:"message"`
}

type CompLint struct {
	Filename string
	Dirname  string
	Env      map[string]string
}

func (*CompLint) ParseInt(line []byte) ([]byte, int, error) {
	i := bytes.IndexByte(line, ':')
	if i < 1 {
		return nil, -1, errors.New("invalid line: " + string(line))
	}
	n, err := strconv.Atoi(string(line[:i]))
	if err == nil {
		line = line[i+1:]
	}
	return line, n, err
}

func (c *CompLint) ParseRowCol(line []byte) (*CompLintReport, error) {
	line, row, err := c.ParseInt(line)
	if err != nil {
		return nil, err
	}
	line, col, err := c.ParseInt(line)
	if err != nil {
		return nil, err
	}
	return &CompLintReport{
		Row: int(row),
		Col: int(col),
		Msg: string(bytes.TrimSpace(line)),
	}, nil
}

func (c *CompLint) ParseRow(line []byte) (*CompLintReport, error) {
	line, row, err := c.ParseInt(line)
	if err != nil {
		return nil, err
	}
	return &CompLintReport{
		Row: int(row),
		Msg: string(bytes.TrimSpace(line)),
	}, nil
}

func (c *CompLint) MergeEnv(override map[string]string) []string {
	a := os.Environ()
	if len(override) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a)+len(override))
	env := a[:0]
	for k, v := range override {
		if v != "" {
			seen[k] = true
			env = append(env, k+"="+v)
		}
	}
	for _, s := range a {
		if n := strings.IndexByte(s, '='); n != -1 {
			if k := s[:n]; !seen[k] {
				seen[k] = true
				env = append(env, s)
			}
		}
	}
	// ensure we have a minimal Go environment
	for k, v := range defaultEnv() {
		if !seen[k] {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func (c *CompLint) CmdArgs() []string {
	// test
	if strings.HasSuffix(c.Filename, "_test.go") {
		return []string{"test", "-c", "-o", os.DevNull}
	}
	// main
	name, err := buildutil.ReadPackageName(c.Filename, nil)
	if err == nil && name == "main" {
		return []string{"build", "-o", os.DevNull}
	}
	// package
	return []string{"install"}
}

// TODO: add cmd to kill list
func (c *CompLint) Compile() ([]CompLintReport, error) {
	// TODO: probably don't need this since we set -o to /dev/null
	dirname, err := ioutil.TempDir("", "margo-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(dirname)

	// TODO: probably don't need this since we set -o to /dev/null
	if c.Env == nil {
		c.Env = make(map[string]string, 1)
	}
	c.Env["GOBIN"] = dirname

	var stderr bytes.Buffer
	cmd := exec.Command("go", c.CmdArgs()...)
	cmd.Dir = c.Dirname
	cmd.Env = c.MergeEnv(c.Env)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		return NoCompileErrors, nil // no error, nothing to report
	}

	var rep []CompLintReport
	var first error
	check := func(r *CompLintReport, err error) {
		if err != nil {
			if first == nil {
				first = err
			}
			return
		}
		rep = append(rep, *r)
	}

	sep := []byte(c.Filename + ":")
	for _, line := range bytes.Split(stderr.Bytes(), []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if n := bytes.Index(line, sep); n != -1 {
			line = line[n+len(sep):]
			switch {
			case compLintReRowCol.Match(line):
				check(c.ParseRowCol(line))
			case compLintReRow.Match(line):
				check(c.ParseRow(line))
			}
		}
	}
	return rep, first
}

func (c *CompLint) Call() (interface{}, string) {
	var errStr string
	rep, err := c.Compile()
	if err != nil {
		errStr = err.Error()
	}
	return rep, errStr
}

func init() {
	registry.Register("lint", func(_ *Broker) Caller {
		return &mLint{}
	})
}
