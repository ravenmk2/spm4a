package proc

import (
	"errors"
	"os/exec"
)

type Proc struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	tree treeHandle
}

func Start(cmd *exec.Cmd) (*Proc, error) {
	setStartAttrs(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Proc{cmd: cmd, done: make(chan struct{}), tree: newTreeHandle(cmd)}
	go func() {
		p.err = cmd.Wait()
		p.tree.close()
		close(p.done)
	}()
	return p, nil
}

func (p *Proc) PID() int { return p.cmd.Process.Pid }

func (p *Proc) Done() <-chan struct{} { return p.done }

// Err blocks until the process exits and returns its wait error.
func (p *Proc) Err() error {
	<-p.done
	return p.err
}

func (p *Proc) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// Terminate gracefully signals the whole process tree (SIGTERM to the group
// on Unix; on Windows there is no graceful signal, so this already hard-kills
// the job object tree — graceful stop goes through actuator /shutdown).
func (p *Proc) Terminate() error {
	if p.Exited() {
		return nil
	}
	return p.tree.terminate(p.PID())
}

// Kill force-kills the whole process tree.
func (p *Proc) Kill() error {
	if p.Exited() {
		return nil
	}
	p.tree.kill(p.PID())
	return nil
}

// TerminatePID / KillPID act on a process we only know by PID (adopted after
// a daemon restart). Unix: signal the process group (pgid == pid). Windows:
// TerminateProcess on the root only.
func TerminatePID(pid int) error {
	if !Alive(pid) {
		return nil
	}
	return terminatePID(pid)
}

func KillPID(pid int) error {
	if !Alive(pid) {
		return nil
	}
	killPID(pid)
	return nil
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
