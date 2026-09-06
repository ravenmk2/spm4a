package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"spm4a/internal/tui"
)

func newTuiCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI",
		Args:  exactArgs(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
				return fmt.Errorf("spm4a tui requires an interactive terminal (TTY)")
			}
			ns, err := resolveNamespace("")
			if err != nil {
				return err
			}
			cl, err := rpcClient(cmd.Context())
			if err != nil {
				return err
			}
			return tui.Run(cmd.Context(), tui.NewIPCClient(cl), ns, all)
		},
	}
	c.Flags().BoolVarP(&all, "all", "A", false, "show apps across all namespaces")
	return c
}
