package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
)

/*
u, err := user.Current()
		if err != nil {
			return
		}
		debugLogDir = filepath.Join(u.HomeDir, "Desktop", dirname)
		if _, err := os.Stat(debugLogDir); err != nil {
			if !os.IsNotExist(err) {
				return
			}
			if err := os.MkdirAll(debugLogDir, 0755); err != nil {
				return
			}
		}
*/

const DEBUG = false

var (
	debugLogDir string
	debugStdin  *bufio.Writer
	debugStdout *bufio.Writer
)

func init() {
	if DEBUG {
		const dirname = "/Users/Charlie/Desktop/GoSublime_Logs"
		stdinName := filepath.Join(dirname, "stdin.log")
		fi, err := os.OpenFile(stdinName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
		if err != nil {
			return
		}
		stdoutName := filepath.Join(dirname, "stdout.log")
		fo, err := os.OpenFile(stdoutName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
		if err != nil {
			return
		}
		debugStdin = bufio.NewWriter(fi)
		debugStdout = bufio.NewWriter(fo)

		byeDefer(func() {
			if debugStdin != nil {
				debugStdin.Flush()
			}
			if debugStdout != nil {
				debugStdout.Flush()
			}
		})
	}
}

func DebugWriter(w io.Writer) io.Writer {
	if DEBUG && debugStdout != nil {
		return io.MultiWriter(w, debugStdout)
	}
	return w
}

func WriteInput(p []byte) {
	if DEBUG && debugStdin != nil {
		debugStdin.Write(p)
		if len(p) != 0 && p[len(p)-1] != '\n' {
			debugStdin.WriteByte('\n')
		}
	}
}
