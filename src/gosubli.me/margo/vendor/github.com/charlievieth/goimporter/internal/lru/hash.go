package lru

import (
	"go/build"
	"hash/crc64"
	"unsafe"
)

var table = crc64.MakeTable(crc64.ISO)

func hash(c *build.Context) uint64 {
	var b []byte
	w := crc64.New(table)

	*(*uintptr)(unsafe.Pointer((uintptr(unsafe.Pointer(&b))))) = *(*uintptr)(unsafe.Pointer(&c.GOARCH))
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 8))) = len(c.GOARCH)
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 16))) = len(c.GOARCH)
	w.Write(b)

	*(*uintptr)(unsafe.Pointer((uintptr(unsafe.Pointer(&b))))) = *(*uintptr)(unsafe.Pointer(&c.GOOS))
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 8))) = len(c.GOOS)
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 16))) = len(c.GOOS)
	w.Write(b)

	*(*uintptr)(unsafe.Pointer((uintptr(unsafe.Pointer(&b))))) = *(*uintptr)(unsafe.Pointer(&c.GOROOT))
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 8))) = len(c.GOROOT)
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 16))) = len(c.GOROOT)
	w.Write(b)

	*(*uintptr)(unsafe.Pointer((uintptr(unsafe.Pointer(&b))))) = *(*uintptr)(unsafe.Pointer(&c.GOPATH))
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 8))) = len(c.GOPATH)
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 16))) = len(c.GOPATH)
	w.Write(b)

	*(*uintptr)(unsafe.Pointer((uintptr(unsafe.Pointer(&b))))) = (uintptr)(unsafe.Pointer(&c.CgoEnabled))
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 8))) = 1
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 16))) = 1
	w.Write(b)

	*(*uintptr)(unsafe.Pointer((uintptr(unsafe.Pointer(&b))))) = (uintptr)(unsafe.Pointer(&c.UseAllFiles))
	w.Write(b)

	for _, s := range c.BuildTags {
		*(*uintptr)(unsafe.Pointer((uintptr(unsafe.Pointer(&b))))) = *(*uintptr)(unsafe.Pointer(&s))
		*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 8))) = len(s)
		*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 16))) = len(s)
		w.Write(b)
	}

	for _, s := range c.ReleaseTags {
		*(*uintptr)(unsafe.Pointer((uintptr(unsafe.Pointer(&b))))) = *(*uintptr)(unsafe.Pointer(&s))
		*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 8))) = len(s)
		*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 16))) = len(s)
		w.Write(b)
	}

	*(*uintptr)(unsafe.Pointer((uintptr(unsafe.Pointer(&b))))) = *(*uintptr)(unsafe.Pointer(&c.InstallSuffix))
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 8))) = len(c.InstallSuffix)
	*(*int)(unsafe.Pointer((uintptr(unsafe.Pointer(&b)) + 16))) = len(c.InstallSuffix)
	w.Write(b)

	return w.Sum64()
}
