package constants

const (
	Host = "localhost"
	Port = "8080"
)

const (
	UserOwnerMetadataKey = "x-md-global-owner"
	UserNameMetadataKey  = "x-md-global-name"
	UserRoleMetadataKey  = "x-md-global-role"
	UserIDMetadataKey    = "x-md-global-user-id"
	ServiceTokenHeader   = "x-config-center-service-token"
	ClientNameHeader     = "x-config-center-client-name"
	ClientInstanceHeader = "x-config-center-client-instance"
	ClientVersionHeader  = "x-config-center-client-version"
)

const (
	FormatConsole = "console"
	FormatJSON    = "json"
)

const (
	SslModeDisable    = "disable"
	SslModeAllow      = "allow"
	SslModePrefer     = "prefer"
	SslModeVerifyCa   = "verify-ca"
	SslModeVerifyFull = "verify-full"
)

const (
	ConfigFilePath   = "configs/config.yaml"
	ConfigFileFormat = "yaml"
)

// Consul configs default values
const (
	ConsulAddr               = "127.0.0.1:8500"
	ConsulPath               = "/consul/"
	ConsulFileFormat         = "yaml"
	ConsulScheme             = "http"
	ConsulTlsScheme          = "https"
	ConsulInsecureSkipVerify = false
	ConsulToken              = ""
)

// Consul service tags
const (
	ConsulTagFx  = "fx"
	ConsulTagTtl = "ttl"
)
