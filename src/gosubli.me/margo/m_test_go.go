package main

import (
	"errors"
	"go/build"
	"path/filepath"

	"github.com/charlievieth/buildutil"
	"go.uber.org/zap"

	"gosubli.me/margo/testrunner"
	"gosubli.me/margo/testutil"
)

type ListTestsRequest struct {
	Filename string `json:"filename"`
}

type ListTestsResponse = testutil.TestFunctions

func (r *ListTestsRequest) call() (*ListTestsResponse, error) {
	orig := build.Default
	ctxt, err := buildutil.MatchContext(&orig, r.Filename, nil)
	if err != nil {
		logger.Error("test: matching context", zap.Error(err))
		ctxt = &orig
	}
	return testutil.ListTestFunctions(ctxt, filepath.Dir(r.Filename))
}

func (r *ListTestsRequest) Call() (interface{}, string) {
	res, err := r.call()
	if res == nil {
		res = new(ListTestsResponse)
	}
	return res, errStr(err)
}

type ContainingFunctionRequest struct {
	Filename string  `json:"filename"`
	Source   *string `json:"source,omitempty"`
	Line     int     `json:"line"`
}

type ContainingFunctionResponse struct {
	Func string `json:"func"`
}

func (r *ContainingFunctionRequest) Call() (interface{}, string) {
	name, err := testutil.ContainingFunction(r.Filename, r.Source, r.Line)
	if err != nil {
		return nil, err.Error()
	}
	return ContainingFunctionResponse{Func: name}, ""
}

type TestRequest struct {
	// current filename: only used for build.Context
	CurrentFile string   `json:"filename"`
	Dir         string   `json:"dir"`
	Names       []string `json:"names"`
}

type TestResponse struct {
	Failed []testrunner.TestFailure `json:"failed"`
	// The Python code is dumb so maybe this makes life easier
	Success bool `json:"success"`
}

func (r *TestRequest) Call() (interface{}, string) {
	var ctxt *build.Context
	if r.CurrentFile != "" {
		var err error
		ctxt, err = buildutil.MatchContext(nil, r.CurrentFile, nil)
		if err != nil {
			logger.Error("test: matching context", zap.Error(err))
		}
	}
	failures, err := testrunner.TestGoPkg(ctxt, r.Dir, r.Names)
	if err != nil {
		if errors.Is(err, testrunner.ErrNoTestFailure) {
			return &TestResponse{Success: true}, ""
		}
		return EmptyResponse{}, err.Error()
	}
	if len(failures) == 0 {
		return &TestResponse{Success: true}, ""
	}
	return &TestResponse{Failed: failures}, ""
}

func init() {
	registry.Register("containing_function", func(b *Broker) Caller {
		return new(ContainingFunctionRequest)
	})
}

func init() {
	registry.Register("list_tests", func(b *Broker) Caller {
		return new(ListTestsRequest)
	})
}

func init() {
	registry.Register("run_tests", func(b *Broker) Caller {
		return new(ListTestsRequest)
	})
}
