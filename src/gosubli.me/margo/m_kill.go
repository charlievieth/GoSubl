package main

import (
	"os/exec"
	"sync"
)

var (
	cmdWatchlist = map[string]*exec.Cmd{}
	cmdWatchLck  = sync.Mutex{}
)

type mKill struct {
	Cid string
}

func (m *mKill) Call() (res interface{}, err string) {
	res = M{
		m.Cid: killCmd(m.Cid),
	}
	return
}

func watchCmd(id string, c *exec.Cmd) bool {
	if id == "" {
		return false
	}

	cmdWatchLck.Lock()
	defer cmdWatchLck.Unlock()

	if _, ok := cmdWatchlist[id]; ok {
		return false
	}
	cmdWatchlist[id] = c
	return true
}

func unwatchCmd(id string) bool {
	if id == "" {
		return false
	}

	cmdWatchLck.Lock()
	defer cmdWatchLck.Unlock()

	if _, ok := cmdWatchlist[id]; ok {
		delete(cmdWatchlist, id)
		return true
	}
	return false
}

func killCmd(id string) bool {
	if id == "" {
		return false
	}

	cmdWatchLck.Lock()
	defer cmdWatchLck.Unlock()

	if c, ok := cmdWatchlist[id]; ok {
		// the primary use-case for these functions are remote requests to cancel the proces
		// so we won't remove it from the map
		c.Process.Kill()
		// neither wait nor release are called because the cmd owner should be waiting on it
		return true
	}
	return false
}

func init() {
	byeDefer(func() {
		cmdWatchLck.Lock()
		wg := new(sync.WaitGroup)
		for _, c := range cmdWatchlist {
			if c == nil || c.Process == nil {
				continue
			}
			wg.Add(1)
			go func(c *exec.Cmd) {
				defer wg.Done()
				c.Process.Kill()
				c.Process.Release()
			}(c)
		}
		wg.Wait()
		cmdWatchLck.Unlock()
	})

	registry.Register("kill", func(b *Broker) Caller {
		return &mKill{}
	})
}

/*
// CmdWatch is the global command watch list.
var CmdWatch cmdWatcher

// cmdWatcher stores a list of commands to watch and/or kill
type cmdWatcher struct {
	cmds map[*exec.Cmd]struct{}

	list map[string]*exec.Cmd
	mu   sync.Mutex
}

// newCmdWatcher returns a new cmdWatcher
func newCmdWatcher() *cmdWatcher {
	return &cmdWatcher{list: make(map[string]*exec.Cmd)}
}

func (w *cmdWatcher) Remove(cmd *exec.Cmd) {
	w.mu.Lock()
	delete(w.cmds, cmd)
	w.mu.Unlock()
}

func (w *cmdWatcher) Add(cmd *exec.Cmd) {
	w.mu.Lock()
	if w.cmds == nil {
		w.cmds = make(map[*exec.Cmd]struct{})
	}
	w.cmds[cmd] = struct{}{}
	w.mu.Unlock()
}

func (w *cmdWatcher) Run(cmd *exec.Cmd) error {
	w.Add(cmd)
	defer w.Remove(cmd)
	err := cmd.Run()
	return err
}

// Watch adds a command to the watch list with key id.
func (cmd *cmdWatcher) Watch(id string, c *exec.Cmd) bool {
	if id == "" || c == nil {
		return false
	}
	cmd.mu.Lock()
	if cmd.list == nil {
		cmd.list = make(map[string]*exec.Cmd)
	}
	_, exists := cmd.list[id]
	if !exists {
		cmd.list[id] = c
	}
	cmd.mu.Unlock()
	return !exists
}

// UnWatch removes a command from the watch list.
func (cmd *cmdWatcher) UnWatch(id string) bool {
	_, ok := cmd.remove(id)
	return ok
}

// Kill, kills the watched command with key id.
func (cmd *cmdWatcher) Kill(id string) bool {
	if c, ok := cmd.remove(id); ok {
		// TODO: Release Process as well?
		return c.Process.Kill() == nil
	}
	return false
}

func (cmd *cmdWatcher) remove(id string) (c *exec.Cmd, ok bool) {
	if cmd.list != nil {
		cmd.mu.Lock()
		if c, ok = cmd.list[id]; ok {
			delete(cmd.list, id)
		}
		cmd.mu.Unlock()
	}
	return
}

// KillAll, kills and releases all watched commands.
func (cmd *cmdWatcher) KillAll() {
	cmd.mu.Lock()
	for _, c := range cmd.list {
		if c != nil {
			c.Process.Kill()
			c.Process.Release()
		}
	}
	cmd.mu.Unlock()
}
*/
