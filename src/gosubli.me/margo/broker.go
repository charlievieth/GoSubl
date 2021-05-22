package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
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

	tag    string
	served counter
	start  time.Time
	r      io.Reader
	w      io.Writer
	in     *bufio.Reader
	out    *bufio.Writer // WARN (CEV): not used
	// out    *json.Encoder // WARN (CEV): not used
	bufPool sync.Pool
}

func NewBroker(r io.Reader, w io.Writer, tag string) *Broker {
	w = DebugWriter(w)
	return &Broker{
		tag: tag,
		r:   r,
		w:   w,
		in:  bufio.NewReaderSize(r, 1024*1024),

		// WARN: maybe this is the problem
		out: bufio.NewWriterSize(w, 8*1024*1024), // 8 MB
		// out: json.NewEncoder(w), // WARN (CEV): not used
		bufPool: sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
	}
}

func (b *Broker) Send(resp Response) error {
	err := b.SendNoLog(resp)
	if err != nil {
		logger.Println("Cannot send result", err)
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
		logger.Println("write error:", err)
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

	buf.Reset()
	b.bufPool.Put(buf)

	// b.w.Write(s)
	// b.w.Write([]byte{'\n'})
	return nil
}

func (b *Broker) recover(method, token string) {
	if err := recover(); err != nil {
		buf := make([]byte, 64*1024*1024)
		n := runtime.Stack(buf, true)
		logger.Printf("%v#%v PANIC: %v\n%s\n\n", method, token, err, buf[:n])
		b.Send(Response{
			Token: token,
			Error: "broker: " + method + "#" + token + " PANIC",
		})
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
		return errors.New("broker: invald method: " + req.Method)
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

		var req Request
		if err := json.Unmarshal(p, &req); err != nil {
			logger.Printf("broker: error decoding JSON: %s\n", err)
			continue
		}

		if req.Method == "" {
			logger.Println("broker: missing method name")
			if req.Token != "" {
				b.Send(Response{
					Token: req.Token,
					Error: "missing method name",
				})
			}
			continue
		}

		if err := b.handleRequest(&req); err != nil {
			logger.Printf("broker: processing request (%q): %s\n", req.Method, err)
			b.Send(Response{
				Token: req.Token,
				Error: err.Error(),
			})
		}
	}
}

func (b *Broker) acceptBytes(lineCh chan []byte) (stopLooping bool) {
	line, err := b.in.ReadBytes('\n')
	if err != nil {
		// WARN: we need should stop looping here
		if err != io.EOF {
			logger.Println("Cannot read input: ", err)
			b.Send(Response{Error: err.Error()})
			return false
		}
		stopLooping = true
	}
	if len(line) > 0 {
		lineCh <- line
	}
	WriteInput(line)

	return stopLooping
}

func (b *Broker) accept(jobsCh chan *Job) (stopLooping bool) {
	line, err := b.in.ReadBytes('\n')
	if err != nil {
		if err != io.EOF {
			logger.Println("Cannot read input", err)
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
		logger.Println("Cannot unmarshal JSPON", err)
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
		e := "Invalid method " + req.Method
		logger.Println(e)
		b.Send(Response{
			Token: req.Token,
			Error: e,
		})
		return stopLooping
	}

	cl := m(b)
	if err := json.Unmarshal(req.Body, cl); err != nil {
		logger.Println("Cannot decode arg", err)
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

func (b *Broker) accept_OLD(jobsCh chan<- *Job) (stopLooping bool) {
	line, err := b.in.ReadBytes('\n')

	// WARN
	WriteInput(line)

	if err == io.EOF {
		stopLooping = true
	} else if err != nil {
		logger.Println("Cannot read input", err)
		b.Send(Response{
			Error: err.Error(),
		})
		return
	}

	req := &Request{}
	dec := json.NewDecoder(bytes.NewReader(line))
	// if this fails, we are unable to return a useful error(no token to send it to)
	// so we'll simply/implicitly drop the request since it has no method
	// we can safely assume that all such cases will be empty lines and not an actual request
	dec.Decode(&req)

	if req.Method == "" {
		return
	}

	// WARN: this appears to no be used
	if req.Method == "bye-ni" {
		return true
	}

	m := registry.Lookup(req.Method)
	if m == nil {
		e := "Invalid method " + req.Method
		logger.Println(e)
		b.Send(Response{
			Token: req.Token,
			Error: e,
		})
		return
	}

	cl := m(b)
	err = dec.Decode(cl)
	if err != nil {
		logger.Println("Cannot decode arg", err)
		b.Send(Response{
			Token: req.Token,
			Error: err.Error(),
		})
		return
	}

	jobsCh <- &Job{
		Method: req.Method,
		Token:  req.Token,
		// Req:    req,
		Caller: cl,
	}

	return
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
		logger.Fatalf("error: failed to find parent process (%d): %s", ppid, err)
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		logger.Fatalf("error: signalling parent process (%d): %s", ppid, err)
	}

	for {
		if b.acceptBytes(lineCh) {
			// If acceptBytes() returns true check if our parent process died.
			if proc.Signal(syscall.Signal(0)) != nil {
				logger.Println("exiting: parent process died")
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
