package inject

import (
	"strings"
	"testing"
)

func TestJdwpAgentSyntax(t *testing.T) {
	// JDK >= 9 binds *:<port>
	got := JdwpAgent(5005, 17)
	if !strings.Contains(got, "address=*:5005") {
		t.Errorf("JDK17 -> %q, want address=*:5005", got)
	}
	// JDK 8 is localhost-only
	got = JdwpAgent(5005, 8)
	if !strings.Contains(got, "address=5005") || strings.Contains(got, "*:") {
		t.Errorf("JDK8 -> %q, want address=5005 without *:", got)
	}
	// unknown major assumes modern
	got = JdwpAgent(5005, 0)
	if !strings.Contains(got, "address=*:5005") {
		t.Errorf("unknown major -> %q, want address=*:5005", got)
	}
	// full flag shape
	got = JdwpAgent(3999, 21)
	want := "-agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=*:3999"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildSpringEnvUnion(t *testing.T) {
	env := map[string]string{
		AppJSONEnv: `{"management.endpoints.web.exposure.include":"metrics","server.port":"9999"}`,
	}
	out, injected, err := BuildSpringEnv(env, 8080)
	if err != nil {
		t.Fatal(err)
	}
	// user server.port wins -> not injected
	if _, ok := injected["server.port"]; ok {
		t.Error("user server.port should win; must not appear in injected")
	}
	// exposure union
	inc, _ := injected["management.endpoints.web.exposure.include"].(string)
	if !strings.Contains(inc, "metrics") || !strings.Contains(inc, "health") || !strings.Contains(inc, "shutdown") {
		t.Errorf("exposure union = %q, want metrics+health+shutdown", inc)
	}
	if injected["management.endpoint.shutdown.enabled"] != true {
		t.Error("shutdown.enabled should be injected true")
	}
	if injected["server.shutdown"] != "graceful" {
		t.Error("server.shutdown should be injected graceful")
	}
	if !strings.Contains(out[AppJSONEnv], `"server.port":"9999"`) {
		t.Errorf("merged JSON lost user value: %s", out[AppJSONEnv])
	}
}

func TestBuildSpringEnvInvalidExisting(t *testing.T) {
	env := map[string]string{AppJSONEnv: "{not json"}
	if _, _, err := BuildSpringEnv(env, 8080); err == nil {
		t.Error("expected error for invalid existing SPRING_APPLICATION_JSON")
	}
}
