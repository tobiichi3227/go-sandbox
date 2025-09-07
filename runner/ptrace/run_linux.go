package ptrace

import (
	"context"
	"os"

	"github.com/tobiichi3227/go-sandbox/pkg/forkexec"
	"github.com/tobiichi3227/go-sandbox/ptracer"
	"github.com/tobiichi3227/go-sandbox/runner"
)

// Run starts the tracing process
func (r *Runner) Run(c context.Context) runner.Result {
	ch := &forkexec.Runner{
		Args:     r.Args,
		Env:      r.Env,
		ExecFile: r.ExecFile,
		RLimits:  r.RLimits,
		Files:    r.Files,
		WorkDir:  r.WorkDir,
		Seccomp:  r.Seccomp.SockFprog(),
		Ptrace:   true,
		SyncFunc: r.SyncFunc,

		UnshareCgroupAfterSync: os.Getuid() == 0,
	}

	th := &tracerHandler{
		ShowDetails: r.ShowDetails,
		Unsafe:      r.Unsafe,
		Handler:     r.Handler,
	}

	tracer := ptracer.Tracer{
		Handler: th,
		Runner:  ch,
		Limit:   r.Limit,
	}
	return tracer.Trace(c)
}
