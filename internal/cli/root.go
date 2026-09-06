package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"

	"spm4a/internal/daemon"
	"spm4a/internal/ipc"
)

var (
	flagJSON      bool
	flagNamespace string
)

type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }

func usageErr(format string, args ...any) error {
	return &usageError{fmt.Errorf(format, args...)}
}

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root := newRootCmd()
	err := root.ExecuteContext(ctx)
	if err == nil {
		return
	}
	if re, ok := err.(*ipc.Error); ok {
		fmt.Fprintln(os.Stderr, "spm4a:", re.Message)
	} else {
		fmt.Fprintln(os.Stderr, "spm4a:", err)
	}
	os.Exit(exitCode(err))
}

func exitCode(err error) int {
	var ue *usageError
	if errors.As(err, &ue) {
		return 2
	}
	var re *ipc.Error
	if errors.As(err, &re) {
		switch re.Code {
		case ipc.CodeAppNotFound:
			return 3
		case ipc.CodeAppExists, ipc.CodePortConflict:
			return 4
		case ipc.CodeReadyTimeout:
			return 6
		case ipc.CodeInvalidParams, ipc.CodeInvalidRequest:
			return 2
		}
		return 1
	}
	if errors.Is(err, daemon.ErrUnavailable) {
		return 5
	}
	return 1
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "spm4a",
		Short:         "Spring Boot application process manager",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	pf := root.PersistentFlags()
	pf.BoolVar(&flagJSON, "json", false, "machine-readable JSON output")
	pf.StringVar(&flagNamespace, "namespace", "", "namespace (alias --ns; env SPM4A_NAMESPACE)")
	pf.StringVar(&flagNamespace, "ns", "", "alias of --namespace")
	_ = pf.MarkHidden("ns")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err} })
	root.AddCommand(
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newReloadCmd(),
		newLsCmd(),
		newStatusCmd(),
		newLogsCmd(),
		newHealthCmd(),
		newRmCmd(),
		newKillCmd(),
		newDaemonCmd(),
	)
	return root
}

func exactArgs(n int, usage string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return usageErr("expected %d argument(s) %s, got %d", n, usage, len(args))
		}
		return nil
	}
}

func spmHome() (string, error) {
	if h := os.Getenv("SPM4A_HOME"); h != "" {
		return h, nil
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(u, ".spm4a"), nil
}

// rpcClient connects to the daemon, spawning it on demand.
func rpcClient(ctx context.Context) (*ipc.Client, error) {
	home, err := spmHome()
	if err != nil {
		return nil, err
	}
	return daemon.Ensure(ctx, home)
}
