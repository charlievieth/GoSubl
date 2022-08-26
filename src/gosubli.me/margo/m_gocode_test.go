package main

import (
	"strings"
	"testing"
)

type extractCursorLineTest struct {
	in, want string
}

// var extractCursorLineTests []extractCursorLineTest{
// 	{
// 		In: `package main$`,
// 	}
// }

func TestExtractCursorLine(t *testing.T) {
	tests := []string{
		`package main$`,
		`package main
$
func main() {
}`,
		`package main

func main() {
	a := "hello"
	if x := fmt.Sr$; x != "" {

	}
}`,
	}

	getWant := func(in string) string {
		for _, s := range strings.Split(in, "\n") {
			if strings.Contains(s, "$") {
				return s
			}
		}
		t.Fatalf("Failed to find `$` in %q", in)
		return ""
	}
	for _, in := range tests {
		want := getWant(in)
		cursor := strings.Index(in, "$")
		in = strings.ReplaceAll(in, "$", "")
		got := extractCursorLine(in, cursor)
		if got != want {
			t.Errorf("extractCursorLine(%q, %d) = %q; want: %q", in, cursor, got, want)
		}
	}

	const source = `package main

func main() {
	a := "hello"
	if x := fmt.Sprint("%v", a); x != "" {
		return
	}
}
`

	for i := -5; i < len(source)+5; i++ {
		if i < 0 || i > len(source) {
			got := extractCursorLine(source, i)
			if got != "" {
				t.Errorf("extractCursorLine(%q, %d) = %q; want: %q", source, i, got, "")
			}
			continue
		}
		var in string
		if i < len(source) {
			b := []byte(source)
			b[i] = '$'
			in = string(b)
		} else {
			in = source + "$"
		}
		want := getWant(in)
		in = strings.ReplaceAll(in, "$", "")
		got := extractCursorLine(in, i)
		if got != want {
			// t.Logf("## Got:\n%s\n##", got)
			// t.Logf("## Want:\n%s\n##", want)
			t.Errorf("extractCursorLine(%q, %d) = %q; want: %q", in, i, got, want)
		}
	}

	// tests := []struct {
	// 	in, want string
	// }{
	// 	{}
	// }
}
