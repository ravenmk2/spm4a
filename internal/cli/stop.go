package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"spm4a/internal/ipc"
	"spm4a/internal/state"
)

func newStopCmd() *cobra.Command {
	var now, all bool
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "stop <name> | stop --all",
		Short: "Stop an app, or all apps in scope with --all",
		Args: func(cmd *cobra.Command, args []string) error {
			if all {
				if len(args) != 0 {
					return usageErr("--all takes no app name")
				}
				return nil
			}
			return exactArgs(1, "<name>")(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := rpcClient(cmd.Context())
			if err != nil {
				return err
			}
			if all {
				return stopAllApps(cmd, cl, now, timeout)
			}
			ns, err := resolveTarget(cmd.Context(), cl, args[0])
			if err != nil {
				return err
			}
			var res struct {
				App *state.App `json:"app"`
			}
			err = cl.Call(cmd.Context(), "app.stop", ipc.StopParams{
				Namespace: ns, Name: args[0], Now: now, Timeout: timeout.String(),
			}, &res)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(res)
			}
			fmt.Printf("%s: stopped\n", args[0])
			return nil
		},
	}
	c.Flags().BoolVar(&now, "now", false, "kill immediately without graceful phase")
	c.Flags().DurationVar(&timeout, "timeout", 15*time.Second, "graceful shutdown timeout")
	c.Flags().BoolVar(&all, "all", false, "stop all apps in the namespace (with -A: all namespaces)")
	return c
}

func stopAllApps(cmd *cobra.Command, cl *ipc.Client, now bool, timeout time.Duration) error {
	apps, err := batchApps(cmd.Context(), cl)
	if err != nil {
		return err
	}
	results := []*batchResult{}
	for _, a := range apps {
		if !a.Active() {
			continue
		}
		r := &batchResult{Namespace: a.Spec.Namespace, Name: a.Spec.Name, OK: true}
		var res struct {
			App *state.App `json:"app"`
		}
		if err := cl.Call(cmd.Context(), "app.stop", ipc.StopParams{
			Namespace: a.Spec.Namespace, Name: a.Spec.Name, Now: now, Timeout: timeout.String(),
		}, &res); err != nil {
			r.OK = false
			r.Err = err.Error()
		}
		results = append(results, r)
	}
	return reportBatch("stopped", results)
}

func newRestartCmd() *cobra.Command {
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "restart <name>",
		Short: "Full process restart",
		Args:  exactArgs(1, "<name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := rpcClient(cmd.Context())
			if err != nil {
				return err
			}
			ns, err := resolveTarget(cmd.Context(), cl, args[0])
			if err != nil {
				return err
			}
			var res struct {
				App *state.App `json:"app"`
			}
			err = cl.Call(cmd.Context(), "app.restart", ipc.RestartParams{
				Namespace: ns, Name: args[0], Timeout: timeout.String(),
			}, &res)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(res)
			}
			fmt.Printf("%s: %s (pid %d, port %d)\n", args[0], res.App.Status, res.App.PID, res.App.ActualPort)
			return nil
		},
	}
	c.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "ready wait timeout")
	return c
}
