package cli

import (
	"github.com/spf13/cobra"

	"spm4a/internal/ipc"
	"spm4a/internal/state"
)

func newLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List apps in the current namespace (-A for all)",
		Args:  exactArgs(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ns, err := resolveNamespace("")
			if err != nil {
				return err
			}
			cl, err := rpcClient(cmd.Context())
			if err != nil {
				return err
			}
			var res struct {
				Apps []*state.App `json:"apps"`
			}
			if err := cl.Call(cmd.Context(), "app.list", ipc.ListParams{Namespace: ns, All: flagAllNs}, &res); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(res)
			}
			printAppTable(res.Apps)
			return nil
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <name>",
		Short: "Show app status",
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
			if err := cl.Call(cmd.Context(), "app.status", ipc.NameParams{Namespace: ns, Name: args[0]}, &res); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(res)
			}
			printAppDetail(res.App)
			return nil
		},
	}
}
