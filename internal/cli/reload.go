package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"spm4a/internal/ipc"
)

func newReloadCmd() *cobra.Command {
	var timeout time.Duration
	c := &cobra.Command{
		Use:   "reload <name>",
		Short: "Compile in workdir to trigger a devtools hot restart (maven/gradle apps)",
		Args:  exactArgs(1, "<name>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ns, err := resolveNamespace("")
			if err != nil {
				return err
			}
			cl, err := rpcClient(cmd.Context())
			if err != nil {
				return err
			}
			var res ipc.ReloadResult
			if err := cl.Call(cmd.Context(), "app.reload", ipc.ReloadParams{
				Namespace: ns, Name: args[0], Timeout: timeout.String(),
			}, &res); err != nil {
				return err
			}
			if flagJSON {
				if err := printJSON(res); err != nil {
					return err
				}
			} else {
				if res.Output != "" {
					fmt.Print(res.Output)
				}
				fmt.Printf("%s: build exit %d, status %s, health %v\n",
					args[0], res.ExitCode, res.AppStatus, healthStatusOf(res.Health))
			}
			if res.ExitCode != 0 {
				return fmt.Errorf("build failed with exit code %d", res.ExitCode)
			}
			return nil
		},
	}
	c.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "build + ready wait timeout")
	return c
}

func healthStatusOf(h any) string {
	if m, ok := h.(map[string]any); ok {
		if s, ok := m["status"].(string); ok {
			return s
		}
	}
	return "unknown"
}
