package ptrace

import (
	"syscall"

	"github.com/tobiichi3227/go-sandbox/pkg/rlimit"
	"github.com/tobiichi3227/go-sandbox/pkg/seccomp"
	"github.com/tobiichi3227/go-sandbox/ptracer"
	"github.com/tobiichi3227/go-sandbox/runner"
)

// Runner defines the spec to run a program safely by ptracer
type Runner struct {
	// argv and env for the child process
	// work path set by setcwd (current working directory for child)
	Args    []string
	Env     []string
	WorkDir string

	// fexecve
	ExecFile uintptr

	// file descriptors for new process, from 0 to len - 1
	Files []uintptr

	// Resource limit set by set rlimit
	RLimits []rlimit.RLimit

	// Res limit enforced by tracer
	Limit runner.Limit

	// Defines seccomp filter for the ptrace runner
	// file access syscalls need to set as ActionTrace
	// allowed need to set as ActionAllow
	// default action should be ActionTrace / ActionKill
	Seccomp seccomp.Filter

	// Traced syscall handler
	Handler Handler

	// ShowDetails / Unsafe debug flag
	ShowDetails, Unsafe bool

	// Use by cgroup to add proc
	SyncFunc func(pid int) error
}

// BanRet defines the return value for a syscall ban action
var BanRet = syscall.EACCES

// Handler defines the action when a file access encountered
type Handler interface {
	CheckRead(string) ptracer.TraceAction
	CheckWrite(string) ptracer.TraceAction
	CheckStat(string) ptracer.TraceAction
	CheckSyscall(string) ptracer.TraceAction
}
