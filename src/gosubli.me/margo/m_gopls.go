package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// TODO: include src one we start talking to gopls directly
type ReferencesRequest struct {
	Filename string            `json:"filename"`
	Offset   int               `json:"offset"`
	Env      map[string]string `json:"env"`
}

type SourceLocation struct {
	Filename string `json:"filename"`
	Line     int    `json:"line"`
	ColStart int    `json:"col_start"`
	ColEnd   int    `json:"col_end"`
}

func ParseSourceLocation(pos string) (*SourceLocation, error) {
	s := pos
	var ci int // column i
	var i int
	for i = len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			if ci != 0 {
				break
			}
			ci = i
		}
	}
	if i < 0 {
		return nil, errors.New("invalid location: " + pos)
	}

	loc := SourceLocation{Filename: s[:i]}

	var err error
	if loc.Line, err = strconv.Atoi(s[i+1 : ci]); err != nil {
		return nil, fmt.Errorf("parsing line: %w", err)
	}

	cols := s[ci+1:]
	if j := strings.IndexByte(cols, '-'); j != -1 {
		if loc.ColStart, err = strconv.Atoi(cols[:j]); err != nil {
			return nil, fmt.Errorf("parsing col start: %w", err)
		}
		if loc.ColEnd, err = strconv.Atoi(cols[j+1:]); err != nil {
			return nil, fmt.Errorf("parsing col end: %w", err)
		}
	} else {
		if loc.ColStart, err = strconv.Atoi(cols); err != nil {
			return nil, fmt.Errorf("parsing col start: %w", err)
		}
	}

	return &loc, nil
}

// type ReferencesResponse struct {
// 	Env      map[string]string
// 	Filename string
// 	Pos      int
// }

// better?
// var re = regexp.MustCompile(`(?m)^(?:/.*/)+[-_. [:alnum:]]+\.go:(\d+):(\d+(?:-\d+)?):?\b`)
//
// var re = regexp.MustCompile(`(?m)^(?:/[^/\n]+)+\.go:(\d+):(\d+(?:-\d+)?):?\b`)

func (r *ReferencesRequest) Call() (interface{}, string) {
	// CEV: we probably don't need this
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// TODO: use line/column (note: unicode is handled on the python side)

	cmd := exec.CommandContext(ctx, "gopls", "-remote", "auto", "references", "-d",
		fmt.Sprintf("%s:#%d", r.Filename, r.Offset))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = filepath.Dir(r.Filename)

	// this is dumb fix this
	id := numbers.nextString()
	watchCmd(id, cmd)
	defer unwatchCmd(id)

	var res []*SourceLocation
	if err := cmd.Run(); err != nil {
		return res, fmt.Sprintf("%s: %s", err.Error(), stderr.String())
	}

	var first error
	lines := bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte{'\n'})
	for _, line := range lines {
		loc, err := ParseSourceLocation(string(line))
		if err != nil && first == nil {
			first = err
		} else {
			res = append(res, loc)
		}
	}
	if first != nil {
		return res, first.Error()
	}
	return res, ""
}

func init() {
	registry.Register("references", func(_ *Broker) Caller {
		return &ReferencesRequest{
			Env: map[string]string{},
		}
	})
}
