package main

import (
	"io/ioutil"
	"strings"
	"testing"
)

func TestReadersEqual(t *testing.T) {
	type TestCase struct {
		s1, s2 string
	}
	large := strings.Repeat("A", 64*1024+1)
	tests := []TestCase{
		{"", ""},
		{"a", ""},
		{"a", "a"},
		{"a", "ab"},
		{large, large},
		{large, large[:len(large)-1] + "b"},
		{large, "b" + large},
	}
	for i, x := range tests {
		exp := x.s1 == x.s2
		got := ReadersEqual(strings.NewReader(x.s1), strings.NewReader(x.s2))
		if got != exp {
			t.Errorf("%d %+v: got: %t want: %t", i, x, got, exp)
		}
	}
}

func TestFindRequest_FileModified(t *testing.T) {
	data, err := ioutil.ReadFile("m_doc_test.go")
	if err != nil {
		t.Fatal(err)
	}
	changedByte := append([]byte(nil), data...)
	_ = changedByte
}
