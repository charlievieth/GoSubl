package main

import (
	"flag"
	"fmt"
	"go/build"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"

	"github.com/charlievieth/godef"
)

var cpuprofileFlag = flag.String("cpuprofile", "", "write CPU profile to `file`")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(2)
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	// Profiling support.
	if *cpuprofileFlag != "" {
		f, err := os.Create(*cpuprofileFlag)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	filename, startOffset, _, err := parsePos(flag.Arg(0))
	if err != nil {
		Fatal(err)
	}
	conf := godef.Config{
		Context: build.Default,
	}

	pos, _, err := conf.Define(filename, startOffset, nil)
	if err != nil {
		Fatal(err)
	}
	fmt.Println(pos)
}

// parseOctothorpDecimal returns the numeric value if s matches "#%d",
// otherwise -1.
func parseOctothorpDecimal(s string) int {
	if s != "" && s[0] == '#' {
		if s, err := strconv.ParseInt(s[1:], 10, 32); err == nil {
			return int(s)
		}
	}
	return -1
}

func parsePos(pos string) (filename string, startOffset, endOffset int, err error) {
	if pos == "" {
		err = fmt.Errorf("no source position specified")
		return
	}

	colon := strings.LastIndex(pos, ":")
	if colon < 0 {
		err = fmt.Errorf("bad position syntax %q", pos)
		return
	}
	filename, offset := pos[:colon], pos[colon+1:]
	startOffset = -1
	endOffset = -1
	if comma := strings.Index(offset, ","); comma < 0 {
		// e.g. "foo.go:#123"
		startOffset = parseOctothorpDecimal(offset)
		endOffset = startOffset
	} else {
		// e.g. "foo.go:#123,#456"
		startOffset = parseOctothorpDecimal(offset[:comma])
		endOffset = parseOctothorpDecimal(offset[comma+1:])
	}
	if startOffset < 0 || endOffset < 0 {
		err = fmt.Errorf("invalid offset %q in query position", offset)
		return
	}
	return
}

func Fatal(err interface{}) {
	if err == nil {
		return
	}
	errMsg := "Error"
	if _, file, line, _ := runtime.Caller(1); file != "" {
		errMsg = fmt.Sprintf("Error (%s:#%d)", filepath.Base(file), line)
	}
	switch e := err.(type) {
	case string, error, fmt.Stringer:
		fmt.Fprintf(os.Stderr, "%s: %s\n", errMsg, e)
	default:
		fmt.Fprintf(os.Stderr, "%s: %#v\n", errMsg, e)
	}
	os.Exit(1)
}
