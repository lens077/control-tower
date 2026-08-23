package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	conf "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// selfSigned 造一对自签证书/私钥的 PEM。用真证书而不是假字符串:
// 这层要验的正是「PEM 解析得对不对」,喂假串只能测到错误分支。
func selfSigned(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "config-center-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	kb, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

func TestBuildTLSConfig_DisabledByDefault(t *testing.T) {
	// nil 与 enable:false 都必须返回 nil —— 集群默认走这条路(边缘终止 TLS)
	got, err := buildTLSConfig(nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = buildTLSConfig(&conf.Server_Tls{Enable: false})
	require.NoError(t, err)
	assert.Nil(t, got, "enable:false 时即便填了证书也不该启用")
}

func TestBuildTLSConfig_Enabled(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)

	got, err := buildTLSConfig(&conf.Server_Tls{
		Enable:  true,
		CertPem: certPEM,
		KeyPem:  keyPEM,
	})
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Len(t, got.Certificates, 1)
	assert.Equal(t, uint16(tls.VersionTLS12), got.MinVersion)
	// 少了 h2 的话 connect 客户端会静默退回 HTTP/1.1 —— 能跑但丢掉多路复用,
	// 属于「看不出来的退化」,所以显式断言
	assert.Equal(t, []string{"h2", "http/1.1"}, got.NextProtos)
	// 没给 client CA 就是单向 TLS,不能顺手要求客户端证书
	assert.Equal(t, tls.NoClientCert, got.ClientAuth)
	assert.Nil(t, got.ClientCAs)
}

// 缺证书/私钥必须报错而不是退化成明文 —— 「配置说加密了实际没有」
// 比起不来危险得多,因为它看起来一切正常。
func TestBuildTLSConfig_EnabledWithoutCertFails(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)

	cases := map[string]*conf.Server_Tls{
		"两者都缺":    {Enable: true},
		"只有证书":    {Enable: true, CertPem: certPEM},
		"只有私钥":    {Enable: true, KeyPem: keyPEM},
		"证书不是PEM": {Enable: true, CertPem: "not-a-pem", KeyPem: keyPEM},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := buildTLSConfig(c)
			require.Error(t, err, "必须报错,绝不能返回 nil,nil 让调用方以为是「未启用」")
			assert.Nil(t, got)
		})
	}
}

func TestBuildTLSConfig_MTLS(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)
	caPEM, _ := selfSigned(t)

	t.Run("给了 CA 但不强制:灰度期,带证书的校验、不带的放行", func(t *testing.T) {
		got, err := buildTLSConfig(&conf.Server_Tls{
			Enable: true, CertPem: certPEM, KeyPem: keyPEM,
			ClientCaPem: caPEM,
		})
		require.NoError(t, err)
		require.NotNil(t, got.ClientCAs)
		assert.Equal(t, tls.VerifyClientCertIfGiven, got.ClientAuth)
	})

	t.Run("强制:真正的 mTLS", func(t *testing.T) {
		got, err := buildTLSConfig(&conf.Server_Tls{
			Enable: true, CertPem: certPEM, KeyPem: keyPEM,
			ClientCaPem: caPEM, RequireClientCert: true,
		})
		require.NoError(t, err)
		assert.Equal(t, tls.RequireAndVerifyClientCert, got.ClientAuth)
	})

	t.Run("要求客户端证书却没给 CA:必须报错", func(t *testing.T) {
		// 这是最危险的一种配置错误:看起来开了 mTLS,实际上没有任何 CA
		// 可以校验,Go 会拒绝所有连接或放行所有连接(取决于版本),两种都是坑
		_, err := buildTLSConfig(&conf.Server_Tls{
			Enable: true, CertPem: certPEM, KeyPem: keyPEM,
			RequireClientCert: true,
		})
		require.Error(t, err)
	})

	t.Run("CA 不是合法 PEM:必须报错", func(t *testing.T) {
		_, err := buildTLSConfig(&conf.Server_Tls{
			Enable: true, CertPem: certPEM, KeyPem: keyPEM,
			ClientCaPem: "garbage",
		})
		require.Error(t, err)
	})
}
