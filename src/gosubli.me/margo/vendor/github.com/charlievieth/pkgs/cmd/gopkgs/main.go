package main

import (
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/charlievieth/pkgs"
)

func main() {
	ctxt := build.Default

	importDir := "/Users/charlie/go/src/github.com/charlievieth/pkgs/cmd"

	t := time.Now()
	s, err := pkgs.Walk(&ctxt, importDir)
	if err != nil {
		Fatal(err)
	}
	d := time.Since(t)

	// for i := 0; i < len(s); i++ {
	//  fmt.Println(s[i])
	// }

	n := len(s)
	fmt.Println("len:", n, d, d/time.Duration(n))

}

func Fatal(err interface{}) {
	if err == nil {
		return
	}
	_, file, line, ok := runtime.Caller(1)
	if ok {
		file = filepath.Base(file)
	}
	switch err.(type) {
	case error, string, fmt.Stringer:
		if ok {
			fmt.Fprintf(os.Stderr, "Error (%s:%d): %s", file, line, err)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s", err)
		}
	default:
		if ok {
			fmt.Fprintf(os.Stderr, "Error (%s:%d): %#v\n", file, line, err)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %#v\n", err)
		}
	}
	os.Exit(1)
}
