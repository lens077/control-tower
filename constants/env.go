package constants

import (
	"os"
	"strconv"
)

// 使用常量名称作为映射
const (
	EnvServiceName    = "SERVICE_NAME"
	EnvServiceVersion = "SERVICE_VERSION"
	EnvDeploymentMode = "DEPLOYMENT_MODE"
	EnvConfigFile     = "CONFIG_FILE"
	// EnvConfigCenterServiceToken protects machine-to-machine GetKey/WatchKeys
	// calls that bypass the gateway. Supply it through the deployment secret.
	EnvConfigCenterServiceToken = "CONFIG_CENTER_SERVICE_TOKEN"
	// EnvCasdoorCertificateFile points at Casdoor's public certificate PEM.
	// Browser traffic is verified locally instead of trusting forwarded identity headers.
	EnvCasdoorCertificateFile = "CASDOOR_CERTIFICATE_FILE"
	// EnvCasdoorIssuer and EnvCasdoorAudience bind otherwise valid Casdoor tokens
	// to the expected identity provider and browser application.
	EnvCasdoorIssuer   = "CASDOOR_ISSUER"
	EnvCasdoorAudience = "CASDOOR_AUDIENCE"
)

// Consul
const (
	EnvConsulEnabled            = "CONSUL_ENABLED"
	EnvConsulAddr               = "CONSUL_ADDR"
	EnvConsulScheme             = "CONSUL_SCHEME"
	EnvConsulToken              = "CONSUL_TOKEN"
	EnvConsulInsecureSkipVerify = "CONSUL_INSECURE_SKIP_VERIFY"
	EnvConsulCaFile             = "CONSUL_CA_FILE"
	EnvConsulCertFile           = "CONSUL_CERT_FILE"
	EnvConsulKeyFile            = "CONSUL_KEY_FILE"
)

// GetEnvString 如果环境变量存在且不为空，则返回环境变量值，否则返回默认值
func GetEnvString(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists && value != "" {
		return value
	}
	return defaultValue
}

// GetEnvBool 处理布尔类型
func GetEnvBool(key string, defaultValue bool) bool {
	s, exists := os.LookupEnv(key)
	if !exists || s == "" {
		return defaultValue
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return defaultValue
	}
	return v
}
