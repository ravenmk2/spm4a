package cli

import (
	"context"
	"fmt"
	"os"

	"spm4a/internal/ipc"
	"spm4a/internal/state"
)

// batchApps lists the apps targeted by a --all command: the current
// namespace, or every namespace with -A/--all-namespace.
func batchApps(ctx context.Context, cl *ipc.Client) ([]*state.App, error) {
	p := ipc.ListParams{All: flagAllNs}
	if !flagAllNs {
		ns, err := resolveNamespace("")
		if err != nil {
			return nil, err
		}
		p.Namespace = ns
	}
	var res struct {
		Apps []*state.App `json:"apps"`
	}
	if err := cl.Call(ctx, "app.list", p, &res); err != nil {
		return nil, err
	}
	return res.Apps, nil
}

type batchResult struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Skipped   bool   `json:"skipped,omitempty"`
	Err       string `json:"error,omitempty"`
}

// reportBatch prints per-app results (JSON array or one line each) and
// returns a generic error (exit 1) when any app failed.
func reportBatch(verb string, results []*batchResult) error {
	failed := 0
	for _, r := range results {
		if !r.OK && !r.Skipped {
			failed++
		}
	}
	if flagJSON {
		_ = printJSON(map[string]any{"results": results})
	} else if len(results) == 0 {
		fmt.Println("no apps")
	} else {
		for _, r := range results {
			switch {
			case r.Skipped:
				fmt.Printf("%s/%s: skipped (running)\n", r.Namespace, r.Name)
			case r.OK:
				fmt.Printf("%s/%s: %s\n", r.Namespace, r.Name, verb)
			default:
				fmt.Fprintf(os.Stderr, "%s/%s: %s\n", r.Namespace, r.Name, r.Err)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d app(s) failed", failed)
	}
	return nil
}
