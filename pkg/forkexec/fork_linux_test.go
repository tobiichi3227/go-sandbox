package forkexec

import (
	"os"
	"syscall"
	"testing"

	"github.com/tobiichi3227/go-sandbox/pkg/mount"
)

func TestFork_DropCaps(t *testing.T) {
	t.Parallel()
	r := Runner{
		Args:       []string{"/bin/echo"},
		CloneFlags: syscall.CLONE_NEWUSER,
		DropCaps:   true,
	}
	_, err := r.Start()
	if err != nil {
		t.Fatal(err)
	}
}

func TestFork_ETXTBSY(t *testing.T) {
	f, err := os.CreateTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Remove(f.Name())
		f.Close()
	})

	if err := f.Chmod(0o777); err != nil {
		t.Fatal(err)
	}

	echo, err := os.Open("/bin/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()

	_, err = f.ReadFrom(echo)
	if err != nil {
		t.Fatal(err)
	}

	r := Runner{
		Args:     []string{f.Name()},
		ExecFile: f.Fd(),
	}
	_, err = r.Start()
	e, ok := err.(ChildError)
	if !ok {
		t.Fatalf("not a child error")
	}
	if e.Err != syscall.ETXTBSY && e.Location != LocExecve && e.Index != 0 {
		t.Fatal(err)
	}
}

func TestFork_OK(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	if err := f.Chmod(0o777); err != nil {
		t.Fatal(err)
	}

	echo, err := os.Open("/bin/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()

	_, err = f.ReadFrom(echo)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	r := Runner{
		Args: []string{f.Name()},
	}
	_, err = r.Start()
	if err != nil {
		t.Fatal(err)
	}
}

func TestFork_ENOENT(t *testing.T) {
	t.Parallel()
	m, err := mount.NewBuilder().
		WithMount(
			mount.Mount{
				Source: "NOT_EXISTS",
			}).Build()
	if err != nil {
		t.Fatal(err)
	}
	r := Runner{
		Args:       []string{"/bin/echo"},
		CloneFlags: syscall.CLONE_NEWNS | syscall.CLONE_NEWUSER,
		Mounts:     m,
	}
	_, err = r.Start()
	e, ok := err.(ChildError)
	if !ok {
		t.Fatalf("not a child error")
	}
	if e.Err != syscall.ENOENT && e.Location != LocExecve {
		t.Fatal(err)
	}
}
