package container

import (
	"fmt"
	"syscall"
	"time"

	"github.com/tobiichi3227/go-sandbox/pkg/forkexec"
	"github.com/tobiichi3227/go-sandbox/pkg/unixsocket"
	"github.com/tobiichi3227/go-sandbox/runner"
)

func (c *containerServer) handleExecve(cmd *execCmd, msg unixsocket.Msg) error {
	var (
		files    []uintptr
		execFile uintptr
		cgroupFd uintptr
		cred     *syscall.Credential
	)
	if cmd == nil {
		return c.sendErrorReply("handle: no parameter provided")
	}
	if len(msg.Fds) > 0 {
		files = intSliceToUintptr(msg.Fds)
		// don't leak fds to child
		closeOnExecFds(msg.Fds)
		// release files after execve
		defer closeFds(msg.Fds)
	}

	// if fexecve, then the first fd must be executable
	if cmd.FdExec {
		if len(files) == 0 {
			return c.sendErrorReply("handle: expected fexecve fd")
		}
		execFile = files[0]
		files = files[1:]
	}
	// if cgroupFd, then the cgroupFd follows
	if cmd.FdCgroup {
		if len(files) == 0 {
			return c.sendErrorReply("handle: expected cgroup fd")
		}
		cgroupFd = files[0]
		files = files[1:]
	}

	var env []string
	env = append(env, c.defaultEnv...)
	env = append(env, cmd.Env...)

	if len(cmd.Argv) > 0 {
		exePath, err := lookPath(cmd.Argv[0], env)
		if err != nil {
			return c.sendErrorReply("handle: %s: %v", cmd.Argv[0], err)
		}
		cmd.Argv[0] = exePath
	}

	syncPid := func(pid int) error {
		msg := unixsocket.Msg{
			Cred: &syscall.Ucred{
				Pid: int32(pid),
				Uid: uint32(syscall.Getuid()),
				Gid: uint32(syscall.Getgid()),
			},
		}
		if err := c.sendReply(reply{}, msg); err != nil {
			return fmt.Errorf("sync func: send reply: %w", err)
		}
		cmd, _, err := c.recvCmd()
		if err != nil {
			return fmt.Errorf("sync func: recv cmd: %w", err)
		}
		if cmd.Cmd == cmdKill {
			return fmt.Errorf("sync func: received kill")
		}
		return nil
	}
	var syncFunc func(pid int) error
	if !cmd.SyncAfter {
		syncFunc = syncPid
	}

	if c.Cred {
		cred = &syscall.Credential{
			Uid:         uint32(c.ContainerUID),
			Gid:         uint32(c.ContainerGID),
			NoSetGroups: true,
		}
	}

	var seccomp *syscall.SockFprog
	if cmd.Seccomp != nil {
		seccomp = cmd.Seccomp.SockFprog()
	}

	r := forkexec.Runner{
		Args:       cmd.Argv,
		Env:        env,
		ExecFile:   execFile,
		RLimits:    cmd.RLimits,
		Files:      files,
		WorkDir:    c.WorkDir,
		NoNewPrivs: true,
		DropCaps:   true,
		SyncFunc:   syncFunc,
		Credential: cred,
		CTTY:       cmd.CTTY,
		Seccomp:    seccomp,
		CgroupFd:   cgroupFd,

		UnshareCgroupAfterSync: c.UnshareCgroup,
	}
	// starts the runner, error is handled same as wait4 to make communication equal
	pid, err := r.Start()
	if err != nil {
		s := "<nil>"
		if len(cmd.Argv) > 0 {
			s = cmd.Argv[0]
		}
		return c.sendErrorReply("start: %s: %v", s, err)
	}
	if cmd.SyncAfter {
		if err := syncPid(1); err != nil {
			syscall.Kill(-1, syscall.SIGKILL)

			c.waitPid <- pid
			ret := <-c.waitPidResult
			err := c.sendReply(convertReply(ret), unixsocket.Msg{})

			c.waitAll <- struct{}{}
			<-c.waitAllDone
			return err
		}
	}
	return c.handleExecveStarted(pid)
}

func (c *containerServer) handleExecveStarted(pid int) error {
	// At this point, either recv kill / send result would be happened
	// host -> container: kill
	// container -> host: result
	// container -> host: done

	// Let's register a wait event
	c.waitPid <- pid

	var ret waitPidResult
	select {
	case <-c.done: // socket error happened
		return c.err

	case <-c.recvCh: // kill cmd received
		syscall.Kill(-1, syscall.SIGKILL)
		ret = <-c.waitPidResult
		c.waitAll <- struct{}{}

		if err := c.sendReply(convertReply(ret), unixsocket.Msg{}); err != nil {
			return err
		}

	case ret = <-c.waitPidResult: // child process returned
		syscall.Kill(-1, syscall.SIGKILL)
		c.waitAll <- struct{}{}

		if err := c.sendReply(convertReply(ret), unixsocket.Msg{}); err != nil {
			return err
		}
		if _, _, err := c.recvCmd(); err != nil { // kill cmd received
			return err
		}
	}
	<-c.waitAllDone
	return nil
}

func convertReply(ret waitPidResult) reply {
	if ret.Err != nil {
		return reply{
			Error: &errorReply{
				Msg: fmt.Sprintf("execve: wait4: %v", ret.Err),
			},
		}
	}

	waitStatus := ret.WaitStatus
	rusage := ret.Rusage

	status := runner.StatusNormal
	userTime := time.Duration(rusage.Utime.Nano()) // ns
	userMem := runner.Size(rusage.Maxrss << 10)    // bytes
	switch {
	case waitStatus.Exited():
		exitStatus := waitStatus.ExitStatus()
		if exitStatus != 0 {
			status = runner.StatusNonzeroExitStatus
		}
		return reply{
			ExecReply: &execReply{
				Status:     status,
				ExitStatus: exitStatus,
				Time:       userTime,
				Memory:     userMem,
			},
		}

	case waitStatus.Signaled():
		switch waitStatus.Signal() {
		// kill signal treats as TLE
		case syscall.SIGXCPU, syscall.SIGKILL:
			status = runner.StatusTimeLimitExceeded
		case syscall.SIGXFSZ:
			status = runner.StatusOutputLimitExceeded
		case syscall.SIGSYS:
			status = runner.StatusDisallowedSyscall
		default:
			status = runner.StatusSignalled
		}
		return reply{
			ExecReply: &execReply{
				ExitStatus: int(waitStatus.Signal()),
				Status:     status,
				Time:       userTime,
				Memory:     userMem,
			},
		}

	default:
		return reply{
			Error: &errorReply{
				Msg: fmt.Sprintf("execve: unknown status: %v", waitStatus),
			},
		}
	}
}
