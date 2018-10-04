package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/charlievieth/buildutil"
)

type LintReport struct {
	Row     int    `json:"row"`
	Column  int    `json:"col"`
	Message string `json:"msg"`
}

var NoLintReport = []LintReport{}

type LintRequest struct {
	Filename string            `json:"filename"`
	Dirname  string            `json:"dirname"`
	Env      map[string]string `json:"env"`
}

func (r *LintRequest) environment() []string {
	env := os.Environ()
	if len(r.Env) == 0 {
		return env
	}
	r.Env = make(map[string]string, len(env)+len(r.Env))
	for _, s := range env {
		if n := strings.IndexByte(s, '='); n != 0 {
			key := s[:n]
			if _, ok := r.Env[key]; !ok {
				r.Env[key] = s[n+1:] // val
			}
		}
	}
	if n := len(r.Env); len(env) < n {
		env = make([]string, 0, n)
	} else {
		env = env[:0]
	}
	for k, v := range r.Env {
		env = append(env, k+"="+v)
	}
	return env
}

func (r *LintRequest) TestPkg() bool {
	return strings.HasSuffix(r.Filename, "_test.go")
}

// TODO (CEV): cache known main files
func (r *LintRequest) MainPkg() bool {
	name, err := buildutil.ReadPackageName(r.Filename, nil)
	return err == nil && name == "main"
}

func (r *LintRequest) Lint() ([]LintReport, error) {
	const pattern = `:(\d+)(?:[:](\d+))?\W+(.+)\s*`
	var args []string
	switch {
	case r.TestPkg():
		args = []string{"test", "-i", "-c", "-o", os.DevNull}
	case r.MainPkg():
		args = []string{"build", "-i", "-o", os.DevNull}
	default:
		args = []string{"install", "-i"}
	}
	// CEV: ignoring < go1.10 compatibility by adding '-i' flag

	// TODO: lazily initialize
	re, err := regexp.Compile(regexp.QuoteMeta(filepath.Base(r.Filename)) + pattern)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("go", args...)
	cmd.Env = r.environment()
	cmd.Dir = r.Dirname
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil, nil
	}
	out = bytes.TrimSpace(out)

	var reports []LintReport
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		// TODO: check if the line contains the file name
		if a := re.FindSubmatch(line); len(a) == 4 {
			row, err := strconv.Atoi(string(a[1]))
			if err != nil {
				continue // TODO: report
			}
			col, err := strconv.Atoi(string(a[2]))
			if err != nil {
				col = 1 // allow column to be missing
			}
			reports = append(reports, LintReport{
				Row:     max(row-1, 0),
				Column:  max(col-1, 0),
				Message: string(bytes.TrimSpace(a[3])),
			})
		}
	}

	return reports, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (r *LintRequest) Call() (interface{}, string) {
	rep, err := r.Lint()
	if err != nil {
		return NoLintReport, err.Error()
	}
	if rep == nil {
		rep = NoLintReport
	}
	return rep, ""
}

func init() {
	registry.Register("complint", func(*Broker) Caller {
		return new(LintRequest)
	})
}
