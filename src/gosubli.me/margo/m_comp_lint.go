package main

import (
	"bytes"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/charlievieth/buildutil"
	"golang.org/x/sync/singleflight"
)

type CompLintRequest struct {
	Filename string `json:"filename"`
}

type CompileError struct {
	Row     int    `json:"row"`
	Col     int    `json:"col"`
	File    string `json:"file"`
	Message string `json:"message"`
}

type CompLintReport struct {
	Filename      string         `json:"filename"`
	TopLevelError string         `json:"top_level_error,omitempty"`
	CmdError      string         `json:"cmd_error,omitempty"`
	Errors        []CompileError `json:"errors,omitempty"`
}

var compRe = regexp.MustCompile(`(?m)^([a-zA-Z]?:?[^:]+):(\d+):?(\d+)?:? (.+)$`)

func (r *CompLintReport) ParseErrors(dirname string, out []byte) {
	const (
		FileIndex     = 1
		RowIndex      = 2
		ColIndex      = 3
		MsgIndex      = 4
		SubmatchCount = 5
	)
	out = bytes.TrimSpace(out)

	first := out
	if n := bytes.IndexByte(out, '\n'); n > 0 {
		first = bytes.TrimRightFunc(out[:n], unicode.IsSpace)
	}
	if len(first) != 0 && first[0] != '#' {
		r.TopLevelError = string(first)
	}

	lines := bytes.Split(out, []byte{'\n'})
	for i := 0; i < len(lines); i++ {
		line := string(lines[i])
		m := compRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		file := string(m[1])
		if !filepath.IsAbs(file) && dirname != "" {
			file = filepath.Join(dirname, file)
		}
		row, _ := strconv.Atoi(string(m[2]))
		col, _ := strconv.Atoi(string(m[3]))

		msg := string(m[4])
		if i < len(lines)-1 && len(lines[i+1]) != 0 && lines[i+1][0] == '\t' {
			msg = msg + " " + string(bytes.TrimSpace(lines[i+1]))
			i++
		}
		r.Errors = append(r.Errors, CompileError{
			Row:     row,
			Col:     col,
			File:    file,
			Message: msg,
		})
	}
}

// Send compile output to /dev/null if a directory exists with the same
// name as the default output binary
func (c *CompLintRequest) BuildOutput() []string {
	if runtime.GOOS != "windows" {
		dir := filepath.Dir(c.Filename)
		output := filepath.Join(dir, filepath.Base(dir))
		if isDir(output) {
			return []string{"-o", os.DevNull}
		}
	}
	return nil
}

var windowsReplacer struct {
	*strings.Replacer
	once sync.Once
}

func initWindowsReplacer() {
	const invalid = `*."/\[]:;|,`
	a := make([]string, 0, len(invalid)*2)
	for i := range invalid {
		a = append(a, invalid[i:i+1], "%")
	}
	windowsReplacer.Replacer = strings.NewReplacer(a...)
}

func (c *CompLintRequest) TestOutput() string {
	dir := filepath.Join(os.TempDir(), "margo-comp-lint")
	if os.MkdirAll(dir, 0744) != nil {
		return os.DevNull
	}
	s := c.Filename
	if runtime.GOOS != "windows" {
		s = strings.Replace(filepath.ToSlash(s), "/", "%", -1)
	} else {
		windowsReplacer.once.Do(initWindowsReplacer)
		s = windowsReplacer.Replace(s)
	}
	return filepath.Join(dir, s+".test")
}

func (c *CompLintRequest) Compile(src []byte) *CompLintReport {
	pkgname, _ := buildutil.ReadPackageName(c.Filename, src)

	ctxt, _ := buildutil.MatchContext(nil, c.Filename, src)
	if ctxt == nil {
		ctxt = &build.Default
	}

	var args []string
	switch {
	case strings.HasSuffix(c.Filename, "_test.go"):
		args = []string{"test", "-i", "-c", "-o", c.TestOutput()}
	case pkgname == "main":
		args = []string{"build"}
		if extra := c.BuildOutput(); len(extra) != 0 {
			args = append(args, extra...)
		}
	default:
		args = []string{"install"}
	}

	cmd := buildutil.GoCommand(ctxt, "go", args...)
	cmd.Dir = filepath.Dir(c.Filename)

	out, err := cmd.CombinedOutput()
	r := &CompLintReport{
		Filename: c.Filename,
	}
	if err != nil {
		r.CmdError = err.Error()
		r.ParseErrors(cmd.Dir, out)
	}
	return r
}

var compLintGroup singleflight.Group

func (c *CompLintRequest) Call() (interface{}, string) {
	src, err := ioutil.ReadFile(c.Filename)
	if err != nil {
		return &CompLintReport{CmdError: err.Error()}, err.Error()
	}
	key := string(src)
	v, _, _ := compLintGroup.Do(key, func() (interface{}, error) {
		return c.Compile(src), nil
	})
	r, ok := v.(*CompLintReport)
	if !ok {
		return nil, fmt.Sprintf("complint: invalid return type: %T", v)
	}
	return r, ""
}

func init() {
	registry.Register("comp_lint", func(_ *Broker) Caller {
		return &CompLintRequest{}
	})
}
