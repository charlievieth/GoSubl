package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type mPlay struct {
	Args      []string          `json:"args"`
	Dir       string            `json:"dir"`
	Fn        string            `json:"fn"`
	Src       string            `json:"src"`
	Env       map[string]string `json:"env"`
	Cid       string            `json:"cid"`
	BuildOnly bool              `json:"build_only"`
	tmpFile   string
	b         *Broker
}

type mPlayResponse struct {
	TempFile string   `json:"tmpFn"`
	Filename string   `json:"fn"`
	Stdout   JsonData `json:"out"`
	Stderr   JsonData `json:"err"`
	Duration string   `json:"dur"`
}

func (m *mPlay) writeTmpFile(dirname string) (string, error) {
	name := filepath.Join(dirname, "a.go")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(m.Src); err != nil {
		return "", err
	}
	return name, nil
}

func (m *mPlay) tmpDir() (string, error) {
	var dir string
	if m.Env != nil {
		var tmp string
		if s := m.Env["TMP"]; s != "" {
			tmp = s
		} else if s := m.Env["TMPDIR"]; s != "" {
			tmp = s
		}
		if tmp != "" {
			if err := os.MkdirAll(dir, 0777); err == nil {
				dir = tmp
			}
		}
	}
	if dir == "" {
		dir = os.TempDir()
	}
	tmpdir, err := ioutil.TempDir(dir, "play-")
	if err != nil {
		return "", err
	}
	return tmpdir, nil
}

// WARN (CEV): this appears to not work
//
// returns the environment for a command.  If the command is 'go run' or
// 'go test' GOOS and GOARCH are set to the system GOOS and GOARCH.
func (m *mPlay) cmdEnv(name string, args ...string) []string {
	const sysGOOS = "GOOS=" + runtime.GOOS
	const sysGOARCH = "GOARCH=" + runtime.GOARCH

	env := envSlice(m.Env)
	if s := filepath.Base(name); s != "go" && s != "go.exe" {
		return env
	}

	// only 'test' and 'run' need the system GOOS and GOARCH to work.
	switch len(args) {
	case 0:
		return env
	case 3:
		if args[0] != "build" || args[1] != "-o" || filepath.Base(args[2]) != "gosublime.a.exe" {
			return env
		}
	default:
		if args[0] != "run" && args[0] != "test" {
			return env
		}
	}
	for i, s := range env {
		switch {
		case strings.HasPrefix(s, "GOOS="):
			env[i] = sysGOOS
		case strings.HasPrefix(s, "GOARCH="):
			env[i] = sysGOARCH
		}
	}
	return env
}

func (m *mPlay) runCmd(name string, args ...string) (*mPlayResponse, string) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command(name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = m.Dir
	cmd.Env = m.cmdEnv(name, args...)

	watchCmd(m.Cid, cmd)
	defer unwatchCmd(m.Cid)

	t := time.Now()
	err := cmd.Run()
	d := time.Since(t)

	res := &mPlayResponse{
		TempFile: m.tmpFile,
		Filename: m.Fn,
		Stdout:   JsonData(stdout.Bytes()),
		Stderr:   JsonData(stderr.Bytes()),
		Duration: d.String(),
	}
	var errStr string
	if err != nil {
		errStr = err.Error()
	}
	return res, errStr
}

// isCommand returns if the pkg at pkgpath is a command (package "main").
func (m *mPlay) isCommand(dirname, filename string) (bool, error) {
	fset := token.NewFileSet()
	if filename != "" {
		af, err := parser.ParseFile(fset, filename, nil, parser.ImportsOnly)
		if err == nil && af.Name != nil {
			return af.Name.Name == "main", nil
		}
	}

	f, err := os.Open(dirname)
	if err != nil {
		return false, err
	}
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return false, err
	}

	for _, name := range names {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		path := filepath.Join(dirname, name)
		af, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			continue
		}
		if af.Name != nil {
			return af.Name.Name == "main", nil
		}
	}
	return false, nil
}

// todo: send the client output as it comes
func (m *mPlay) Call() (interface{}, string) {
	dir, err := m.tmpDir()
	if err != nil {
		return nil, err.Error()
	}
	defer os.RemoveAll(dir)

	if m.Src != "" {
		m.tmpFile, err = m.writeTmpFile(dir)
		if err != nil {
			return nil, err.Error()
		}
		m.Dir = dir
	}
	if m.Dir == "" {
		return nil, "missing directory"
	}
	if m.Args == nil {
		m.Args = []string{}
	}

	// not really sure whats going on here...
	if m.Cid == "" {
		m.Cid = "play.auto." + numbers.nextString()
	} else {
		killCmd(m.Cid)
	}

	if !m.BuildOnly {
		cmd, err := m.isCommand(m.Dir, m.Fn)
		if err != nil {
			return EmptyResponse, err.Error()
		}
		if !cmd {
			return m.runCmd("go", "test")
		}
	}

	// CEV: Temp workaround until mPlay.cmdEnv() works.
	//
	// We are building a Go program to execute, make sure GOOS and GOARCH
	// match the host OS.
	if m.Env != nil {
		m.Env["GOOS"] = runtime.GOOS
		m.Env["GOARCH"] = runtime.GOARCH
	}
	fn := filepath.Join(dir, "gosublime.a.exe")
	res, errStr := m.runCmd("go", "build", "-o", fn)
	if m.BuildOnly || errStr != "" {
		return res, errStr
	}
	return m.runCmd(fn, m.Args...)
}

func init() {
	registry.Register("play", func(b *Broker) Caller {
		return &mPlay{
			b:   b,
			Env: map[string]string{},
		}
	})
}
