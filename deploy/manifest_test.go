package deploy_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestPreConfigWebUsesPublicAPIHost(t *testing.T) {
	config := readFile(t, "pre/config/web-configmap.yaml")
	match := regexp.MustCompile(`"apiUrl"\s*:\s*"([^"]+)"`).FindStringSubmatch(config)
	if len(match) != 2 {
		t.Fatal("pre config web ConfigMap has no apiUrl")
	}
	if got, want := match[1], "https://config-api.apikv.com"; got != want {
		t.Fatalf("pre config web apiUrl = %q, want %q", got, want)
	}

	route := readFile(t, "pre/config/httproute.yaml")
	for _, hostname := range []string{`"config.apikv.com"`, `"config-api.apikv.com"`} {
		if !strings.Contains(route, hostname) {
			t.Errorf("pre config HTTPRoute is missing hostname %s", hostname)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
