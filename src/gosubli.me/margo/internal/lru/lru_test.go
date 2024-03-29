/*
Copyright 2013 Google Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package lru

import (
	"fmt"
	"sync"
	"testing"
)

type simpleStruct struct {
	int
	string
}

type complexStruct struct {
	int
	simpleStruct
}

var getTests = []struct {
	name       string
	keyToAdd   interface{}
	keyToGet   interface{}
	expectedOk bool
}{
	{"string_hit", "myKey", "myKey", true},
	{"string_miss", "myKey", "nonsense", false},
	{"simple_struct_hit", simpleStruct{1, "two"}, simpleStruct{1, "two"}, true},
	{"simple_struct_miss", simpleStruct{1, "two"}, simpleStruct{0, "noway"}, false},
	{"complex_struct_hit", complexStruct{1, simpleStruct{2, "three"}},
		complexStruct{1, simpleStruct{2, "three"}}, true},
}

func TestGet(t *testing.T) {
	for _, tt := range getTests {
		lru := New(0)
		lru.Add(tt.keyToAdd, 1234)
		val, ok := lru.Get(tt.keyToGet)
		if ok != tt.expectedOk {
			t.Fatalf("%s: cache hit = %v; want %v", tt.name, ok, !ok)
		} else if ok && val != 1234 {
			t.Fatalf("%s expected get to return 1234 but got %v", tt.name, val)
		}
	}
}

func TestRemove(t *testing.T) {
	lru := New(0)
	lru.Add("myKey", 1234)
	if val, ok := lru.Get("myKey"); !ok {
		t.Fatal("TestRemove returned no match")
	} else if val != 1234 {
		t.Fatalf("TestRemove failed.  Expected %d, got %v", 1234, val)
	}

	lru.Remove("myKey")
	if _, ok := lru.Get("myKey"); ok {
		t.Fatal("TestRemove returned a removed entry")
	}
}

func TestRemoveOldest(t *testing.T) {
	lru := New(0)
	want := make([]bool, 4)
	keys := make([]string, 4)
	for i := 0; i < len(want); i++ {
		want[i] = true
		keys[i] = fmt.Sprintf("%d", i)
		lru.Add(keys[i], i)
		if _, ok := lru.Get(keys[i]); !ok {
			t.Fatal("TestRemoveOldest returned no match")
		}
	}
	for i := 0; i < len(want); i++ {
		want[i] = false
		lru.RemoveOldest()
		for j, exp := range want {
			_, got := lru.Get(keys[j])
			if got != exp {
				t.Errorf("RemoveOldest(%q) = %t; want: %t", keys[i], got, exp)
			}
		}
	}
	if lru.Len() != 0 {
		t.Errorf("TestRemoveOldest len should be 0 got: %d", lru.Len())
	}
}

func TestClear(t *testing.T) {
	lru := New(0)
	for i := 0; i < 4; i++ {
		lru.Add(fmt.Sprintf("%d", i), i)
	}
	if lru.Len() != 4 {
		t.Errorf("Len = %d; want: %d", lru.Len(), 4)
	}
	lru.Clear()
	if lru.Len() != 0 {
		t.Errorf("Len = %d; want: %d", lru.Len(), 0)
	}
}

func TestEvict(t *testing.T) {
	evictedKeys := make([]Key, 0)
	onEvictedFun := func(key Key, value interface{}) {
		evictedKeys = append(evictedKeys, key)
	}

	lru := New(20)
	lru.OnEvicted = onEvictedFun
	for i := 0; i < 22; i++ {
		lru.Add(fmt.Sprintf("myKey%d", i), 1234)
	}

	if len(evictedKeys) != 2 {
		t.Fatalf("got %d evicted keys; want 2", len(evictedKeys))
	}
	if evictedKeys[0] != Key("myKey0") {
		t.Fatalf("got %v in first evicted key; want %s", evictedKeys[0], "myKey0")
	}
	if evictedKeys[1] != Key("myKey1") {
		t.Fatalf("got %v in second evicted key; want %s", evictedKeys[1], "myKey1")
	}
}

func TestRemoveFunc(t *testing.T) {
	lru := New(0)
	lru.Add("a", 1)
	lru.Add("b", 2)
	lru.RemoveFunc(func(key Key, val interface{}) bool {
		return key.(string) == "b"
	})
	if _, ok := lru.Get("b"); ok {
		t.Error("Failed to remove key: b")
	}
	if _, ok := lru.Get("a"); !ok {
		t.Error("Should not have removed key: a")
	}
}

func TestParallelStress(t *testing.T) {
	const N = 1024
	lru := New(1024)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 128*1024; i++ {
				key := i % N
				switch i % 4 {
				case 0:
					lru.Add(key, i)
				case 1:
					lru.Remove(key)
				case 2:
					lru.RemoveOldest()
				case 3:
					lru.Clear()
				}
			}
		}()
	}
	wg.Wait()
}
