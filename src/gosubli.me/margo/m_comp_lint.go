package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
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

var windowsPathReplacer *strings.Replacer

func init() {
	if runtime.GOOS == "windows" {
		const invalid = `*."/\[]:;|,`
		a := make([]string, 0, len(invalid)*2)
		for _, r := range invalid {
			a = append(a, string(r), `%`)
		}
		windowsPathReplacer = strings.NewReplacer(a...)
	}
}

var startGarbageCollectTestOutputOnce sync.Once

func garbageCollectTestOutput(dirname string) {
	for {
		fis, err := ioutil.ReadDir(dirname)
		if err != nil {
			continue
		}
		for _, fi := range fis {
			if time.Since(fi.ModTime()) > time.Hour {
				os.Remove(filepath.Join(dirname, fi.Name()))
			}
		}
		time.Sleep(time.Minute * 5)
	}
}

func (c *CompLintRequest) TestOutput() string {
	dir := filepath.Join(os.TempDir(), "margo-comp-lint")
	if os.MkdirAll(dir, 0755) != nil {
		return os.DevNull
	}
	startGarbageCollectTestOutputOnce.Do(func() {
		go garbageCollectTestOutput(dir)
	})
	s := c.Filename
	if runtime.GOOS != "windows" {
		s = strings.Replace(filepath.ToSlash(s), "/", "%", -1)
	} else {
		s = windowsPathReplacer.Replace(s)
	}
	// The max file name is 255 on Darwin and 259 on Windows so use 254 to be safe.
	if len(s) >= 254-len(".test") {
		h := md5.Sum([]byte(s))
		s = hex.EncodeToString(h[:]) + "." + filepath.Base(c.Filename)
	}
	return filepath.Join(dir, s+".test")
}

func (c *CompLintRequest) isGenerateCommand(ctxt *build.Context, pkgName string) bool {
	if pkgName != "main" {
		return false
	}
	if len(ctxt.BuildTags) == 0 {
		return false
	}
	dir := filepath.Dir(c.Filename)
	des, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	base := filepath.Base(c.Filename)
	n := 0
	for _, d := range des {
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || name == base {
			continue
		}
		n++
		path := dir + string(filepath.Separator) + name
		pkgname, _ := buildutil.ReadPackageName(path, nil)
		if pkgName != "" && pkgname != "main" {
			return true
		}
		if n == 4 {
			break
		}
	}
	return false
}

func hasExitCode(err error, code int) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode() == code
	}
	return false
}

func isIFlagError(out []byte, err error) bool {
	if !hasExitCode(err, 2) {
		return false
	}
	for _, msg := range []string{
		"flag provided but not defined: -i",
		"unknown flag -i",
	} {
		if bytes.Contains(out, []byte(msg)) {
			return true
		}
	}
	return false
}

func containsArg(arg string, args []string) bool {
	for _, s := range args {
		if s == arg {
			return true
		}
	}
	return false
}

func removeArg(remove string, args []string) []string {
	a := args[:0]
	for _, s := range args {
		if s != remove {
			a = append(a, s)
		}
	}
	return a
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
		if c.isGenerateCommand(ctxt, pkgname) {
			args = append(args, filepath.Base(c.Filename))
		}
	default:
		args = []string{"install"}
	}
	if !goCmdIFlagSupported {
		args = removeArg("-i", args)
	}

	dir := filepath.Dir(c.Filename)
	cmd := buildutil.GoCommand(ctxt, "go", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil && isIFlagError(out, err) && containsArg("-i", args) {
		args = removeArg("-i", args)
		cmd = buildutil.GoCommand(ctxt, "go", args...)
		cmd.Dir = dir
		out, err = cmd.CombinedOutput()
	}
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
