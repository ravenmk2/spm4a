package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"spm4a/internal/state"
)

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printAppTable(apps []*state.App) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAMESPACE\tNAME\tLAUNCHER\tSTATUS\tPORT\tPID\tRESTARTS")
	for _, a := range apps {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%d\n",
			a.Spec.Namespace, a.Spec.Name, a.Spec.Launcher, a.Status, a.ActualPort, a.PID, a.Restarts)
	}
	_ = w.Flush()
}

func printAppDetail(a *state.App) {
	out := func(k string, v any) { fmt.Printf("%-14s %v\n", k+":", v) }
	out("name", a.Spec.Name)
	out("namespace", a.Spec.Namespace)
	out("status", a.Status)
	out("pid", a.PID)
	out("port", a.ActualPort)
	if a.DebugPort != 0 {
		out("debugPort", a.DebugPort)
	}
	out("workdir", a.Spec.Workdir)
	out("launcher", a.Spec.Launcher)
	if a.JavaBin != "" {
		out("java", a.JavaBin)
	}
	if a.ResolvedJar != "" {
		out("jar", a.ResolvedJar)
	}
	if len(a.ResolvedJvmOpts) > 0 {
		out("jvmOpts", strings.Join(a.ResolvedJvmOpts, " "))
	}
	out("healthPath", a.Spec.HealthPath)
	if a.LogPath != "" {
		out("log", a.LogPath)
	}
	if len(a.Injected) > 0 {
		out("injected", a.Injected)
	}
	out("restarts", a.Restarts)
	if a.LastExit != nil {
		out("lastExit", fmt.Sprintf("code %d at %s", a.LastExit.Code, a.LastExit.At.Format(time.RFC3339)))
	}
}
