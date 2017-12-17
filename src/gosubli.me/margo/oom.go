package main

import (
	"log"
	"runtime"
	"time"
)

func startOomKiller(maxMb int) {
	go func() {
		const MB = 1024 * 1024
		const format = "MarGo: OOM.\n" +
			"Memory limit: %vm\n" +
			"Memory usage: %vm\n" +
			"Number goroutines: %v\n" +
			"------- begin stack trace ----\n" +
			"\n%s\n\n" +
			"-------  end stack trace  ----\n"

		var mst runtime.MemStats
		tick := time.NewTicker(time.Second * 2)

		for range tick.C {
			runtime.ReadMemStats(&mst)
			alloc := int(mst.Sys / MB)
			if alloc >= maxMb {
				buf := make([]byte, 1*MB)
				n := runtime.Stack(buf, true)
				log.Fatalf(format, maxMb, alloc, runtime.NumGoroutine(), buf[:n])
			}
		}
	}()
}
