package inject

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const AppJSONEnv = "SPRING_APPLICATION_JSON"

// JdwpAgent returns the §8.3 JDWP agent flag. JDK >= 9 binds all interfaces
// with address=*:<port>; JDK 8 only supports address=<port> (localhost).
// major == 0 (unknown) assumes a modern JDK.
func JdwpAgent(port, major int) string {
	addr := "*:" + strconv.Itoa(port)
	if major > 0 && major < 9 {
		addr = strconv.Itoa(port)
	}
	return "-agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=" + addr
}

const exposureIncludeKey = "management.endpoints.web.exposure.include"

var exposureDefaults = []string{"health", "shutdown"}

// BuildSpringEnv merges the §8.2 injection list into env's
// SPRING_APPLICATION_JSON: user keys win; the exposure include list is the
// union of the user's value and health,shutdown (deduped). Returns the
// updated env map and the injected subset actually applied (auditable in
// spm4a status).
func BuildSpringEnv(env map[string]string, port int) (map[string]string, map[string]any, error) {
	out := make(map[string]string, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	merged := map[string]any{}
	if existing := env[AppJSONEnv]; existing != "" {
		if err := json.Unmarshal([]byte(existing), &merged); err != nil {
			return nil, nil, fmt.Errorf("existing %s is not valid JSON: %w", AppJSONEnv, err)
		}
	}
	injected := map[string]any{}
	setIfAbsent := func(key string, v any) {
		if _, ok := merged[key]; !ok {
			merged[key] = v
			injected[key] = v
		}
	}

	setIfAbsent("server.port", port)

	if cur, _ := merged[exposureIncludeKey].(string); cur != "" {
		parts := splitTrim(cur)
		added := false
		for _, want := range exposureDefaults {
			if !containsFold(parts, want) {
				parts = append(parts, want)
				added = true
			}
		}
		if added {
			union := strings.Join(parts, ",")
			merged[exposureIncludeKey] = union
			injected[exposureIncludeKey] = union
		}
	} else {
		merged[exposureIncludeKey] = "health,shutdown"
		injected[exposureIncludeKey] = "health,shutdown"
	}

	setIfAbsent("management.endpoint.shutdown.enabled", true)
	setIfAbsent("server.shutdown", "graceful")

	b, err := json.Marshal(merged)
	if err != nil {
		return nil, nil, err
	}
	out[AppJSONEnv] = string(b)
	return out, injected, nil
}

func splitTrim(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
