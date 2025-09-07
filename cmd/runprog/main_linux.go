// Command runprog executes program defined restricted environment including seccomp-ptraced, namespaced and containerized.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tobiichi3227/go-sandbox/cmd/runprog/config"
	"github.com/tobiichi3227/go-sandbox/container"
	"github.com/tobiichi3227/go-sandbox/pkg/cgroup"
	"github.com/tobiichi3227/go-sandbox/pkg/forkexec"
	"github.com/tobiichi3227/go-sandbox/pkg/memfd"
	"github.com/tobiichi3227/go-sandbox/pkg/mount"
	"github.com/tobiichi3227/go-sandbox/pkg/rlimit"
	"github.com/tobiichi3227/go-sandbox/pkg/seccomp"
	"github.com/tobiichi3227/go-sandbox/pkg/seccomp/libseccomp"
	"github.com/tobiichi3227/go-sandbox/runner"
	"github.com/tobiichi3227/go-sandbox/runner/ptrace"
	"github.com/tobiichi3227/go-sandbox/runner/ptrace/filehandler"
	"github.com/tobiichi3227/go-sandbox/runner/unshare"
	"golang.org/x/sys/unix"
)

var (
	addReadable, addWritable, addRawReadable, addRawWritable       arrayFlags
	allowProc, unsafe, showDetails, useCGroup, memfile, cred, nucg bool
	timeLimit, realTimeLimit, memoryLimit, outputLimit, stackLimit uint64
	inputFileName, outputFileName, errorFileName, workPath, runt   string

	useCGroupFd   bool
	pType, result string
	args          []string
)

// container init
func init() {
	container.Init()
}

func main() {
	flag.Usage = printUsage
	flag.Uint64Var(&timeLimit, "tl", 1, "Set time limit (in second)")
	flag.Uint64Var(&realTimeLimit, "rtl", 0, "Set real time limit (in second)")
	flag.Uint64Var(&memoryLimit, "ml", 256, "Set memory limit (in mb)")
	flag.Uint64Var(&outputLimit, "ol", 64, "Set output limit (in mb)")
	flag.Uint64Var(&stackLimit, "sl", 1024, "Set stack limit (in mb)")
	flag.StringVar(&inputFileName, "in", "", "Set input file name")
	flag.StringVar(&outputFileName, "out", "", "Set output file name")
	flag.StringVar(&errorFileName, "err", "", "Set error file name")
	flag.StringVar(&workPath, "work-path", "", "Set the work path of the program")
	flag.StringVar(&pType, "type", "default", "Set the program type (for some program such as python)")
	flag.StringVar(&result, "res", "stdout", "Set the file name for output the result")
	flag.Var(&addReadable, "add-readable", "Add a readable file")
	flag.Var(&addWritable, "add-writable", "Add a writable file")
	flag.BoolVar(&unsafe, "unsafe", false, "Don't check dangerous syscalls")
	flag.BoolVar(&showDetails, "show-trace-details", false, "Show trace details")
	flag.BoolVar(&allowProc, "allow-proc", false, "Allow fork, exec... etc.")
	flag.Var(&addRawReadable, "add-readable-raw", "Add a readable file (don't transform to its real path)")
	flag.Var(&addRawWritable, "add-writable-raw", "Add a writable file (don't transform to its real path)")
	flag.BoolVar(&useCGroup, "cgroup", false, "Use cgroup to colloct resource usage")
	flag.BoolVar(&useCGroupFd, "cgroupfd", false, "Use cgroup FD to clone3 (cgroup v2 & kernel > 5.7)")
	flag.BoolVar(&memfile, "memfd", false, "Use memfd as exec file")
	flag.StringVar(&runt, "runner", "ptrace", "Runner for the program (ptrace, ns, container)")
	flag.BoolVar(&cred, "cred", false, "Generate credential for containers (uid=10000)")
	flag.BoolVar(&nucg, "nucg", false, "don't unshare cgroup")
	flag.Parse()

	args = flag.Args()
	if len(args) == 0 {
		printUsage()
	}

	if realTimeLimit < timeLimit {
		realTimeLimit = timeLimit + 2
	}
	if stackLimit > memoryLimit {
		stackLimit = memoryLimit
	}
	if workPath == "" {
		workPath, _ = os.Getwd()
	}

	var (
		f   *os.File
		err error
	)
	if result == "stdout" {
		f = os.Stdout
	} else if result == "stderr" {
		f = os.Stderr
	} else {
		f, err = os.Create(result)
		if err != nil {
			debug("Failed to open result file:", err)
			return
		}
		defer f.Close()
	}

	rt, err := start()
	if rt == nil {
		rt = &runner.Result{
			Status: runner.StatusRunnerError,
		}
	}
	if err == nil && rt.Status != runner.StatusNormal {
		err = rt.Status
	}
	debug("setupTime: ", rt.SetUpTime)
	debug("runningTime: ", rt.RunningTime)
	if err != nil {
		debug(err)
		c, ok := err.(runner.Status)
		if !ok {
			c = runner.StatusRunnerError
		}
		// Handle fatal error from trace
		fmt.Fprintf(f, "%d %d %d %d\n", getStatus(c),
			int(rt.Time.Round(time.Millisecond)/time.Millisecond), uint64(rt.Memory)>>10, rt.ExitStatus)
		if c == runner.StatusRunnerError {
			os.Exit(1)
		}
	} else {
		fmt.Fprintf(f, "%d %d %d %d\n", 0,
			int(rt.Time.Round(time.Millisecond)/time.Millisecond), uint64(rt.Memory)>>10, rt.ExitStatus)
	}
}

type containerRunner struct {
	container.Environment
	container.ExecveParam
}

func (r *containerRunner) Run(c context.Context) runner.Result {
	return r.Environment.Execve(c, r.ExecveParam)
}

func start() (*runner.Result, error) {
	var (
		r        runner.Runner
		cg       cgroup.Cgroup
		cgDir    *os.File
		cgroupFd uintptr
		err      error
		execFile uintptr
		rt       runner.Result
	)

	addRead := filehandler.GetExtraSet(addReadable, addRawReadable)
	addWrite := filehandler.GetExtraSet(addWritable, addRawWritable)
	args, allow, trace, h := config.GetConf(pType, workPath, args, addRead, addWrite, allowProc)

	mb := mount.NewBuilder().
		// basic exec and lib
		WithBind("/bin", "bin", true).
		WithBind("/lib", "lib", true).
		WithBind("/lib64", "lib64", true).
		WithBind("/usr", "usr", true).
		// java wants /proc/self/exe as it need relative path for lib
		// however, /proc gives interface like /proc/1/fd/3 ..
		// it is fine since open that file will be a EPERM
		// changing the fs uid and gid would be a good idea
		WithProc().
		// some compiler have multiple version
		WithBind("/etc/alternatives", "etc/alternatives", true).
		// fpc wants /etc/fpc.cfg
		WithBind("/etc/fpc.cfg", "etc/fpc.cfg", true).
		// go wants /dev/null
		WithBind("/dev/null", "dev/null", false).
		// ghc wants /var/lib/ghc
		WithBind("/var/lib/ghc", "var/lib/ghc", true).
		// work dir
		WithTmpfs("w", "size=8m,nr_inodes=4k").
		// tmp dir
		WithTmpfs("tmp", "size=8m,nr_inodes=4k").
		FilterNotExist()

	mt, err := mb.FilterNotExist().Build()
	if err != nil {
		return nil, err
	}

	if useCGroup {
		t := cgroup.DetectType()
		if t == cgroup.TypeV2 {
			cgroup.EnableV2Nesting()
		}
		ct, err := cgroup.GetAvailableController()
		if err != nil {
			return nil, err
		}
		b, err := cgroup.New("runprog", ct)
		if err != nil {
			return nil, err
		}
		debug(b)
		cg, err = b.Random("runprog")
		if err != nil {
			return nil, err
		}
		defer cg.Destroy()
		if err = cg.SetMemoryLimit(memoryLimit << 20); err != nil {
			return nil, err
		}
		debug("cgroup:", cg)
		if useCGroupFd {
			debug("use cgroup fd")
			if t != cgroup.TypeV2 {
				return nil, fmt.Errorf("use cgroup fd cannot be enabled without cgroup v2")
			}
			if cgDir, err = cg.Open(); err != nil {
				return nil, err
			}
			defer cgDir.Close()
			cgroupFd = cgDir.Fd()
		}
	}

	var syncFunc func(pid int) error
	if cg != nil {
		syncFunc = func(pid int) error {
			if err := cg.AddProc(pid); err != nil {
				return err
			}
			return nil
		}
	}

	if memfile {
		fin, err := os.Open(args[0])
		if err != nil {
			return nil, fmt.Errorf("failed to open args[0]: %w", err)
		}
		execf, err := memfd.DupToMemfd("run_program", fin)
		if err != nil {
			return nil, fmt.Errorf("dup to memfd failed: %w", err)
		}
		fin.Close()
		defer execf.Close()
		execFile = execf.Fd()
		debug("memfd: ", execFile)
	}

	// open input / output / err files
	files, err := prepareFiles(inputFileName, outputFileName, errorFileName)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare files: %w", err)
	}
	defer closeFiles(files)

	// if not defined, then use the original value
	fds := make([]uintptr, len(files))
	for i, f := range files {
		if f != nil {
			fds[i] = f.Fd()
		} else {
			fds[i] = uintptr(i)
		}
	}

	rlims := rlimit.RLimits{
		CPU:         timeLimit,
		CPUHard:     realTimeLimit,
		FileSize:    outputLimit << 20,
		Stack:       stackLimit << 20,
		Data:        memoryLimit << 20,
		OpenFile:    256,
		DisableCore: true,
	}
	debug("rlimit: ", rlims)

	actionDefault := libseccomp.ActionKill
	if showDetails {
		actionDefault = libseccomp.ActionTrace
	}
	if runt != "ptrace" {
		allow = append(allow, trace...)
		trace = nil
	}
	builder := libseccomp.Builder{
		Allow:   allow,
		Trace:   trace,
		Default: actionDefault,
	}
	// do not build filter for container unsafe since seccomp is not compatible with aarch64 syscalls
	var filter seccomp.Filter
	if !unsafe || runt != "container" {
		filter, err = builder.Build()
		if err != nil {
			return nil, fmt.Errorf("failed to create seccomp filter: %w", err)
		}
	}

	limit := runner.Limit{
		TimeLimit:   time.Duration(timeLimit) * time.Second,
		MemoryLimit: runner.Size(memoryLimit << 20),
	}

	if runt == "container" {
		var credG container.CredGenerator
		if cred {
			credG = newCredGen()
		}
		var stderr io.Writer
		if showDetails {
			stderr = os.Stderr
		}

		cloneFlag := forkexec.UnshareFlags
		if nucg {
			cloneFlag &= ^unix.CLONE_NEWCGROUP
		}

		b := container.Builder{
			TmpRoot:       "dm",
			Mounts:        mb.Mounts,
			Stderr:        stderr,
			CredGenerator: credG,
			CloneFlags:    uintptr(cloneFlag),
		}

		m, err := b.Build()
		if err != nil {
			return nil, fmt.Errorf("failed to new container: %w", err)
		}
		defer m.Destroy()
		err = m.Ping()
		if err != nil {
			return nil, fmt.Errorf("failed to ping container: %w", err)
		}
		if unsafe {
			filter = nil
		}
		r = &containerRunner{
			Environment: m,
			ExecveParam: container.ExecveParam{
				Args:          args,
				Env:           []string{pathEnv},
				Files:         fds,
				ExecFile:      execFile,
				RLimits:       rlims.PrepareRLimit(),
				Seccomp:       filter,
				SyncFunc:      syncFunc,
				CgroupFD:      cgroupFd,
				SyncAfterExec: cg == nil || cgDir != nil,
			},
		}
	} else if runt == "ns" {
		root, err := os.MkdirTemp("", "ns")
		if err != nil {
			return nil, fmt.Errorf("cannot make temp root for new namespace")
		}
		defer os.RemoveAll(root)
		r = &unshare.Runner{
			Args:        args,
			Env:         []string{pathEnv},
			ExecFile:    execFile,
			WorkDir:     "/w",
			Files:       fds,
			RLimits:     rlims.PrepareRLimit(),
			Limit:       limit,
			Seccomp:     filter,
			Root:        root,
			Mounts:      mt,
			ShowDetails: showDetails,
			SyncFunc:    syncFunc,
			HostName:    "run_program",
			DomainName:  "run_program",
		}
	} else if runt == "ptrace" {
		r = &ptrace.Runner{
			Args:        args,
			Env:         []string{pathEnv},
			ExecFile:    execFile,
			WorkDir:     workPath,
			RLimits:     rlims.PrepareRLimit(),
			Limit:       limit,
			Files:       fds,
			Seccomp:     filter,
			ShowDetails: showDetails,
			Unsafe:      unsafe,
			Handler:     h,
			SyncFunc:    syncFunc,
		}
	} else {
		return nil, fmt.Errorf("invalid runner type: %s", runt)
	}

	// gracefully shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)

	// Run tracer
	sTime := time.Now()
	c, cancel := context.WithTimeout(context.Background(), time.Duration(int64(realTimeLimit)*int64(time.Second)))
	defer cancel()

	s := make(chan runner.Result, 1)
	go func() {
		s <- r.Run(c)
	}()
	rTime := time.Now()

	select {
	case <-sig:
		cancel()
		rt = <-s
		rt.Status = runner.StatusRunnerError

	case rt = <-s:
	}
	eTime := time.Now()

	if rt.SetUpTime == 0 {
		rt.SetUpTime = rTime.Sub(sTime)
		rt.RunningTime = eTime.Sub(rTime)
	}

	debug("results:", rt, err)

	if useCGroup {
		cpu, err := cg.CPUUsage()
		if err != nil {
			return nil, fmt.Errorf("cgroup cpu: %v", err)
		} else {
			rt.Time = time.Duration(cpu)
		}
		// max memory usage may not exist in cgroup v2
		memory, err := cg.MemoryMaxUsage()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("cgroup memory: %v", err)
		} else if err == nil {
			rt.Memory = runner.Size(memory)
		}
		procPeak, err := cg.ProcessPeak()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("cgroup pid: %v", err)
		} else if err == nil {
			rt.ProcPeak = procPeak
		}
		debug("cgroup: cpu: ", cpu, " memory: ", memory, " procPeak: ", procPeak)
		debug("cgroup:", rt)
	}
	if rt.Status == runner.StatusTimeLimitExceeded || rt.Status == runner.StatusNormal {
		if rt.Time > limit.TimeLimit {
			rt.Status = runner.StatusTimeLimitExceeded
		} else {
			rt.Status = runner.StatusNormal
		}
	}
	if rt.Status == runner.StatusMemoryLimitExceeded || rt.Status == runner.StatusNormal {
		if rt.Memory > limit.MemoryLimit {
			rt.Status = runner.StatusMemoryLimitExceeded
		} else {
			rt.Status = runner.StatusNormal
		}
	}
	return &rt, nil
}

type credGen struct {
	cur uint32
}

func newCredGen() *credGen {
	return &credGen{cur: 10000}
}

func (c *credGen) Get() syscall.Credential {
	n := atomic.AddUint32(&c.cur, 1)
	return syscall.Credential{
		Uid: n,
		Gid: n,
	}
}
