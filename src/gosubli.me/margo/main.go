package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gosubli.me/margo/internal/logwriter"
)

const DEBUG = false

var (
	numbers = new(counter)

	// TODO (CEV): use a pointer
	sendCh = make(chan Response, 100)

	logger = func() *zap.Logger {
		b := make([]byte, hex.DecodedLen(8))
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		id := hex.EncodeToString(b)

		lvl := zap.InfoLevel
		if DEBUG {
			lvl = zap.DebugLevel
		}

		cfg := zap.NewProductionConfig()
		cfg.Level = zap.NewAtomicLevelAt(lvl)
		cfg.OutputPaths = []string{"async:stderr"}

		cfg.Encoding = "console"
		cfg.EncoderConfig = zap.NewDevelopmentEncoderConfig()

		// enc := zapcore.NewJSONEncoder(cfg.EncoderConfig)
		enc := zapcore.NewConsoleEncoder(cfg.EncoderConfig)
		sink := &logwriter.BufferedWriteSyncer{
			WS: zapcore.AddSync(os.Stderr),
		}
		ll := zap.New(
			zapcore.NewCore(enc, sink, cfg.Level),
			zap.AddCaller(), zap.AddStacktrace(zap.FatalLevel),
		)
		return ll.Named("margo_" + id)
	}()
)

type counter uint64

func (c *counter) next() uint64 {
	return atomic.AddUint64((*uint64)(c), 1)
}

func (c *counter) val() uint64 {
	return atomic.LoadUint64((*uint64)(c))
}

func (c *counter) nextString() string {
	return strconv.FormatUint(c.next(), 10)
}

var byeFuncs struct {
	sync.Mutex
	fns []func()
}

func byeDefer(fn func()) {
	if fn != nil {
		byeFuncs.Lock()
		byeFuncs.fns = append(byeFuncs.fns, fn)
		byeFuncs.Unlock()
	}
}

func main() {
	do := "-"
	poll := 0
	wait := false
	dump_env := false
	maxMemDefault := 1000
	maxMem := 0
	tag := ""
	flags := flag.NewFlagSet("MarGo", flag.ExitOnError)
	flags.BoolVar(&dump_env, "env", dump_env, "if true, dump all environment variables as a json map to stdout and exit")
	flags.BoolVar(&wait, "wait", wait, "Whether or not to wait for outstanding requests (which may be hanging forever) when exiting")
	flags.IntVar(&poll, "poll", poll, "If N is greater than zero, send a response every N seconds. The token will be `margo.poll`")
	flags.StringVar(&do, "do", "-", "Process the specified operations(lines) and exit. `-` means operate as normal (`-do` implies `-wait=true`)")
	flags.StringVar(&tag, "tag", tag, "Requests will include a member `tag' with this value")
	flags.IntVar(&maxMem, "oom", maxMemDefault, "The maximum amount of memory MarGo is allowed to use. If memory use reaches this value, MarGo dies :'(")
	pprofAddr := flags.String("pprof-addr", "", "HTTP address for pprof")
	flags.Parse(os.Args[1:])

	byeDefer(func() { logger.Sync() })
	defer func() {
		logger.Warn("margo exiting")
		logger.Sync()
	}()
	logger.Warn("margo starting")

	if *pprofAddr != "" {
		go func() {
			if err := http.ListenAndServe(*pprofAddr, nil); err != nil {
				logger.Error("failed to start pprof server", zap.Error(err))
			}
		}()
	}

	if maxMem <= 0 {
		maxMem = maxMemDefault
	}
	// startOomKiller(maxMem)

	if dump_env {
		m := defaultEnv()
		for _, s := range os.Environ() {
			p := strings.SplitN(s, "=", 2)
			if len(p) == 2 {
				m[p[0]] = p[1]
			} else {
				m[p[0]] = ""
			}
		}
		json.NewEncoder(os.Stdout).Encode(m)
		return
	}

	var in io.Reader = os.Stdin
	doCall := do != "-"
	if doCall {
		b64 := "base64:"
		if strings.HasPrefix(do, b64) {
			s, _ := base64.StdEncoding.DecodeString(do[len(b64):])
			in = bytes.NewReader(s)
		} else {
			in = strings.NewReader(do)
		}
	}

	broker := NewBroker(logger, in, os.Stdout, tag)
	if poll > 0 {
		pollSeconds := time.Second * time.Duration(poll)
		pollCounter := new(counter)
		go func() {
			for {
				time.Sleep(pollSeconds)
				broker.SendNoLog(Response{
					Token: "margo.poll",
					Data: M{
						"time": time.Now().String(),
						"seq":  pollCounter.nextString(),
					},
				})
			}
		}()
	}

	go func() {
		for r := range sendCh {
			broker.SendNoLog(r)
		}
	}()

	// broker.Loop(!doCall, (wait || doCall))
	broker.LoopBytes(!doCall, (wait || doCall))

	byeFuncs.Lock()
	wg := new(sync.WaitGroup)
	for _, fn := range byeFuncs.fns {
		wg.Add(1)
		go func(fn func()) {
			defer wg.Done()
			defer func() {
				if e := recover(); e != nil {
					logger.Error("panic: bye funcs", zap.Any("panic", e))
				}
			}()
			fn()
		}(fn)
	}
	wg.Wait()
	byeFuncs.Unlock()
}
