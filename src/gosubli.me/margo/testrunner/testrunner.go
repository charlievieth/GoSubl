package testrunner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charlievieth/buildutil"
	"github.com/charlievieth/reonce"
)

// TODO: handle no tests
// {"Time":"2022-07-16T20:52:15.542552-04:00","Action":"output","Package":"repl/repl-1","Output":"?   \trepl/repl-1\t[no test files]\n"}
// {"Time":"2022-07-16T20:52:15.542677-04:00","Action":"skip","Package":"repl/repl-1","Elapsed":0}

type TestAction string

const (
	TestRun    = TestAction("run")    // the test has started running
	TestPause  = TestAction("pause")  // the test has been paused
	TestCont   = TestAction("cont")   // the test has continued running
	TestPass   = TestAction("pass")   // the test passed
	TestBench  = TestAction("bench")  // the benchmark printed log output but did not fail
	TestFail   = TestAction("fail")   // the test or benchmark failed
	TestOutput = TestAction("output") // the test printed output
	TestSkip   = TestAction("skip")   // the test was skipped or the package contained no tests
)

// func (a TestAction) String() string { return string(a) }

type TestEvent struct {
	Time    time.Time  `json:",omitempty"` // encodes as an RFC3339-format string
	Action  TestAction `json:",omitempty"`
	Package string     `json:",omitempty"`
	Test    *string    `json:",omitempty"`
	Elapsed *float64   `json:",omitempty"` // seconds
	Output  *string    `json:",omitempty"`
}

func (t *TestEvent) GetTime() (v time.Time) {
	if t != nil {
		v = t.Time
	}
	return v
}

func (t *TestEvent) GetAction() (v TestAction) {
	if t != nil {
		v = t.Action
	}
	return v
}

func (t *TestEvent) GetPackage() (v string) {
	if t != nil {
		v = t.Package
	}
	return v
}

func (t *TestEvent) GetTest() (v string) {
	if t != nil && t.Test != nil {
		v = *t.Test
	}
	return v
}

func (t *TestEvent) GetElapsed() (v float64) {
	if t != nil && t.Elapsed != nil {
		v = *t.Elapsed
	}
	return v
}

func (t *TestEvent) GetOutput() (v string) {
	if t != nil && t.Output != nil {
		v = *t.Output
	}
	return v
}

type TestEvents []TestEvent

func (e TestEvents) Len() int           { return len(e) }
func (e TestEvents) Less(i, j int) bool { return e[i].Time.Before(e[j].Time) }
func (e TestEvents) Swap(i, j int)      { e[i], e[j] = e[j], e[i] }

func (e TestEvents) HasAction(act TestAction) bool {
	for i := 0; i < len(e); i++ {
		if e[i].Action == act {
			return true
		}
	}
	return false
}

func (e TestEvents) FilterByAction(act TestAction) TestEvents {
	n := 0
	for i := 0; i < len(e); i++ {
		if e[i].Action == act {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	o := make(TestEvents, 0, n)
	for i := 0; i < len(e); i++ {
		if e[i].Action == act {
			o = append(o, e[i])
		}
	}
	return o
}

// Package => Test
type Output map[string]map[string]TestEvents

func (o Output) AddTest(pkgName, testName string, events []TestEvent) {
	pkg := o[pkgName]
	if pkg == nil {
		pkg = make(map[string]TestEvents)
		o[pkgName] = pkg
	}
	pkg[testName] = events
}

func (o Output) FilterByAction(act TestAction) Output {
	m := make(Output)
	for pkg, test := range o {
		for name, events := range test {
			if events.HasAction(act) {
				m.AddTest(pkg, name, events)
			}
		}
	}
	return m
}

func ParseEvents(r io.Reader) (Output, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	m := make(Output)
	for {
		var event TestEvent
		if err := dec.Decode(&event); err != nil {
			if err != io.EOF {
				return m, err
			}
			break
		}
		pkg := m[event.Package]
		if pkg == nil {
			pkg = make(map[string]TestEvents)
			m[event.Package] = pkg
		}
		if event.GetTest() != "" {
			pkg[event.GetTest()] = append(pkg[event.GetTest()], event)
		}
	}
	return m, nil
}

// TODO: list all test failues - not just the first line!
type TestFailure struct {
	Package  string   `json:"package"`
	Test     string   `json:"test"`
	Filename string   `json:"filename"`
	Line     int      `json:"line"`
	Failure  string   `json:"failure"`
	Output   []string `json:"output"`
}

func (t *TestFailure) GetOutput() string {
	if n := MinIndent(t.Output); n > 0 {
		var w strings.Builder
		prefix := strings.Repeat(" ", n)
		for _, s := range t.Output {
			w.WriteString(strings.TrimPrefix(s, prefix))
		}
		return w.String()
	}
	return ""
}

var ErrNoTestFailure = errors.New("no test failure")

var (
	filenameLineFailureRe = reonce.New(`^[ ]{4}(\w+\.go):(\d+)\:\s+(.*)`)
	testFailRe            = reonce.New(`^\s*--- FAIL:\s+Test\w+`)
	// testFailRe            = reonce.New(`^--- FAIL:\s+Test\w+`)
	testRunRe = reonce.New(`^=== RUN\s+`)
	ansiRe    = reonce.New(`(?m)` + "\x1b" + `\[(?:\d+(?:;\d+)*)?m`)
)

func ParseTestFailure(events TestEvents) (*TestFailure, error) {
	if !events.HasAction(TestFail) {
		return nil, ErrNoTestFailure
	}
	events = events.FilterByAction(TestOutput)
	if len(events) == 0 {
		return nil, ErrNoTestFailure
	}
	if testRunRe.MatchString(events[0].GetOutput()) {
		events = events[1:]
	}
	tf := &TestFailure{
		Package: events[0].Package,
		Test:    events[0].GetTest(),
	}
	for ; len(events) > 0; events = events[1:] {
		a := filenameLineFailureRe.FindStringSubmatch(events[0].GetOutput())
		if len(a) == 4 {
			tf.Filename = a[1]
			line, err := strconv.Atoi(a[2])
			if err != nil {
				continue
			}
			tf.Line = line
			tf.Failure = a[3]
			break
		}
	}
	if tf.Filename == "" {
		return nil, ErrNoTestFailure
	}
	tf.Output = make([]string, 0, len(events))
	for _, e := range events {
		out := strings.TrimSuffix(e.GetOutput(), "\n")
		if !testFailRe.MatchString(out) {
			// TODO: replace ANSI ???
			// tf.Output = append(tf.Output, ansiRe.ReplaceAllString(out, ""))
			tf.Output = append(tf.Output, e.GetOutput())
		}
	}

	// WARN: do we want this ???
	// for i, s := range tf.Output {
	// 	tf.Output[i] = strings.TrimPrefix(s, "    ")
	// }

	return tf, nil
}

func leadingSpaces(s string) int {
	if len(s) == 0 || s[0] != ' ' {
		return 0
	}
	var i int
	for i = 0; i < len(s) && s[i] == ' '; i++ {
	}
	return i
}

func stripANSI(s string) string {
	if strings.Contains(s, "\x1b[") {
		return ansiRe.ReplaceAllString(s, "")
	}
	return s
}

// TODO: use this
func MinIndent(a []string) int {
	if len(a) == 0 {
		return 0
	}
	min := leadingSpaces(a[0])
	for _, s := range a[1:] {
		// WARN: should we strip ANSI / non-printable chars ???
		// if n := leadingSpaces(s); n < min {
		if n := leadingSpaces(stripANSI(s)); n < min {
			min = n
		}
	}
	return min
}

func BuildTestPattern(names []string) string {
	var w strings.Builder
	w.WriteString(`^(`)
	if len(names) > 0 {
		w.WriteString(regexp.QuoteMeta(names[0]))
		for i := 1; i < len(names); i++ {
			w.WriteByte('|')
			w.WriteString(regexp.QuoteMeta(names[i]))
		}
	}
	w.WriteString(`)$`)
	return w.String()
}

// TODO:
//  * Package tests: don't have line numbers
//  	* The Package name is "" and there is no "Test" field
//  * Parent tests: don't have line numbers

func TestGoPkg(ctxt *build.Context, dir string, tests []string) ([]TestFailure, error) {
	args := []string{"test", "-json"}
	if len(tests) > 0 {
		args = append(args, "-run", BuildTestPattern(tests))
	}

	var stderr bytes.Buffer
	cmd := buildutil.GoCommand(ctxt, "go", args...)
	cmd.Dir = dir
	cmd.Stderr = &stderr
	rc, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	m, err := ParseEvents(rc)
	if err != nil {
		return nil, err
	}
	cerr := cmd.Wait()
	if cerr == nil {
		return nil, ErrNoTestFailure
	}
	if cerr != nil && stderr.Len() != 0 {
		return nil, fmt.Errorf("error testing: %q: %s: %s",
			dir, err, strings.TrimSpace(stderr.String()))
	}

	m = m.FilterByAction(TestFail)

	var failures []TestFailure
	for _, test := range m {
		for _, events := range test {
			f, err := ParseTestFailure(events)
			if err != nil {
				if err == ErrNoTestFailure {
					continue
				}
				return nil, err
			}
			failures = append(failures, *f)
		}
	}

	sort.Slice(failures, func(i, j int) bool {
		return failures[i].Test < failures[j].Test
	})
	sort.SliceStable(failures, func(i, j int) bool {
		return failures[i].Package < failures[j].Package
	})

	return failures, nil
}
