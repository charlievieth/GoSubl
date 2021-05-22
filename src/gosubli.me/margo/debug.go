package main

import (
	"bufio"
	"io"
	"log"
	"os"
	"os/user"
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

// WARN WARN WARN
const DEBUG = false

var (
	debugLogDir string
	debugStdin  *bufio.Writer
	debugStdout *bufio.Writer
)

func init() {
	if DEBUG {
		user, err := user.Current()
		if err != nil {

			panic("Error: " + err.Error())
		}
		if user.HomeDir == "" {
			panic("Error: empty HomeDir")
		}
		dirname := filepath.Join(user.HomeDir, "Desktop", "GoSublime_Logs")
		if err := os.MkdirAll(dirname, 0755); err != nil {
			panic("Error: " + err.Error())
		}

		stdinName := filepath.Join(dirname, "stdin.log")
		fi, err := os.OpenFile(stdinName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0664)
		if err != nil {
			panic("Error: " + err.Error())
		}
		stdoutName := filepath.Join(dirname, "stdout.log")
		fo, err := os.OpenFile(stdoutName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0664)
		if err != nil {
			panic("Error: " + err.Error())
		}
		stderrName := filepath.Join(dirname, "stderr.log")
		fe, err := os.OpenFile(stderrName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0664)
		if err != nil {
			panic("Error: " + err.Error())
		}
		debugStdin = bufio.NewWriter(fi)
		debugStdout = bufio.NewWriter(fo)

		logger = log.New(io.MultiWriter(os.Stderr, fe), "margo: ", log.Ldate|log.Ltime|log.Lshortfile)

		byeDefer(func() {
			if debugStdin != nil {
				debugStdin.Flush()
			}
			if debugStdout != nil {
				debugStdout.Flush()
			}
			if fi != nil {
				fi.Close()
			}
			if fo != nil {
				fo.Close()
			}
			if fe != nil {
				fe.Close()
			}
		})
	}
}

type FlushingWriter struct {
	*bufio.Writer
}

func (w *FlushingWriter) Write(p []byte) (int, error) {
	n, werr := w.Writer.Write(p)
	ferr := w.Writer.Flush()
	if ferr != nil && werr == nil {
		werr = ferr
	}
	return n, werr
}

func DebugWriter(w io.Writer) io.Writer {
	if DEBUG && debugStdout != nil {
		return io.MultiWriter(w, &FlushingWriter{debugStdout})
		// return io.MultiWriter(w, debugStdout)
	}
	return w
}

func WriteInput(p []byte) {
	if DEBUG && debugStdin != nil {
		debugStdin.Write(p)
		if len(p) != 0 && p[len(p)-1] != '\n' {
			debugStdin.WriteByte('\n')
		}
		debugStdin.Flush()
	}
}
