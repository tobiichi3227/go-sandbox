package mount

import (
	"syscall"
)

// Mount defines syscall for mount points
type Mount struct {
	Source, Target, FsType, Data string
	Flags                        uintptr
	IgnoreErr                    bool
}

// SyscallParams defines the raw syscall arguments to mount
type SyscallParams struct {
	Source, Target, FsType, Data *byte
	Flags                        uintptr
	Prefixes                     []*byte
	MakeNod                      bool
	IgnoreErr                    bool
}

// ToSyscall convert Mount to SyscallPrams
func (m *Mount) ToSyscall() (*SyscallParams, error) {
	var data *byte
	source, err := syscall.BytePtrFromString(m.Source)
	if err != nil {
		return nil, err
	}
	target, err := syscall.BytePtrFromString(m.Target)
	if err != nil {
		return nil, err
	}
	fsType, err := syscall.BytePtrFromString(m.FsType)
	if err != nil {
		return nil, err
	}
	if m.Data != "" {
		data, err = syscall.BytePtrFromString(m.Data)
		if err != nil {
			return nil, err
		}
	}
	prefix := pathPrefix(m.Target)
	paths, err := arrayPtrFromStrings(prefix)
	if err != nil {
		return nil, err
	}
	return &SyscallParams{
		Source:    source,
		Target:    target,
		FsType:    fsType,
		Flags:     m.Flags,
		Data:      data,
		Prefixes:  paths,
		IgnoreErr: m.IgnoreErr,
	}, nil
}

// pathPrefix get all components from path
func pathPrefix(path string) []string {
	ret := make([]string, 0)
	for i := 1; i < len(path); i++ {
		if path[i] == '/' {
			ret = append(ret, path[:i])
		}
	}
	ret = append(ret, path)
	return ret
}

// arrayPtrFromStrings converts strings to c style strings
func arrayPtrFromStrings(str []string) ([]*byte, error) {
	bytes := make([]*byte, 0, len(str))
	for _, s := range str {
		b, err := syscall.BytePtrFromString(s)
		if err != nil {
			return nil, err
		}
		bytes = append(bytes, b)
	}
	return bytes, nil
}
