package main

import (
	"bytes"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/charlievieth/buildutil"
)

var tempDirOnce sync.Once
var tempGOBIN string

func TempGOBIN() string {
	tempDirOnce.Do(func() {
		dir, err := ioutil.TempDir("", "margo_complint_")
		if err != nil {
			panic(err)
		}
		tempGOBIN = dir
		byeDefer(func() {
			os.RemoveAll(tempGOBIN)
		})
	})
	return tempGOBIN
}

type LintRequest struct {
	Filename string
	Env      map[string]string
}

func (r *LintRequest) environment() []string {
	env := os.Environ()
	if len(r.Env) == 0 {
		r.Env = make(map[string]string, len(env)+1)
		r.Env["GOBIN"] = TempGOBIN()
	}
	for _, s := range env {
		if n := strings.IndexByte(s, '='); n != 0 {
			key := s[:n]
			if _, ok := r.Env[key]; !ok {
				r.Env[key] = s[n+1:] // val
			}
		}
	}
	if n := len(r.Env); n > len(env) {
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

func (r *LintRequest) MainPkg() bool {
	name, err := buildutil.ReadPackageName(r.Filename, nil)
	return err == nil && name == "main"
}

func (r *LintRequest) Call() (interface{}, string) {
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

	dir, file := filepath.Split(r.Filename)

	re, err := regexp.Compile(regexp.QuoteMeta(file) + pattern)
	if err != nil {
		return LintReport{}, err.Error()
	}

	cmd := exec.Command("go", args...)
	cmd.Env = r.environment()
	cmd.Dir = filepath.Dir(dir)
	out, _ := cmd.CombinedOutput()
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		re.Match(line) // WARN
	}

	return nil, ""
}

type LintReport struct {
	Row     int    `json:"row"`
	Column  int    `json:"col"`
	Message string `json:"msg"`
}
