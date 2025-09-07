package forkexec

import (
	"os"
	"syscall"
	"testing"

	"github.com/tobiichi3227/go-sandbox/pkg/mount"
	"golang.org/x/sys/unix"
)

// All testing data were from docker env on amd64 arch

const (
	roBind = unix.MS_BIND | unix.MS_NOSUID | unix.MS_PRIVATE | unix.MS_RDONLY
)

var defaultBind = []string{"/usr", "/lib", "/lib64", "/bin"}

func BenchmarkStdFork(b *testing.B) {
	f := openNull(b)
	defer f.Close()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			pid, err := syscall.ForkExec("/bin/echo", nil, &syscall.ProcAttr{
				Env:   []string{"PATH=/bin"},
				Files: []uintptr{f.Fd(), f.Fd(), f.Fd()},
			})
			if err != nil {
				b.Fatal(err)
			}
			wait4(pid, b)
		}
	})
}

func BenchmarkStdForkUser(b *testing.B) {
	f := openNull(b)
	defer f.Close()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			pid, err := syscall.ForkExec("/bin/echo", nil, &syscall.ProcAttr{
				Env:   []string{"PATH=/bin"},
				Files: []uintptr{f.Fd(), f.Fd(), f.Fd()},
				Sys: &syscall.SysProcAttr{
					Cloneflags: syscall.CLONE_NEWUSER,
				},
			})
			if err != nil {
				b.Fatal(err)
			}
			wait4(pid, b)
		}
	})
}

// BenchmarkSimpleFork is about 0.70ms/op
func BenchmarkSimpleFork(b *testing.B) {
	r, f := getRunner(b)
	defer f.Close()
	benchmarkRun(r, b)
}

// BenchmarkUnsharePid is about 0.79ms/op
func BenchmarkUnsharePid(b *testing.B) {
	r, f := getRunner(b)
	defer f.Close()
	r.CloneFlags = unix.CLONE_NEWPID
	benchmarkRun(r, b)
}

// BenchmarkUnshareUser is about 0.84ms/op
func BenchmarkUnshareUser(b *testing.B) {
	r, f := getRunner(b)
	defer f.Close()
	r.CloneFlags = unix.CLONE_NEWUSER
	benchmarkRun(r, b)
}

// BenchmarkUnshareUts is about 0.78ms/op
func BenchmarkUnshareUts(b *testing.B) {
	r, f := getRunner(b)
	defer f.Close()
	r.CloneFlags = unix.CLONE_NEWUTS
	benchmarkRun(r, b)
}

// BenchmarkUnshareCgroup is about 0.85ms/op
func BenchmarkUnshareCgroup(b *testing.B) {
	r, f := getRunner(b)
	defer f.Close()
	r.CloneFlags = unix.CLONE_NEWCGROUP
	benchmarkRun(r, b)
}

// BenchmarkUnshareIpc is about 51ms/op
func BenchmarkUnshareIpc(b *testing.B) {
	r, f := getRunner(b)
	defer f.Close()
	r.CloneFlags = unix.CLONE_NEWIPC
	benchmarkRun(r, b)
}

// BenchmarkUnshareMount is about 51ms/op
func BenchmarkUnshareMount(b *testing.B) {
	r, f := getRunner(b)
	defer f.Close()
	r.CloneFlags = unix.CLONE_NEWNS
	benchmarkRun(r, b)
}

// BenchmarkUnshareNet is about 426ms/op
func BenchmarkUnshareNet(b *testing.B) {
	r, f := getRunner(b)
	defer f.Close()
	r.CloneFlags = unix.CLONE_NEWNET
	benchmarkRun(r, b)
}

// BenchmarkFastUnshareMountPivot is about 104ms/op
func BenchmarkFastUnshareMountPivot(b *testing.B) {
	root, err := os.MkdirTemp("", "ns")
	if err != nil {
		b.Errorf("failed to create temp dir")
	}
	defer os.RemoveAll(root)
	r, f := getRunner(b)
	defer f.Close()
	r.CloneFlags = unix.CLONE_NEWNS | unix.CLONE_NEWPID | unix.CLONE_NEWUSER | unix.CLONE_NEWUTS | unix.CLONE_NEWCGROUP
	r.PivotRoot = root
	r.NoNewPrivs = true
	r.DropCaps = true
	r.Mounts = getMounts(defaultBind)
	benchmarkRun(r, b)
}

// BenchmarkUnshareAll is about 800ms/op
func BenchmarkUnshareAll(b *testing.B) {
	r, f := getRunner(b)
	defer f.Close()
	r.CloneFlags = UnshareFlags
	r.NoNewPrivs = true
	r.DropCaps = true
	benchmarkRun(r, b)
}

// BenchmarkUnshareMountPivot is about 880ms/op
func BenchmarkUnshareMountPivot(b *testing.B) {
	root, err := os.MkdirTemp("", "ns")
	if err != nil {
		b.Errorf("failed to create temp dir")
	}
	defer os.RemoveAll(root)
	r, f := getRunner(b)
	defer f.Close()
	r.CloneFlags = UnshareFlags
	r.PivotRoot = root
	r.NoNewPrivs = true
	r.DropCaps = true
	r.Mounts = getMounts(defaultBind)
	benchmarkRun(r, b)
}

func getRunner(b *testing.B) (*Runner, *os.File) {
	f := openNull(b)
	return &Runner{
		Args:    []string{"/bin/echo"},
		Env:     []string{"PATH=/bin"},
		Files:   []uintptr{f.Fd(), f.Fd(), f.Fd()},
		WorkDir: "/bin",
	}, f
}

func benchmarkRun(r *Runner, b *testing.B) {
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			pid, err := r.Start()
			if err != nil {
				b.Fatal(err)
			}
			wait4(pid, b)
		}
	})
}

func getMounts(dirs []string) []mount.SyscallParams {
	builder := mount.NewBuilder()
	for _, d := range dirs {
		builder.WithMount(mount.Mount{
			Source: d,
			Target: d[1:],
			Flags:  roBind,
		})
	}
	m, _ := builder.FilterNotExist().Build()
	return m
}

func openNull(b *testing.B) *os.File {
	f, err := os.OpenFile("/dev/null", os.O_RDWR, 0o666)
	if err != nil {
		b.Errorf("Failed to open %v", err)
	}
	return f
}

func wait4(pid int, b *testing.B) {
	var wstat syscall.WaitStatus
	for {
		syscall.Wait4(pid, &wstat, 0, nil)
		if wstat.Exited() {
			if s := wstat.ExitStatus(); s != 0 {
				b.Errorf("Exited: %d", s)
			}
			break
		}
	}
}
