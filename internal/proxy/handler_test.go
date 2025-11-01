package proxy

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuthMiddleware_PathHandling тестирует обработку специальных путей
func TestAuthMiddleware_PathHandling(t *testing.T) {
	prx.global.maxReqBodySizeInt64 = 100

	tests := []struct {
		name           string
		path           string
		method         string
		body           string
		expectNextCall bool
		expectStatus   int
	}{
		{
			name:           "metrics path - should call next",
			path:           "/metrics",
			method:         "GET",
			body:           "",
			expectNextCall: true,
			expectStatus:   http.StatusOK,
		},
		{
			name:           "health path - should call next",
			path:           "/health",
			method:         "GET",
			body:           "",
			expectNextCall: true,
			expectStatus:   http.StatusOK,
		},
		{
			name:           "favicon path - should not call next",
			path:           "/favicon.ico",
			method:         "GET",
			body:           "",
			expectNextCall: false,
			expectStatus:   http.StatusOK,
		},
		{
			name:           "root path GET - should not call next",
			path:           "/",
			method:         "GET",
			body:           "",
			expectNextCall: false,
			expectStatus:   http.StatusOK,
		},
		{
			name:           "api path POST without auth - should fail",
			path:           "/api",
			method:         "POST",
			body:           `{"jsonrpc":"2.0","method":"test","id":1}`,
			expectNextCall: false,
			expectStatus:   http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			middleware := AuthMiddleware(nextHandler, "/metrics", "admin", "password", "test-token")

			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			} else {
				bodyReader = nil
			}

			req := httptest.NewRequest(tt.method, tt.path, bodyReader)
			if tt.method == "POST" && tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}

			recorder := httptest.NewRecorder()

			middleware.ServeHTTP(recorder, req)

			assert.Equal(t, tt.expectNextCall, nextCalled, "Next handler call mismatch")
			assert.Equal(t, tt.expectStatus, recorder.Code, "Status code mismatch")
		})
	}
}

// TestAuthMiddleware_Authentication тестирует различные методы аутентификации
func TestAuthMiddleware_Authentication(t *testing.T) {
	prx.global.maxReqBodySizeInt64 = 100

	tests := []struct {
		name           string
		login          string
		password       string
		token          string
		authHeader     string
		expectedStatus int
		description    string
	}{
		{
			name:           "valid bearer token",
			token:          "secret-token",
			authHeader:     "Bearer secret-token",
			expectedStatus: http.StatusOK,
			description:    "Должен пропустить с правильным токеном",
		},
		{
			name:           "invalid bearer token",
			token:          "secret-token",
			authHeader:     "Bearer wrong-token",
			expectedStatus: http.StatusUnauthorized,
			description:    "Должен отвергнуть неправильный токен",
		},
		{
			name:           "missing bearer token",
			token:          "secret-token",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
			description:    "Должен отвергнуть запрос без токена",
		},
		{
			name:           "valid basic auth",
			login:          "admin",
			password:       "password123",
			authHeader:     "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:password123")),
			expectedStatus: http.StatusOK,
			description:    "Должен пропустить с правильными логином/паролем",
		},
		{
			name:           "invalid basic auth",
			login:          "admin",
			password:       "password123",
			authHeader:     "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:wrongpass")),
			expectedStatus: http.StatusUnauthorized,
			description:    "Должен отвергнуть с неправильным паролем",
		},
		{
			name:           "no authentication required when empty credentials",
			login:          "",
			password:       "",
			token:          "",
			authHeader:     "",
			expectedStatus: http.StatusOK,
			description:    "Должен пропустить когда нет требований аутентификации",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			middleware := AuthMiddleware(nextHandler, "/metrics", tt.login, tt.password, tt.token)

			// Создаем валидный JSON-RPC запрос
			requestBody := `{"jsonrpc":"2.0","method":"host.get","id":1}`
			req := httptest.NewRequest("POST", "/api", strings.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			recorder := httptest.NewRecorder()

			middleware.ServeHTTP(recorder, req)

			if tt.expectedStatus == http.StatusOK {
				assert.True(t, nextCalled, "Next handler should be called for successful auth")
			} else {
				assert.False(t, nextCalled, "Next handler should not be called for failed auth")
			}
			assert.Equal(t, tt.expectedStatus, recorder.Code, tt.description)
		})
	}
}

// TestAuthMiddleware_SpecialMethods тестирует обработку специальных методов
func TestAuthMiddleware_SpecialMethods(t *testing.T) {
	prx.global.maxReqBodySizeInt64 = 100
	prx.config.APIversion = "6.0.0"

	tests := []struct {
		name           string
		method         string
		authHeader     string
		expectedStatus int
		checkResult    bool
		expectedResult string
	}{
		{
			name:           "user.login method - no auth required",
			method:         "user.login",
			authHeader:     "",
			expectedStatus: http.StatusOK,
			checkResult:    true,
			expectedResult: "faketoken123",
		},
		{
			name:           "apiinfo.version method - no auth required",
			method:         "apiinfo.version",
			authHeader:     "",
			expectedStatus: http.StatusOK,
			checkResult:    true,
			expectedResult: "6.0.0",
		},
		{
			name:           "create method blocked - no auth required",
			method:         "host.create",
			authHeader:     "",
			expectedStatus: http.StatusOK,
			checkResult:    false,
		},
		{
			name:           "normal method requires auth",
			method:         "host.get",
			authHeader:     "Bearer token",
			expectedStatus: http.StatusOK,
			checkResult:    false,
		},
		{
			name:           "normal method without auth fails",
			method:         "host.get",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
			checkResult:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			// Middleware с аутентификацией
			middleware := AuthMiddleware(nextHandler, "/metrics", "user", "pass", "token")

			requestBody := `{"jsonrpc":"2.0","method":"` + tt.method + `","id":1}`
			req := httptest.NewRequest("POST", "/api", strings.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			recorder := httptest.NewRecorder()

			middleware.ServeHTTP(recorder, req)

			assert.Equal(t, tt.expectedStatus, recorder.Code)

			if tt.expectedStatus == http.StatusOK && tt.checkResult {
				var response map[string]interface{}
				err := json.Unmarshal(recorder.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResult, response["result"])
			}

			// Проверяем что next handler не вызывался для специальных методов
			specialMethods := map[string]bool{
				"user.login":      true,
				"apiinfo.version": true,
				"host.create":     true,
				"item.create":     true,
				"trigger.create":  true,
			}
			if specialMethods[tt.method] {
				assert.False(t, nextCalled, "Next handler should not be called for special methods")
			}
		})
	}
}

// TestAuthMiddleware_JSONValidation тестирует валидацию JSON
func TestAuthMiddleware_JSONValidation(t *testing.T) {
	prx.global.maxReqBodySizeInt64 = 100

	tests := []struct {
		name           string
		body           string
		contentType    string
		expectedStatus int
		description    string
	}{
		{
			name:           "valid JSON",
			body:           `{"jsonrpc":"2.0","method":"test","id":1}`,
			contentType:    "application/json",
			expectedStatus: http.StatusUnauthorized, // требует аутентификации
			description:    "Должен принять валидный JSON",
		},
		{
			name:           "invalid JSON syntax",
			body:           `{"jsonrpc":"2.0","method":"test","id":1`,
			contentType:    "application/json",
			expectedStatus: http.StatusBadRequest,
			description:    "Должен отвергнуть невалидный JSON",
		},
		{
			name:           "wrong Content-Type",
			body:           `{"jsonrpc":"2.0","method":"test","id":1}`,
			contentType:    "text/plain",
			expectedStatus: http.StatusUnsupportedMediaType,
			description:    "Должен отвергнуть неправильный Content-Type",
		},
		{
			name:           "missing jsonrpc version",
			body:           `{"method":"test","id":1}`,
			contentType:    "application/json",
			expectedStatus: http.StatusBadRequest,
			description:    "Должен отвергнуть запрос без jsonrpc версии",
		},
		{
			name:           "wrong jsonrpc version",
			body:           `{"jsonrpc":"1.0","method":"test","id":1}`,
			contentType:    "application/json",
			expectedStatus: http.StatusBadRequest,
			description:    "Должен отвергнуть неправильную версию jsonrpc",
		},
		{
			name:           "empty body",
			body:           "",
			contentType:    "application/json",
			expectedStatus: http.StatusBadRequest,
			description:    "Должен отвергнуть пустое тело",
		},
		{
			name:           "malformed JSON",
			body:           `{jsonrpc: "2.0"}`,
			contentType:    "application/json",
			expectedStatus: http.StatusBadRequest,
			description:    "Должен отвергнуть некорректный JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			middleware := AuthMiddleware(nextHandler, "/metrics", "admin", "123", "")

			req := httptest.NewRequest("POST", "/api", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)

			recorder := httptest.NewRecorder()

			middleware.ServeHTTP(recorder, req)

			assert.Equal(t, tt.expectedStatus, recorder.Code, tt.description)

			// Next handler не должен вызываться при ошибках валидации
			if tt.expectedStatus != http.StatusOK && tt.expectedStatus != http.StatusUnauthorized {
				assert.False(t, nextCalled, "Next handler should not be called for validation errors")
			}
		})
	}
}

// TestAuthMiddleware_MethodHandling тестирует обработку HTTP методов
func TestAuthMiddleware_MethodHandling(t *testing.T) {
	prx.global.maxReqBodySizeInt64 = 100

	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		authtoken      string
		expectedStatus int
	}{
		{
			name:           "POST to api - allowed",
			method:         "POST",
			path:           "/api",
			body:           `{"jsonrpc":"2.0","method":"test"}`,
			expectedStatus: http.StatusUnauthorized, // требует аутентификации
		},
		{
			name:           "GET to api - not allowed",
			method:         "GET",
			path:           "/api",
			body:           "",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "PUT to api - not allowed",
			method:         "PUT",
			path:           "/api",
			body:           "",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "DELETE to api - not allowed",
			method:         "DELETE",
			path:           "/api",
			body:           "",
			expectedStatus: http.StatusMethodNotAllowed,
		},
		{
			name:           "GET to root - allowed",
			method:         "GET",
			path:           "/",
			body:           "",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "POST to root - allowed",
			method:         "POST",
			path:           "/",
			body:           `{"jsonrpc":"2.0","method":"test.test","auth":"token123","id":1}`,
			authtoken:      "token123",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "POST to root - JSON Broken",
			method:         "POST",
			path:           "/",
			body:           `{"jsonrpc":"2.0","method""test.test","auth":"token123","id":1}`,
			authtoken:      "token123",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			//nextCalled := false
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				//	nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			middleware := AuthMiddleware(nextHandler, "/metrics", "", "", "token123")

			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.method == "POST" && tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+tt.authtoken)
			}

			recorder := httptest.NewRecorder()

			middleware.ServeHTTP(recorder, req)

			assert.Equal(t, tt.expectedStatus, recorder.Code)
		})
	}
}

// TestFaviconHandler тестирует обработку favicon
func TestFaviconHandler(t *testing.T) {
	recorder := httptest.NewRecorder()

	faviconHandler(recorder)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "image/x-icon", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "public, max-age=86400", recorder.Header().Get("Cache-Control"))

	// Проверяем что возвращаются какие-то данные
	assert.True(t, recorder.Body.Len() > 0, "Favicon should return some data")
}
