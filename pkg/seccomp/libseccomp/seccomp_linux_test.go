package libseccomp

import (
	"testing"

	"github.com/tobiichi3227/go-sandbox/pkg/seccomp"
)

var (
	defaultSyscallAllows = []string{
		"read", "write", "readv", "writev", "close", "fstat", "lseek", "dup", "dup2", "dup3", "ioctl", "fcntl", "fadvise64",
		"mmap", "mprotect", "munmap", "brk", "mremap", "msync", "mincore", "madvise",
		"rt_sigaction", "rt_sigprocmask", "rt_sigreturn", "rt_sigpending", "sigaltstack",
		"getcwd", "exit", "exit_group", "arch_prctl",
		"gettimeofday", "getrlimit", "getrusage", "times", "time", "clock_gettime", "restart_syscall",
	}

	defaultSyscallTraces = []string{
		"execve", "open", "openat", "unlink", "unlinkat", "readlink", "readlinkat", "lstat", "stat", "access", "faccessat",
	}
)

func TestBuildFilter(t *testing.T) {
	_, err := buildFilterMock()
	if err != nil {
		t.Error("BuildFilter failed")
	}
}

// BenchmarkBuildDefaultFilter is about 0.2ms/op
func BenchmarkBuildDefaultFilter(b *testing.B) {
	for i := 0; i < b.N; i++ {
		builder := Builder{
			Allow:   defaultSyscallAllows,
			Trace:   defaultSyscallTraces,
			Default: ActionTrace,
		}
		builder.Build()
	}
}

func buildFilterMock() (seccomp.Filter, error) {
	b := Builder{
		Allow:   []string{"fork"},
		Trace:   []string{"execve"},
		Default: ActionTrace,
	}
	return b.Build()
}
