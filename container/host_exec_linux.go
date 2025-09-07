package container

import (
	"context"
	"fmt"
	"time"

	"github.com/tobiichi3227/go-sandbox/pkg/rlimit"
	"github.com/tobiichi3227/go-sandbox/pkg/seccomp"
	"github.com/tobiichi3227/go-sandbox/pkg/unixsocket"
	"github.com/tobiichi3227/go-sandbox/runner"
)

// ExecveParam is parameters to run process inside container
type ExecveParam struct {
	// Args holds command line arguments
	Args []string

	// Env specifies the environment of the process
	Env []string

	// Files specifies file descriptors for the child process
	Files []uintptr

	// ExecFile specifies file descriptor for executable file using fexecve
	ExecFile uintptr

	// CgroupFD specifies file descriptor for cgroup V2
	CgroupFD uintptr

	// RLimits specifies POSIX Resource limit through setrlimit
	RLimits []rlimit.RLimit

	// Seccomp specifies seccomp filter
	Seccomp seccomp.Filter

	// CTTY specifies whether to set controlling TTY
	CTTY bool

	// SyncFunc calls with pid just before execve (for attach the process to cgroups)
	SyncFunc func(pid int) error

	// SyncAfterExec makes syncFunc sync after the start of the execution
	// Thus, since pid is not guarantee to be exist (may exit early), it is not passed
	SyncAfterExec bool
}

// Execve runs process inside container. It accepts context cancellation as time limit exceeded.
func (c *container) Execve(ctx context.Context, param ExecveParam) runner.Result {
	c.mu.Lock()
	defer c.mu.Unlock()

	sTime := time.Now()

	// if execve with fd, put fd at the first parameter
	var files []int
	if param.ExecFile > 0 {
		files = append(files, int(param.ExecFile))
	}
	if param.CgroupFD > 0 {
		files = append(files, int(param.CgroupFD))
	}
	files = append(files, uintptrSliceToInt(param.Files)...)
	msg := unixsocket.Msg{
		Fds: files,
	}
	execCmd := &execCmd{
		Argv:      param.Args,
		Env:       param.Env,
		RLimits:   param.RLimits,
		Seccomp:   param.Seccomp,
		FdExec:    param.ExecFile > 0,
		CTTY:      param.CTTY,
		SyncAfter: param.SyncAfterExec,
		FdCgroup:  param.CgroupFD > 0,
	}
	cm := cmd{
		Cmd:     cmdExecve,
		ExecCmd: execCmd,
	}
	if err := c.sendCmd(cm, msg); err != nil {
		return errResult("execve: sendCmd %v", err)
	}
	// sync function
	rep, msg, err := c.recvReply()
	if err != nil {
		return errResult("execve: recvReply %v", err)
	}
	// if sync function did not involved
	if rep.Error != nil {
		return errResult("execve: %v", rep.Error)
	}
	// if pid not received
	if msg.Cred == nil {
		// tell kill function to exit and sync
		c.execveSyncKill()
		// tell err exec function to exit and sync
		c.execveSyncKill()
		return errResult("execve: no pid received")
	}
	if param.SyncFunc != nil {
		if err := param.SyncFunc(int(msg.Cred.Pid)); err != nil {
			// tell sync function to exit and recv error
			c.execveSyncKill()
			return errResult("execve: syncfunc failed %v", err)
		}
	}
	// send to syncFunc ack ok
	if err := c.sendCmd(cmd{Cmd: cmdOk}, unixsocket.Msg{}); err != nil {
		return errResult("execve: ack failed %v", err)
	}

	// wait for done
	return c.waitForDone(ctx, sTime)
}

func (c *container) waitForDone(ctx context.Context, sTime time.Time) runner.Result {
	mTime := time.Now()
	select {
	case <-c.done: // socket error
		return convertReplyResult(reply{}, sTime, mTime, c.err)

	case <-ctx.Done(): // cancel
		c.sendCmd(cmd{Cmd: cmdKill}, unixsocket.Msg{}) // kill
		reply, _, err := c.recvReply()
		return convertReplyResult(reply, sTime, mTime, err)

	case ret := <-c.recvCh: // result
		err := c.sendCmd(cmd{Cmd: cmdKill}, unixsocket.Msg{}) // kill
		return convertReplyResult(ret.Reply, sTime, mTime, err)
	}
}

func convertReplyResult(reply reply, sTime, mTime time.Time, err error) runner.Result {
	// handle potential error
	if err != nil {
		return runner.Result{
			Status: runner.StatusRunnerError,
			Error:  err.Error(),
		}
	}
	if reply.Error != nil {
		return runner.Result{
			Status: runner.StatusRunnerError,
			Error:  reply.Error.Error(),
		}
	}
	if reply.ExecReply == nil {
		return runner.Result{
			Status: runner.StatusRunnerError,
			Error:  "execve: no reply received",
		}
	}
	// emit result after all communication finish
	return runner.Result{
		Status:      reply.ExecReply.Status,
		ExitStatus:  reply.ExecReply.ExitStatus,
		Time:        reply.ExecReply.Time,
		Memory:      reply.ExecReply.Memory,
		SetUpTime:   mTime.Sub(sTime),
		RunningTime: time.Since(mTime),
	}
}

// execveSyncKill will send kill and recv reply
func (c *container) execveSyncKill() {
	c.sendCmd(cmd{Cmd: cmdKill}, unixsocket.Msg{})
	c.recvReply()
}

func errResult(f string, v ...interface{}) runner.Result {
	return runner.Result{
		Status: runner.StatusRunnerError,
		Error:  fmt.Sprintf(f, v...),
	}
}
