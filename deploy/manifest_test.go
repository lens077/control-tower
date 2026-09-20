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
	// 只看生效字段：注释里必须能写出这些值（"**不设** SESSION_COOKIE_INSECURE"、
	// 事故记录里的 localhost 回调地址），否则为了绕过本测试就得把理由删掉——
	// 规则能从代码读出来，理由不能。
	manifest := stripYAMLComments(readFile(t, "pre/gateway/deployment.yaml"))
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

// 上面那条只证明 pre **没有** dev 的值，缺整块 BFF 配置时它一样全绿 —— 2026-09-20
// 就是这么漏的：pre 压根没写 BFF 环境变量，集群里跑的是 dev 那份，线上授权 URL 的
// redirect_uri 成了 http://localhost:3000/auth/callback，登录整条不可用。
// 缺配置的症状是**静默降级**（main.go 四项不齐就关掉会话轨，站点没有登录入口，不报错），
// 所以必须正面断言"存在且是公网值"，不能只断言"不含 dev 值"。
func TestPreGatewayEnablesBFFWithPublicOrigins(t *testing.T) {
	manifest := readFile(t, "pre/gateway/deployment.yaml")
	// main.go 要求齐备的四项，缺一项整条会话轨关闭。
	for _, required := range []string{
		"- name: SESSION_REDIS_ADDR",
		"- name: CASDOOR_CLIENT_ID",
		"- name: CASDOOR_CLIENT_SECRET",
		"- name: BFF_PUBLIC_BASE_URL",
	} {
		if !strings.Contains(manifest, required) {
			t.Errorf("pre gateway deployment is missing %q —— BFF 会话轨会静默关闭", required)
		}
	}
	// redirect_uri = BFF_PUBLIC_BASE_URL + "/auth/callback"，必须是网关自己的公网源。
	pattern := regexp.MustCompile(`(?m)- name: BFF_PUBLIC_BASE_URL\n\s+value: "?(\S+?)"?\n`)
	match := pattern.FindStringSubmatch(manifest)
	if len(match) != 2 {
		t.Fatal("pre gateway deployment has no literal BFF_PUBLIC_BASE_URL value")
	}
	if got, want := match[1], "https://gateway.apikv.com"; got != want {
		t.Errorf("pre BFF_PUBLIC_BASE_URL = %q, want %q", got, want)
	}
	// 相对路径会按网关的源解析，所以前端源必须显式列白名单。
	if !strings.Contains(manifest, "value: https://shop.apikv.com") {
		t.Error("pre gateway does not allow https://shop.apikv.com as a post-login redirect")
	}
	// 会话存储走 TLS，CA 必须真的挂进容器，否则启动期 Ping 直接失败。
	if !strings.Contains(manifest, "mountPath: /etc/control-tower/session-tls") {
		t.Error("pre gateway declares SESSION_REDIS_CA_FILE but never mounts the CA")
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

// stripYAMLComments 去掉整行注释，只留生效字段。
// 不处理行内 `#`：本仓清单里没有行内注释，为此引 YAML 解析器不值当。
func stripYAMLComments(manifest string) string {
	kept := make([]string, 0, strings.Count(manifest, "\n")+1)
	for _, line := range strings.Split(manifest, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
