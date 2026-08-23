package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithCORS_AllowsDirectWebBearerRequests(t *testing.T) {
	handler := withCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), []string{"http://localhost:3005"})

	request := httptest.NewRequest(http.MethodOptions, "/config.v1.ConfigService/ListNamespaces", nil)
	request.Header.Set("Origin", "http://localhost:3005")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "authorization,connect-protocol-version,content-type")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, "http://localhost:3005", response.Header().Get("Access-Control-Allow-Origin"))
	allowedHeaders := strings.ToLower(response.Header().Get("Access-Control-Allow-Headers"))
	assert.Contains(t, allowedHeaders, "authorization")
	assert.Contains(t, allowedHeaders, "connect-protocol-version")
	assert.Contains(t, allowedHeaders, "content-type")
}

func TestWithCORS_RejectsUnknownWebOrigin(t *testing.T) {
	handler := withCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), []string{"http://localhost:3005"})

	request := httptest.NewRequest(http.MethodOptions, "/config.v1.ConfigService/ListNamespaces", nil)
	request.Header.Set("Origin", "https://attacker.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.NotEqual(t, "https://attacker.example.com", response.Header().Get("Access-Control-Allow-Origin"))
}
