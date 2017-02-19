package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type mShCmd struct {
	Name  string
	Args  []string
	Input string
	And   *mShCmd
	Or    *mShCmd
}

type mSh struct {
	Env map[string]string
	Cmd mShCmd
	Cid string
	Cwd string
}

func (m *mSh) updateGOOS() bool {
	if len(m.Cmd.Args) == 0 {
		return false
	}
	name := strings.ToLower(filepath.Base(m.Cmd.Name))
	if name == "go" || name == "go.exe" {
		arg := strings.ToLower(m.Cmd.Args[0])
		return arg == "run" || arg == "test"
	}
	return false
}

func (m *mSh) environment() []string {
	env := envSlice(m.Env)
	if m.updateGOOS() {
		for i, s := range env {
			switch {
			case strings.HasPrefix(s, "GOOS="):
				env[i] = "GOOS=" + runtime.GOOS
			case strings.HasPrefix(s, "GOARCH="):
				env[i] = "GOARCH=" + runtime.GOARCH
			}
		}
	}
	return env
}

// todo: send the client output as it comes
//       handle And, Or
func (m *mSh) Call() (interface{}, string) {
	if m.Cid == "" {
		m.Cid = "sh.auto." + numbers.nextString()
	} else {
		killCmd(m.Cid)
	}

	start := time.Now()
	var stdErr bytes.Buffer
	var stdOut bytes.Buffer

	c := exec.Command(m.Cmd.Name, m.Cmd.Args...)
	c.Stdout = &stdOut
	c.Stderr = &stdErr
	if m.Cmd.Input != "" {
		c.Stdin = strings.NewReader(m.Cmd.Input)
	}
	c.Dir = m.Cwd
	c.Env = m.environment()

	watchCmd(m.Cid, c)
	err := c.Run()
	unwatchCmd(m.Cid)

	res := M{
		"out": JsonData(stdOut.Bytes()),
		"err": JsonData(stdErr.Bytes()),
		"dur": time.Now().Sub(start).String(),
	}
	return res, errStr(err)
}

func init() {
	registry.Register("sh", func(b *Broker) Caller {
		return &mSh{}
	})
}
