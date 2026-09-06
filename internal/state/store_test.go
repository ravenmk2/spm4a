package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testApp(name string) *App {
	return &App{Spec: Spec{Name: name, Namespace: "default", Workdir: "/x", Launcher: "jar", Port: 8080}, Status: StatusStopped}
}

func TestLoadLegacyBareArray(t *testing.T) {
	dir := t.TempDir()
	nsDir := filepath.Join(dir, "shop")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `[{"spec":{"name":"order","namespace":"shop","workdir":"/x","launcher":"jar","port":8080},"status":"stopped"}]`
	if err := os.WriteFile(filepath.Join(nsDir, "state.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	app, ok := s.Get("shop", "order")
	if !ok {
		t.Fatal("legacy format: app not loaded")
	}
	if app.Spec.Port != 8080 {
		t.Errorf("port = %d, want 8080", app.Spec.Port)
	}

	// saving migrates to the wrapped format
	app.Status = StatusReady
	if err := s.Save("shop"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(nsDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f stateFile
	if err := json.Unmarshal(data, &f); err != nil || len(f.Apps) != 1 {
		t.Fatalf("saved file is not the wrapped format: %v\n%s", err, data)
	}
	if f.Apps[0].Status != StatusReady {
		t.Errorf("migrated app status = %q, want ready", f.Apps[0].Status)
	}
}

func TestLoadWrappedFormat(t *testing.T) {
	dir := t.TempDir()
	nsDir := filepath.Join(dir, "mall")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapped := `{"apps":[{"spec":{"name":"pay","namespace":"mall","workdir":"/y","launcher":"maven"},"status":"ready","pid":123}]}`
	if err := os.WriteFile(filepath.Join(nsDir, "state.json"), []byte(wrapped), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	app, ok := s.Get("mall", "pay")
	if !ok || app.PID != 123 || app.Spec.Launcher != "maven" {
		t.Fatalf("wrapped format not loaded: %+v ok=%v", app, ok)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(testApp("a1")); err != nil {
		t.Fatal(err)
	}
	s2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Get("default", "a1"); !ok {
		t.Fatal("round trip lost the app")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "default", "state.json"))
	if !json.Valid(data) || string(data)[0] != '{' {
		t.Errorf("saved file must be a JSON object, got: %.40s", data)
	}
}
