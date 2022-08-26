package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

type EmptyResponse struct{}

type M map[string]interface{}

type Request struct {
	Method string          `json:"method"`
	Token  string          `json:"token"`
	Body   json.RawMessage `json:"body"`
}

// type RequestX struct {
// 	Method string          `json:"method"`
// 	Token  string          `json:"token"`
// 	Body   json.RawMessage `json:"body"`
// }

type ErrorResponse struct {
	Error string `json:"error"`
}

type Response struct {
	Token string      `json:"token"`
	Error string      `json:"error"`
	Tag   string      `json:"tag"`
	Data  interface{} `json:"data"`
}

type Job struct {
	Method string
	Token  string
	Caller Caller
}

type Broker struct {
	sync.Mutex

	tag     string
	served  counter
	start   time.Time
	r       io.Reader
	w       io.Writer
	in      *bufio.Reader
	out     *bufio.Writer // WARN (CEV): not used
	bufPool sync.Pool
	log     *zap.Logger
}

func NewBroker(log *zap.Logger, r io.Reader, w io.Writer, tag string) *Broker {
	return &Broker{
		tag: tag,
		r:   r,
		w:   w,
		in:  bufio.NewReaderSize(r, 1024*1024),

		// WARN: maybe this is the problem
		out: bufio.NewWriterSize(w, 8*1024*1024), // 8 MB
		bufPool: sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
		log: log.With(zap.Namespace("broker")),
	}
}

func (b *Broker) Send(resp Response) error {
	err := b.SendNoLog(resp)
	if err != nil {
		b.log.Error("cannot send result", zap.Error(err))
	}
	return err
}

func (b *Broker) SendNoLog(resp Response) error {
	if resp.Data == nil {
		resp.Data = EmptyResponse{}
	}

	if resp.Tag == "" {
		resp.Tag = b.tag
	}
	if resp.Error != "" {
		b.log.Warn("broker: response error", zap.String("token", resp.Token),
			zap.String("error", resp.Error))
	}

	s, err := json.Marshal(resp)
	if err != nil {
		// if there is a token, it means the client is waiting for a response
		// so respond with the json error. cause of json encode failure includes: non-utf8 string
		if resp.Token == "" {
			return err
		}

		errResp := ErrorResponse{
			Error: "margo broker: cannot encode json response: " + err.Error(),
		}
		if s, err = json.Marshal(&errResp); err != nil {
			return err
		}
	}

	buf := b.bufPool.Get().(*bytes.Buffer)
	buf.Grow(len(s) + 1)
	buf.Write(s)
	buf.WriteByte('\n')

	// TODO: use this error to indicate the client died !!!
	//
	// the only expected write failure are due to broken pipes
	// which usually means the client has gone away so just ignore the error
	b.Lock()
	if _, err := buf.WriteTo(b.w); err != nil {
		b.log.Error("writing response", zap.Error(err))
	}

	// if _, err := b.w.Write(append(s, '\n')); err != nil {
	// 	logger.Println("write error:", err)
	// }

	// if _, err := b.w.Write(s); err != nil {
	// 	logger.Println("write error:", err)
	// }
	// // if _, err := b.w.Write([]byte{'\n'}); err != nil {
	// if _, err := b.w.Write([]byte("\r\n")); err != nil {
	// 	logger.Println("write error:", err)
	// }

	// b.out.Write(s)
	// b.out.WriteByte('\n')
	// b.out.Flush()
	b.Unlock()

	if buf.Cap() < 1024*1024 {
		buf.Reset()
		b.bufPool.Put(buf)
	}

	// b.w.Write(s)
	// b.w.Write([]byte{'\n'})
	return nil
}

func (b *Broker) recover(method, token string) {
	if err := recover(); err != nil {
		buf := make([]byte, 64*1024*1024)
		n := runtime.Stack(buf, true)
		b.log.Error("recovered panic", zap.String("method", method),
			zap.String("token", token), zap.Any("panic", err),
			zap.ByteString("stacktrace", buf[:n]),
		)
	}
}

func (b *Broker) call(method, token string, caller Caller) {
	b.served.next()

	defer b.recover(method, token)

	res, err := caller.Call()
	// TODO: this can be removed
	if res == nil {
		res = EmptyResponse{}
	} else if v, ok := res.(M); ok && v == nil {
		res = EmptyResponse{}
	}

	b.Send(Response{
		Token: token,
		Error: err,
		Data:  res,
	})
}

func (b *Broker) handleRequest(req *Request) error {
	b.served.next()
	defer b.recover(req.Method, req.Token)

	m := registry.Lookup(req.Method)
	if m == nil {
		return fmt.Errorf("broker: invald method: %q: allowed methods: %q",
			req.Method, registry.Methods())
	}
	cl := m(b)

	if err := json.Unmarshal(req.Body, cl); err != nil {
		return fmt.Errorf("broker: cannot unmarshal request (%q): %w",
			req.Method, err)
	}

	res, err := cl.Call()
	// TODO: this can be removed
	if res == nil {
		res = EmptyResponse{}
	} else if v, ok := res.(M); ok && v == nil {
		res = EmptyResponse{}
	}
	return b.Send(Response{
		Token: req.Token,
		Error: err,
		Data:  res,
	})
}

func (b *Broker) workerBytes(wg *sync.WaitGroup, inputCh <-chan []byte) {
	defer wg.Done()

	for p := range inputCh {
		if len(p) == 0 {
			continue
		}

		start := time.Now()
		var req Request
		if err := json.Unmarshal(p, &req); err != nil {
			b.log.Error("request: decoding JSON", zap.Error(err))
			continue
		}
		b.log.Debug("request: unmarshal time", zap.String("method", req.Method),
			zap.String("token", req.Token), zap.Duration("duration", time.Since(start)))

		if req.Method == "" {
			b.log.Warn("request: missing method name", zap.String("token", req.Token))
			if req.Token != "" {
				b.Send(Response{
					Token: req.Token,
					Error: "missing method name",
				})
			}
			continue
		}

		err := b.handleRequest(&req)
		if err != nil {
			b.log.Error("request: handle error", zap.String("method", req.Method),
				zap.String("token", req.Token), zap.Error(err))
			b.Send(Response{
				Token: req.Token,
				Error: err.Error(),
			})
		} else {
			dur := time.Since(start)
			b.log.Debug("request: total time", zap.String("method", req.Method),
				zap.String("token", req.Token), zap.Duration("duration", dur))
		}
	}
}

func (b *Broker) acceptBytes(lineCh chan []byte) (stopLooping bool) {
	line, err := b.in.ReadBytes('\n')
	if err != nil {
		// WARN: we need should stop looping here
		if err != io.EOF {
			b.log.Error("accept bytes: cannot read input", zap.Error(err))
			b.Send(Response{Error: err.Error()})
			return false
		}
		stopLooping = true
	}
	if len(line) > 0 {
		lineCh <- line
	}
	return stopLooping
}

func (b *Broker) accept(jobsCh chan *Job) (stopLooping bool) {
	line, err := b.in.ReadBytes('\n')
	if err != nil {
		if err != io.EOF {
			b.log.Error("accept: cannot read input", zap.Error(err))
			b.Send(Response{
				Error: err.Error(),
			})
			return
		}
		stopLooping = true
	}

	req := &Request{}
	if err := json.Unmarshal(line, req); err != nil {
		// Handle
		b.log.Error("accept: cannot unmarshal JSON", zap.Error(err))
		b.Send(Response{
			Error: err.Error(),
		})
		return
	}
	if req.Method == "" {
		return stopLooping
	}
	// WARN: this appears to no be used
	if req.Method == "bye-ni" {
		return true
	}

	m := registry.Lookup(req.Method)
	if m == nil {
		b.log.Error("accept: invald method", zap.String("method", req.Method))
		b.Send(Response{
			Token: req.Token,
			Error: "Invalid method " + req.Method,
		})
		return stopLooping
	}

	cl := m(b)
	if err := json.Unmarshal(req.Body, cl); err != nil {
		b.log.Error("accept: cannot unmarshal JSON", zap.String("method", req.Method),
			zap.String("token", req.Token), zap.Error(err))
		b.Send(Response{
			Token: req.Token,
			Error: err.Error(),
		})
		return stopLooping
	}

	jobsCh <- &Job{
		Method: req.Method,
		Token:  req.Token,
		Caller: cl,
	}

	return stopLooping
}

func (b *Broker) worker(wg *sync.WaitGroup, jobsCh chan *Job) {
	defer wg.Done()
	for job := range jobsCh {
		if job != nil {
			b.call(job.Method, job.Token, job.Caller)
		}
	}
}

func (b *Broker) LoopBytes(decorate bool, wait bool) {
	b.start = time.Now()

	if decorate {
		go b.SendNoLog(Response{
			Token: "margo.hello",
			Data: M{
				"time": b.start.String(),
			},
		})
	}

	const workers = 20
	wg := &sync.WaitGroup{}

	lineCh := make(chan []byte, 1024)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go b.workerBytes(wg, lineCh)
	}

	ppid := os.Getppid()
	proc, err := os.FindProcess(ppid)
	if err != nil {
		b.log.Error("error: failed to find parent process",
			zap.Int("ppid", ppid), zap.Error(err))
	}
	if runtime.GOOS != "windows" {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			b.log.Error("error: signalling parent process",
				zap.Int("ppid", ppid), zap.Error(err))
		}
	}

	for {
		if b.acceptBytes(lineCh) {
			// If acceptBytes() returns true check if our parent process died.
			if proc.Signal(syscall.Signal(0)) != nil {
				b.log.Warn("exiting: parent process died", zap.Int("ppid", proc.Pid))
				break
			}
			// short break to prevent a hot loop
			time.Sleep(time.Millisecond * 5)
		}
	}

	close(lineCh)
	if wait {
		wg.Wait()
	}

	if decorate {
		b.SendNoLog(Response{
			Token: "margo.bye-ni",
			Data: M{
				"served": b.served.val(),
				"uptime": time.Now().Sub(b.start).String(),
			},
		})
	}
}

func (b *Broker) Loop(decorate bool, wait bool) {
	b.start = time.Now()

	if decorate {
		go b.SendNoLog(Response{
			Token: "margo.hello",
			Data: M{
				"time": b.start.String(),
			},
		})
	}

	const workers = 20
	wg := &sync.WaitGroup{}

	jobsCh := make(chan *Job, 1000)
	for i := 0; i < workers; i += 1 {
		wg.Add(1)
		go b.worker(wg, jobsCh)
	}

	////////////////////////////////////////////
	//                                        //
	// TODO: use a separate loop to read from //
	// STDIN then pass the lines to a pool of //
	// workers for parsing.                   //
	//                                        //
	////////////////////////////////////////////

	// WARN
	numCPU := runtime.NumCPU()
	for {
		stopLooping := b.accept(jobsCh)
		if stopLooping {
			break
		}
		// WARN
		if numCPU <= 4 {
			runtime.Gosched()
		}
	}
	close(jobsCh)

	if wait {
		wg.Wait()
	}

	if decorate {
		b.SendNoLog(Response{
			Token: "margo.bye-ni",
			Data: M{
				"served": b.served.val(),
				"uptime": time.Now().Sub(b.start).String(),
			},
		})
	}
}

/*
func (b *Broker) handleBytes(line []byte) error {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return err // WARN: log
	}
	if req.Method == "" {
		return errors.New("broker: empty request method") // WARN: log
	}

	b.served.next()
	defer b.recover(req.Method, req.Token)

	m := registry.Lookup(req.Method)
	if m == nil {
		// e := "Invalid method " + req.Method
		// logger.Println(e)
		// b.Send(Response{
		// 	Token: req.Token,
		// 	Error: e,
		// })
		return errors.New("broker: invald method: " + req.Method)
	}

	cl := m(b)
	if err := json.Unmarshal(req.Body, cl); err != nil {
		// logger.Println("Cannot decode arg", err)
		// b.Send(Response{
		// 	Token: req.Token,
		// 	Error: err.Error(),
		// })
		return err
	}

	res, err := cl.Call()
	// TODO: this can be removed
	if res == nil {
		res = EmptyResponse{}
	} else if v, ok := res.(M); ok && v == nil {
		res = EmptyResponse{}
	}
	b.Send(Response{
		Token: req.Token,
		Error: err,
		Data:  res,
	})

	return nil
}
*/
