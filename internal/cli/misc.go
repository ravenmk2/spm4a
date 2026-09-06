package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"spm4a/internal/daemon"
	"spm4a/internal/ipc"
)

func newRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove an app from management (must be stopped)",
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
			var res map[string]any
			if err := cl.Call(cmd.Context(), "app.delete", ipc.NameParams{Namespace: ns, Name: args[0]}, &res); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(res)
			}
			fmt.Printf("%s: removed\n", args[0])
			return nil
		},
	}
}

func newKillCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:   "kill",
		Short: "Shut down the daemon",
		Args:  exactArgs(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := spmHome()
			if err != nil {
				return err
			}
			cl := ipc.NewClient(ipc.Endpoint{Home: home})
			pingCtx, cancel := context.WithTimeout(cmd.Context(), time.Second)
			var ping ipc.PingResult
			err = cl.Call(pingCtx, "daemon.ping", nil, &ping)
			cancel()
			if err != nil {
				fmt.Println("daemon not running")
				return nil
			}
			var res map[string]any
			if err := cl.Call(cmd.Context(), "daemon.shutdown", ipc.ShutdownParams{All: all}, &res); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(res)
			}
			fmt.Println("daemon shutting down")
			return nil
		},
	}
	c.Flags().BoolVar(&all, "all", false, "stop all apps before shutdown")
	return c
}

func newHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health <name>",
		Short: "Pass through the actuator health JSON",
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
			var res json.RawMessage
			if err := cl.Call(cmd.Context(), "app.health", ipc.NameParams{Namespace: ns, Name: args[0]}, &res); err != nil {
				return err
			}
			var v any
			if err := json.Unmarshal(res, &v); err != nil {
				fmt.Println(string(res))
				return nil
			}
			return printJSON(v)
		},
	}
}

func newDaemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "daemon",
		Short:  "Run the spm4a daemon (normally auto-spawned)",
		Hidden: true,
		Args:   exactArgs(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := spmHome()
			if err != nil {
				return err
			}
			return daemon.Run(home)
		},
	}
}
