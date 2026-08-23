package iam

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lens077/control-tower/constants"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const (
	testIssuer   = "https://identity.example.com"
	testAudience = "config-center-web"
)

func TestNewAuthorizer_RequiresIssuerAndAudienceWithCertificate(t *testing.T) {
	t.Setenv(constants.EnvCasdoorCertificateFile, "configured-casdoor.pem")
	t.Setenv(constants.EnvCasdoorIssuer, "")
	t.Setenv(constants.EnvCasdoorAudience, "")

	_, err := NewAuthorizer(zap.NewNop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), constants.EnvCasdoorIssuer)
	assert.Contains(t, err.Error(), constants.EnvCasdoorAudience)
}

func TestAuthorizer_AllowsServiceTokenOnlyForReadProcedures(t *testing.T) {
	authorizer := &Authorizer{serviceToken: []byte("reader-token"), log: zap.NewNop()}
	handler := authorizer.HTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		require.True(t, ok)
		assert.True(t, principal.Machine)
		w.WriteHeader(http.StatusNoContent)
	}))

	allowed := httptest.NewRequest(http.MethodPost, "/config.v1.ConfigService/GetKey", nil)
	allowed.Header.Set(constants.ServiceTokenHeader, "reader-token")
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	assert.Equal(t, http.StatusNoContent, allowedResponse.Code)

	denied := httptest.NewRequest(http.MethodPost, "/config.v1.ConfigService/PutKey", nil)
	denied.Header.Set(constants.ServiceTokenHeader, "reader-token")
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	assert.Equal(t, http.StatusForbidden, deniedResponse.Code)
	assert.Empty(t, deniedResponse.Header().Get("WWW-Authenticate"))
}

func TestAuthorizer_RequiresBoundAdminCasdoorToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	authorizer := &Authorizer{
		casdoorKey:       &key.PublicKey,
		expectedIssuer:   testIssuer,
		expectedAudience: testAudience,
		log:              zap.NewNop(),
	}
	handler := authorizer.HTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		require.True(t, ok)
		assert.Equal(t, "config-admin", principal.Name)
		w.WriteHeader(http.StatusNoContent)
	}))

	admin := httptest.NewRequest(http.MethodPost, "/config.v1.ConfigService/PutKey", nil)
	admin.Header.Set("Authorization", "Bearer "+signedToken(t, key, validClaims(map[string]any{
		"name": "config-admin", "isAdmin": true,
	})))
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, admin)
	assert.Equal(t, http.StatusNoContent, adminResponse.Code)

	member := httptest.NewRequest(http.MethodPost, "/config.v1.ConfigService/GetKey", nil)
	member.Header.Set("Authorization", "Bearer "+signedToken(t, key, validClaims(map[string]any{
		"name": "member", "role": "admin", "roles": []string{"admin"},
	})))
	memberResponse := httptest.NewRecorder()
	handler.ServeHTTP(memberResponse, member)
	assert.Equal(t, http.StatusForbidden, memberResponse.Code)

	wrongAudience := httptest.NewRequest(http.MethodPost, "/config.v1.ConfigService/GetKey", nil)
	wrongAudience.Header.Set("Authorization", "Bearer "+signedToken(t, key, validClaims(map[string]any{
		"name": "config-admin", "isAdmin": true, "aud": []string{"another-app"},
	})))
	wrongAudienceResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongAudienceResponse, wrongAudience)
	assert.Equal(t, http.StatusUnauthorized, wrongAudienceResponse.Code)
	assert.Equal(t, "Bearer", wrongAudienceResponse.Header().Get("WWW-Authenticate"))

	wrongIssuer := httptest.NewRequest(http.MethodPost, "/config.v1.ConfigService/GetKey", nil)
	wrongIssuer.Header.Set("Authorization", "Bearer "+signedToken(t, key, validClaims(map[string]any{
		"name": "config-admin", "isAdmin": true, "iss": "https://attacker.example.com",
	})))
	wrongIssuerResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongIssuerResponse, wrongIssuer)
	assert.Equal(t, http.StatusUnauthorized, wrongIssuerResponse.Code)
}

func TestAuthorizer_RejectsDisabledAccountsAndRefreshTokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	authorizer := &Authorizer{
		casdoorKey:       &key.PublicKey,
		expectedIssuer:   testIssuer,
		expectedAudience: testAudience,
		log:              zap.NewNop(),
	}
	handler := authorizer.HTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	disabled := httptest.NewRequest(http.MethodPost, "/config.v1.ConfigService/GetKey", nil)
	disabled.Header.Set("Authorization", "Bearer "+signedToken(t, key, validClaims(map[string]any{
		"isAdmin": true, "isForbidden": true,
	})))
	disabledResponse := httptest.NewRecorder()
	handler.ServeHTTP(disabledResponse, disabled)
	assert.Equal(t, http.StatusForbidden, disabledResponse.Code)

	refresh := httptest.NewRequest(http.MethodPost, "/config.v1.ConfigService/GetKey", nil)
	refresh.Header.Set("Authorization", "Bearer "+signedToken(t, key, validClaims(map[string]any{
		"isAdmin": true, "TokenType": "refresh-token",
	})))
	refreshResponse := httptest.NewRecorder()
	handler.ServeHTTP(refreshResponse, refresh)
	assert.Equal(t, http.StatusUnauthorized, refreshResponse.Code)
}

func validClaims(overrides map[string]any) map[string]any {
	claims := map[string]any{
		"iss": testIssuer,
		"aud": []string{testAudience},
		"exp": time.Now().Add(time.Minute).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}
	for key, value := range overrides {
		claims[key] = value
	}
	return claims
}

func signedToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	require.NoError(t, err)
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}
