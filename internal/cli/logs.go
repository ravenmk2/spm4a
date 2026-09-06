package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"spm4a/internal/ipc"
)

func newLogsCmd() *cobra.Command {
	var follow bool
	var lines int
	c := &cobra.Command{
		Use:   "logs <name>",
		Short: "Show app logs (-f to follow)",
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
			params := ipc.LogsParams{Namespace: ns, Name: args[0], Lines: lines}
			if !follow {
				var res struct {
					Lines []string `json:"lines"`
				}
				if err := cl.Call(cmd.Context(), "logs.get", params, &res); err != nil {
					return err
				}
				if flagJSON {
					return printJSON(res)
				}
				for _, l := range res.Lines {
					fmt.Println(l)
				}
				return nil
			}

			sub, err := cl.Stream(cmd.Context(), "logs.follow", params)
			if err != nil {
				return err
			}
			defer sub.Close()
			go func() {
				<-cmd.Context().Done()
				sub.Close()
			}()
			for ev := range sub.Events {
				if flagJSON {
					fmt.Println(string(ev.Params))
					continue
				}
				var p struct {
					Data string `json:"data"`
				}
				if err := json.Unmarshal(ev.Params, &p); err == nil {
					fmt.Print(p.Data)
				}
			}
			return nil
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	c.Flags().IntVar(&lines, "lines", 200, "number of recent lines to show")
	return c
}
