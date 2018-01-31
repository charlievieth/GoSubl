package cache

import (
	"io"
	"io/ioutil"
	"os"
	"testing"
)

func writeTmpFile(data []byte) (string, error) {
	f, err := ioutil.TempFile("", "cache-test-")
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	f.Close()
	return f.Name(), err
}

func readCachedFile(c *File, path string) ([]byte, error) {
	rc, err := c.OpenFile(path)
	if err != nil {
		return nil, err
	}
	b, err := ioutil.ReadAll(rc)
	rc.Close()
	return b, err
}

func TestFile(t *testing.T) {
	const data = "Hello, World!"
	path, err := writeTmpFile([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	var c File
	b, err := readCachedFile(&c, path)
	if string(b) != data {
		t.Fatalf("file: got: %s want: %s", string(b), data)
	}

	modifiedData := []string{
		"Hello, World!",
		"HELLO, WORLD!",
		"hello, world!",
		"Welp - hope this worked",
	}
	for _, mod := range modifiedData {
		if err := ioutil.WriteFile(path, []byte(mod), 0600); err != nil {
			t.Fatal(err)
		}
		b, err := readCachedFile(&c, path)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != mod {
			t.Fatalf("file: got: %s want: %s", string(b), mod)
		}
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenFile(path); err == nil {
		t.Error("file: expected an error got: nil")
	}
}

func BenchmarkFile_Cache(b *testing.B) {
	const data = "Hello, World!"
	path, err := writeTmpFile([]byte(data))
	if err != nil {
		b.Fatal(err)
	}
	defer os.Remove(path)
	b.ResetTimer()

	var c File
	b.RunParallel(func(pb *testing.PB) {
		buf := make([]byte, 256)
		for pb.Next() {
			rc, err := c.OpenFile(path)
			if err != nil {
				b.Fatal(err)
			}
			_, err = rc.Read(buf)
			rc.Close()
			if err != nil && err != io.EOF {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkFile_Base(b *testing.B) {
	const data = "Hello, World!"
	path, err := writeTmpFile([]byte(data))
	if err != nil {
		b.Fatal(err)
	}
	defer os.Remove(path)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		buf := make([]byte, 256)
		for pb.Next() {
			f, err := os.Open(path)
			if err != nil {
				b.Fatal(err)
			}
			_, err = f.Read(buf)
			f.Close()
			if err != nil && err != io.EOF {
				b.Fatal(err)
			}
		}
	})
}
