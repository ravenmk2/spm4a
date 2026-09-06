package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"spm4a/internal/jdk"
)

type jdkView struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Major   int    `json:"major"`
	Home    string `json:"home"`
	Default bool   `json:"default,omitempty"` // PATH 上的默认 java
}

func newJdkCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "jdk",
		Short: "Manage the local JDK registry (no daemon involved)",
		Args:  exactArgs(0, ""),
	}
	c.AddCommand(newJdkScanCmd(), newJdkLsCmd(), newJdkAddCmd())
	return c
}

func jdkRegistry() (*jdk.Registry, error) {
	home, err := spmHome()
	if err != nil {
		return nil, err
	}
	return jdk.Load(home)
}

func newJdkScanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "Scan common locations for JDKs and register them",
		Args:  exactArgs(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := jdkRegistry()
			if err != nil {
				return err
			}
			added, updated, err := reg.Scan(os.Environ())
			if err != nil {
				return err
			}
			if err := printJdks(reg); err != nil {
				return err
			}
			if !flagJSON {
				fmt.Printf("scan: %d added, %d updated\n", added, updated)
			}
			return nil
		},
	}
}

func newJdkLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List registered JDKs",
		Args:  exactArgs(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := jdkRegistry()
			if err != nil {
				return err
			}
			return printJdks(reg)
		},
	}
}

func newJdkAddCmd() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "add <path>",
		Short: "Register a JDK home (probes java -version)",
		Args:  exactArgs(1, "<path>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := jdkRegistry()
			if err != nil {
				return err
			}
			e, err := reg.Add(args[0], name)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(e)
			}
			fmt.Printf("%s: %s (major %d) at %s\n", e.Name, e.Version, e.Major, e.Home)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "explicit registry name (overrides same-name entry)")
	return c
}

func printJdks(reg *jdk.Registry) error {
	defaultHome := pathJavaHome()
	views := make([]jdkView, 0, len(reg.Entries))
	for _, e := range reg.Entries {
		views = append(views, jdkView{
			Name: e.Name, Version: e.Version, Major: e.Major, Home: e.Home,
			Default: defaultHome != "" && samePath(e.Home, defaultHome),
		})
	}
	if flagJSON {
		return printJSON(map[string]any{"jdks": views})
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tMAJOR\tHOME")
	for _, v := range views {
		name := v.Name
		if v.Default {
			name += "*"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", name, v.Version, v.Major, v.Home)
	}
	_ = w.Flush()
	if len(views) > 0 && defaultHome != "" {
		fmt.Println("(* = java on PATH)")
	}
	return nil
}

// pathJavaHome resolves the JDK home of the java on PATH.
func pathJavaHome() string {
	p, err := exec.LookPath("java")
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Dir(filepath.Dir(p))
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 == nil {
		a = ra
	}
	if err2 == nil {
		b = rb
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
