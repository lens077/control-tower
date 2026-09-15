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

// 2026-09-15 起集群统一从 TCR 拉镜像（GHCR 匿名拉取在节点上不稳定，见 AGENTS.md「部署现状」）。
// TCR 仓库是 private，所以每份 Deployment 都必须带 tcr-pull 拉取凭据，缺一份就是 ImagePullBackOff。
const tcrImagePrefix = "image: ccr.ccs.tencentyun.com/sumery/control-tower-"

var deploymentManifests = []string{
	"config/deployment.yaml",
	"config/web-deployment.yaml",
	"gateway/deployment.yaml",
}

func TestDeploymentImagesUseTCR(t *testing.T) {
	for _, environment := range []string{"dev", "pre"} {
		for _, deployment := range deploymentManifests {
			manifest := readFile(t, environment+"/"+deployment)
			if !strings.Contains(manifest, tcrImagePrefix) {
				t.Errorf("%s/%s is not pulling from TCR (%s...)", environment, deployment, tcrImagePrefix)
			}
			if strings.Contains(manifest, "ghcr.io/") {
				t.Errorf("%s/%s still references GHCR", environment, deployment)
			}
		}
	}
}

func TestTCRDeploymentsCarryPullSecret(t *testing.T) {
	pattern := regexp.MustCompile(`(?m)^      imagePullSecrets:\n        - name: tcr-pull$`)
	for _, environment := range []string{"dev", "pre"} {
		for _, deployment := range deploymentManifests {
			manifest := readFile(t, environment+"/"+deployment)
			if !pattern.MatchString(manifest) {
				t.Errorf("%s/%s does not declare imagePullSecrets tcr-pull at pod spec level", environment, deployment)
			}
		}
	}
}

func TestConfigManifestsDoNotClaimGatewayDiscovery(t *testing.T) {
	for _, environment := range []string{"dev", "pre"} {
		config := readFile(t, environment+"/config/deployment.yaml")
		if strings.Contains(config, "discovery:///config-service") {
			t.Errorf("%s config deployment still claims gateway discovers config-service through Consul", environment)
		}
	}
}

func TestGatewayEntrypointsExistInBothOverlays(t *testing.T) {
	for _, environment := range []string{"dev", "pre"} {
		route := readFile(t, environment+"/gateway/httproute.yaml")
		for _, hostname := range []string{`"gateway.dev.test"`, `"gateway.apikv.com"`} {
			if !strings.Contains(route, hostname) {
				t.Errorf("%s gateway HTTPRoute is missing hostname %s", environment, hostname)
			}
		}

		service := readFile(t, environment+"/gateway/service.yaml")
		if !strings.Contains(service, "name: ecommerce-gateway-service") {
			t.Errorf("%s gateway Service has the wrong name", environment)
		}
		if !strings.Contains(service, "app: control-tower-gateway") {
			t.Errorf("%s gateway Service does not select control-tower-gateway", environment)
		}
	}
}

func TestPreGatewayDoesNotUseDevBFFSettings(t *testing.T) {
	manifest := readFile(t, "pre/gateway/deployment.yaml")
	for _, forbidden := range []string{
		"control-tower-config-source-dev",
		"SESSION_COOKIE_INSECURE",
		"http://localhost:3000",
	} {
		if strings.Contains(manifest, forbidden) {
			t.Errorf("pre gateway deployment contains dev-only setting %q", forbidden)
		}
	}
	if !strings.Contains(manifest, "secretName: control-tower-config-source-pre") {
		t.Error("pre gateway deployment does not use the pre config source Secret")
	}
}

func TestGatewayOTLPEndpointsUseCollector(t *testing.T) {
	const collector = `OTEL_EXPORTER_OTLP_ENDPOINT: "otel-opentelemetry-collector.opentelemetry.svc:4318"`
	for _, environment := range []string{"dev", "pre"} {
		manifest := readFile(t, environment+"/gateway/deployment.yaml")
		if !strings.Contains(manifest, collector) {
			t.Errorf("%s gateway does not send OTLP signals through the collector", environment)
		}
	}
}

func TestDeploymentModesMatchOverlay(t *testing.T) {
	pattern := regexp.MustCompile(`(?m)- name: DEPLOYMENT_MODE\n\s+value: "?([a-z]+)"?`)
	for _, environment := range []string{"dev", "pre"} {
		for _, service := range []string{"config", "gateway"} {
			manifest := readFile(t, environment+"/"+service+"/deployment.yaml")
			match := pattern.FindStringSubmatch(manifest)
			if len(match) != 2 {
				t.Errorf("%s/%s deployment has no DEPLOYMENT_MODE", environment, service)
				continue
			}
			if got := match[1]; got != environment {
				t.Errorf("%s/%s DEPLOYMENT_MODE = %q, want %q", environment, service, got, environment)
			}
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
