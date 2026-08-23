// Package iam verifies the human and machine identities allowed to access configuration.
package iam

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lens077/control-tower/constants"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

var Module = fx.Module("iam", fx.Provide(NewAuthorizer))

type principalContextKey struct{}

// Principal is the verified request identity made available to service methods.
type Principal struct {
	Name    string
	Machine bool
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

type Authorizer struct {
	serviceToken     []byte
	casdoorKey       *rsa.PublicKey
	expectedIssuer   string
	expectedAudience string
	log              *zap.Logger
}

type authorizationError struct {
	status int
	reason string
}

func (e *authorizationError) Error() string { return e.reason }

func unauthorized(reason string) error {
	return &authorizationError{status: http.StatusUnauthorized, reason: reason}
}

func forbidden(reason string) error {
	return &authorizationError{status: http.StatusForbidden, reason: reason}
}

// NewAuthorizer loads Casdoor's public certificate and validation binding from
// the local process environment; no gateway or user-service call is involved.
func NewAuthorizer(logger *zap.Logger) (*Authorizer, error) {
	authorizer := &Authorizer{
		serviceToken: []byte(os.Getenv(constants.EnvConfigCenterServiceToken)),
		log:          logger.Named("iam"),
	}

	certificateFile := os.Getenv(constants.EnvCasdoorCertificateFile)
	if certificateFile == "" {
		authorizer.log.Warn("Casdoor certificate is not configured; browser IAM is disabled")
		return authorizer, nil
	}
	authorizer.expectedIssuer = strings.TrimSuffix(strings.TrimSpace(os.Getenv(constants.EnvCasdoorIssuer)), "/")
	authorizer.expectedAudience = strings.TrimSpace(os.Getenv(constants.EnvCasdoorAudience))
	if authorizer.expectedIssuer == "" || authorizer.expectedAudience == "" {
		return nil, fmt.Errorf("%s and %s are required when browser IAM is enabled",
			constants.EnvCasdoorIssuer, constants.EnvCasdoorAudience)
	}

	contents, err := os.ReadFile(certificateFile)
	if err != nil {
		return nil, fmt.Errorf("read Casdoor certificate %q: %w", certificateFile, err)
	}
	key, err := parseRSAPublicKey(contents)
	if err != nil {
		return nil, fmt.Errorf("parse Casdoor certificate %q: %w", certificateFile, err)
	}
	authorizer.casdoorKey = key
	return authorizer, nil
}

func (a *Authorizer) HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		principal, err := a.authorize(r)
		if err != nil {
			status := http.StatusUnauthorized
			var denied *authorizationError
			if errors.As(err, &denied) {
				status = denied.status
			}
			a.log.Warn("configuration request denied", zap.String("path", r.URL.Path), zap.Int("status", status), zap.Error(err))
			if status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", "Bearer")
			}
			http.Error(w, http.StatusText(status), status)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
	})
}

func (a *Authorizer) authorize(r *http.Request) (Principal, error) {
	if candidate := r.Header.Get(constants.ServiceTokenHeader); candidate != "" {
		if len(a.serviceToken) == 0 || !hmac.Equal([]byte(candidate), a.serviceToken) {
			return Principal{}, unauthorized("invalid service token")
		}
		if !machineReadProcedure(r.URL.Path) {
			return Principal{}, forbidden("service token cannot mutate configuration")
		}
		return Principal{Name: "service", Machine: true}, nil
	}

	if a.casdoorKey == nil {
		return Principal{}, unauthorized("Casdoor certificate is not configured")
	}
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		return Principal{}, unauthorized("missing bearer token")
	}
	claims, err := verifyCasdoorToken(
		strings.TrimPrefix(authorization, "Bearer "),
		a.casdoorKey,
		a.expectedIssuer,
		a.expectedAudience,
	)
	if err != nil {
		return Principal{}, unauthorized(err.Error())
	}
	if boolClaim(claims, "isForbidden") || boolClaim(claims, "isDeleted") {
		return Principal{}, forbidden("Casdoor account is disabled")
	}
	if !isAdmin(claims) {
		return Principal{}, forbidden("Casdoor account is not an administrator")
	}
	return Principal{Name: claimName(claims), Machine: false}, nil
}

func machineReadProcedure(path string) bool {
	switch path {
	case "/config.v1.ConfigService/GetKey", "/config.v1.ConfigService/WatchKeys":
		return true
	default:
		return false
	}
}

func parseRSAPublicKey(contents []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(contents)
	if block == nil {
		return nil, errors.New("invalid PEM")
	}
	if certificate, err := x509.ParseCertificate(block.Bytes); err == nil {
		key, ok := certificate.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("certificate public key is not RSA")
		}
		return key, nil
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not RSA")
	}
	return rsaKey, nil
}

func verifyCasdoorToken(raw string, key *rsa.PublicKey, expectedIssuer, expectedAudience string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed bearer token")
	}
	header, err := decodeJWTPart(parts[0])
	if err != nil {
		return nil, err
	}
	if header["alg"] != "RS256" {
		return nil, errors.New("unsupported JWT signing algorithm")
	}
	payload, err := decodeJWTPart(parts[1])
	if err != nil {
		return nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode JWT signature: %w", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return nil, errors.New("invalid JWT signature")
	}

	now := time.Now().Unix()
	if expiration, ok := numberClaim(payload, "exp"); !ok || now >= expiration {
		return nil, errors.New("expired JWT")
	}
	if notBefore, ok := numberClaim(payload, "nbf"); ok && now+60 < notBefore {
		return nil, errors.New("JWT is not active yet")
	}
	if issuedAt, ok := numberClaim(payload, "iat"); ok && now+60 < issuedAt {
		return nil, errors.New("JWT was issued in the future")
	}
	issuer, ok := payload["iss"].(string)
	if !ok || issuer != expectedIssuer {
		return nil, errors.New("unexpected JWT issuer")
	}
	if !audienceContains(payload["aud"], expectedAudience) {
		return nil, errors.New("unexpected JWT audience")
	}
	if payload["tokenType"] == "refresh-token" || payload["TokenType"] == "refresh-token" {
		return nil, errors.New("refresh token cannot authenticate API requests")
	}
	return payload, nil
}

func decodeJWTPart(part string) (map[string]any, error) {
	contents, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return nil, fmt.Errorf("decode JWT: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode JWT JSON: %w", err)
	}
	return result, nil
}

func numberClaim(claims map[string]any, key string) (int64, bool) {
	number, ok := claims[key].(json.Number)
	if !ok {
		return 0, false
	}
	value, err := number.Int64()
	return value, err == nil
}

func boolClaim(claims map[string]any, key string) bool {
	value, _ := claims[key].(bool)
	return value
}

func audienceContains(claim any, expected string) bool {
	switch value := claim.(type) {
	case string:
		return value == expected
	case []any:
		for _, item := range value {
			if audience, ok := item.(string); ok && audience == expected {
				return true
			}
		}
	}
	return false
}

func isAdmin(claims map[string]any) bool {
	// Casdoor's signed isAdmin claim is the authority. Unscoped role names can
	// collide across organizations and applications, so they are not accepted.
	return boolClaim(claims, "isAdmin")
}

func claimName(claims map[string]any) string {
	for _, key := range []string{"name", "preferred_username", "id", "sub"} {
		if value, ok := claims[key].(string); ok && value != "" {
			return value
		}
	}
	return "casdoor-admin"
}
