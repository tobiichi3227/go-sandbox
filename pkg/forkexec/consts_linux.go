package forkexec

import (
	"golang.org/x/sys/unix"
)

// defines missing consts from syscall package
const (
	SECCOMP_SET_MODE_STRICT   = 0
	SECCOMP_SET_MODE_FILTER   = 1
	SECCOMP_FILTER_FLAG_TSYNC = 1

	// Unshare flags
	UnshareFlags = unix.CLONE_NEWIPC | unix.CLONE_NEWNET | unix.CLONE_NEWNS |
		unix.CLONE_NEWPID | unix.CLONE_NEWUSER | unix.CLONE_NEWUTS | unix.CLONE_NEWCGROUP

	// Read-only bind mount need to be remounted
	bindRo = unix.MS_BIND | unix.MS_RDONLY
)

// used by unshare remount / to private
var (
	none    = []byte("none\000")
	slash   = []byte("/\000")
	empty   = []byte("\000")
	tmpfs   = []byte("tmpfs\000")
	devnull = []byte("/dev/null\000")

	// tmp dir made by pivot_root
	oldRoot = []byte("old_root\000")

	// set groups for unshare user
	setGIDAllow = []byte("allow")
	setGIDDeny  = []byte("deny")

	// go does not allow constant uintptr to be negative...
	_AT_FDCWD = unix.AT_FDCWD

	// Drop all capabilities
	dropCapHeader = unix.CapUserHeader{
		Version: unix.LINUX_CAPABILITY_VERSION_3,
		Pid:     0,
	}

	dropCapData = unix.CapUserData{
		Effective:   0,
		Permitted:   0,
		Inheritable: 0,
	}

	// 1ms
	etxtbsyRetryInterval = unix.Timespec{
		Nsec: 1 * 1000 * 1000,
	}
)

const (
	_SECURE_NOROOT = 1 << iota
	_SECURE_NOROOT_LOCKED

	_SECURE_NO_SETUID_FIXUP
	_SECURE_NO_SETUID_FIXUP_LOCKED

	_SECURE_KEEP_CAPS
	_SECURE_KEEP_CAPS_LOCKED

	_SECURE_NO_CAP_AMBIENT_RAISE
	_SECURE_NO_CAP_AMBIENT_RAISE_LOCKED
)
