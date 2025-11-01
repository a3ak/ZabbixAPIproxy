// zabbix_client_test.go
package zabbix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestZabbixClient_SendToZabbix тестирует базовую функциональность с реальным HTTP сервером
func TestZabbixClient_SendToZabbix(t *testing.T) {
	// Создаем тестовый HTTP сервер
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Проверяем метод и content-type
		if r.Method != "POST" {
			t.Errorf("Expected POST method, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected application/json content-type, got %s", r.Header.Get("Content-Type"))
		}

		// Читаем тело запроса
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("Error reading request body: %v", err)
			return
		}

		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("Error parsing request JSON: %v", err)
			return
		}

		// В зависимости от URL возвращаем разные ответы
		switch r.URL.String() {
		case "/success":
			response := map[string]any{
				"jsonrpc": "2.0",
				"result":  []map[string]any{{"hostid": "10084", "host": "test-host"}},
				"id":      1,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)

		case "/error":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal Server Error"))

		case "/invalid-json":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("invalid json"))

		case "/large-response":
			// Создаем большой ответ для тестирования лимитов
			largeData := make([]map[string]any, 1000)
			for i := range largeData {
				largeData[i] = map[string]any{"id": i, "data": strings.Repeat("x", 1000)}
			}
			response := map[string]any{"result": largeData}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := Zabbix{
		Limits: struct {
			MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
			MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
			MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
		}{
			MaxRequestsByZBX:   100,
			MaxTimeoutByZBX:    "30s",
			MaxRespBodySizeZbx: "1MB", // Ограничиваем для теста
		},
	}

	client, err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer client.Close()

	tests := []struct {
		name          string
		url           string
		request       map[string]any
		expectError   bool
		errorContains string
	}{
		{
			name: "successful request",
			url:  server.URL + "/success",
			request: map[string]any{
				"jsonrpc": "2.0",
				"method":  "host.get",
				"params":  map[string]any{},
				"id":      1,
			},
			expectError: false,
		},
		{
			name: "HTTP error",
			url:  server.URL + "/error",
			request: map[string]any{
				"method": "host.get",
				"params": map[string]any{},
			},
			expectError:   true,
			errorContains: "HTTP 500",
		},
		{
			name: "invalid JSON response",
			url:  server.URL + "/invalid-json",
			request: map[string]any{
				"method": "host.get",
				"params": map[string]any{},
			},
			expectError:   true,
			errorContains: "invalid JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			response, err := client.SendToZabbix(ctx, tt.url, false, tt.request)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, but got none")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error containing '%s', got '%v'", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if response == nil {
					t.Error("Expected response, but got nil")
				}
			}
		})
	}
}

// TestZabbixClient_Init тестирует инициализацию клиента
func TestZabbixClient_Init(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Zabbix
		expectError bool
	}{
		{
			name: "valid configuration",
			cfg: Zabbix{
				Limits: struct {
					MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
					MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
					MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
				}{
					MaxRequestsByZBX:   100,
					MaxTimeoutByZBX:    "30s",
					MaxRespBodySizeZbx: "10MB",
				},
			},
			expectError: false,
		},
		{
			name: "invalid MaxRespBodySizeZbx with default fallback",
			cfg: Zabbix{
				Limits: struct {
					MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
					MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
					MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
				}{
					MaxRequestsByZBX:   100,
					MaxTimeoutByZBX:    "30s",
					MaxRespBodySizeZbx: "invalid",
				},
			},
			expectError: true, // Ожидаем ошибку, но клиент все равно создается
		},
		{
			name:        "empty configuration with defaults",
			cfg:         Zabbix{},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := Init(tt.cfg)

			if client == nil {
				t.Fatal("Client should not be nil even with errors")
			}
			defer client.Close()

			if tt.expectError && err == nil {
				t.Error("Expected error, but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

// TestZabbixClient_ConnectionPool тестирует пул соединений
func TestZabbixClient_ConnectionPool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{"result": []map[string]any{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := Zabbix{
		Limits: struct {
			MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
			MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
			MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
		}{
			MaxRequestsByZBX:   100,
			MaxTimeoutByZBX:    "30s",
			MaxRespBodySizeZbx: "10MB",
		},
	}

	client, err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer client.Close()

	// Проверяем начальное состояние
	initialCount := client.GetClientsCount()
	if initialCount != 0 {
		t.Errorf("Expected 0 clients initially, got %d", initialCount)
	}

	// Делаем запросы с разными ignoreSSL флагами
	ctx := context.Background()
	request := map[string]any{"method": "test", "params": map[string]any{}}

	// Запрос с ignoreSSL = false
	_, err = client.SendToZabbix(ctx, server.URL, false, request)
	if err != nil {
		t.Errorf("First request failed: %v", err)
	}

	// Запрос с ignoreSSL = true
	_, err = client.SendToZabbix(ctx, server.URL, true, request)
	if err != nil {
		t.Errorf("Second request failed: %v", err)
	}

	// Проверяем что создалось 2 разных клиента (для ignoreSSL=false и ignoreSSL=true)
	finalCount := client.GetClientsCount()
	if finalCount != 2 {
		t.Errorf("Expected 2 clients, got %d", finalCount)
	}
}

// TestZabbixClient_ContextCancellation тестирует отмену контекста
func TestZabbixClient_ContextCancellation(t *testing.T) {
	// Сервер с задержкой для тестирования таймаута
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond) // Задержка
		response := map[string]any{"result": []map[string]any{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := Zabbix{
		Limits: struct {
			MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
			MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
			MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
		}{
			MaxRequestsByZBX:   100,
			MaxTimeoutByZBX:    "1s", // Короткий таймаут
			MaxRespBodySizeZbx: "10MB",
		},
	}

	client, err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer client.Close()

	// Тестируем отмену контекста
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Немедленно отменяем

	request := map[string]any{"method": "test", "params": map[string]any{}}
	_, err = client.SendToZabbix(ctx, server.URL, false, request)

	if err == nil {
		t.Error("Expected error due to cancelled context, but got none")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Errorf("Expected context error, got: %v", err)
	}
}

// TestZabbixClient_ConcurrentAccess тестирует конкурентный доступ
func TestZabbixClient_ConcurrentAccess(t *testing.T) {
	var requestCount int32
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		mu.Unlock()

		response := map[string]any{"result": []map[string]any{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cfg := Zabbix{
		Limits: struct {
			MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
			MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
			MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
		}{
			MaxRequestsByZBX:   100,
			MaxTimeoutByZBX:    "30s",
			MaxRespBodySizeZbx: "10MB",
		},
	}

	client, err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer client.Close()

	// Запускаем несколько горутин
	var wg sync.WaitGroup
	concurrentRequests := 10
	errors := make(chan error, concurrentRequests)

	for i := 0; i < concurrentRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			ctx := context.Background()
			request := map[string]any{
				"method": "test",
				"params": map[string]any{"id": id},
			}

			_, err := client.SendToZabbix(ctx, server.URL, false, request)
			if err != nil {
				errors <- fmt.Errorf("goroutine %d: %v", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Проверяем ошибки
	for err := range errors {
		t.Error(err)
	}

	// Проверяем что все запросы были выполнены
	if requestCount != int32(concurrentRequests) {
		t.Errorf("Expected %d calls, got %d", concurrentRequests, requestCount)
	}
}

// TestZabbixClient_JSONMarshalError тестирует ошибки маршалинга
func TestZabbixClient_JSONMarshalError(t *testing.T) {
	cfg := Zabbix{
		Limits: struct {
			MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
			MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
			MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
		}{
			MaxRequestsByZBX:   100,
			MaxTimeoutByZBX:    "30s",
			MaxRespBodySizeZbx: "10MB",
		},
	}

	client, err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer client.Close()

	// Создаем запрос с циклической ссылкой (нельзя маршалить)
	type CyclicStruct struct {
		Self *CyclicStruct
	}
	cyclic := &CyclicStruct{}
	cyclic.Self = cyclic

	ctx := context.Background()
	_, err = client.SendToZabbix(ctx, "http://test.example.com", false, map[string]any{
		"invalid": cyclic, // Вызовет ошибку маршалинга
	})

	if err == nil {
		t.Error("Expected JSON marshal error, but got none")
	}
}

// TestZabbixClient_Close тестирует закрытие клиентов
func TestZabbixClient_Close(t *testing.T) {
	cfg := Zabbix{
		Limits: struct {
			MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
			MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
			MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
		}{
			MaxRequestsByZBX:   100,
			MaxTimeoutByZBX:    "30s",
			MaxRespBodySizeZbx: "10MB",
		},
	}

	client, err := Init(cfg)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Создаем несколько клиентов
	_ = client.getHTTPClient(false)
	_ = client.getHTTPClient(true)

	if client.GetClientsCount() != 2 {
		t.Errorf("Expected 2 clients before close, got %d", client.GetClientsCount())
	}

	// Закрываем клиенты
	client.Close()

	// Клиенты все еще в мапе, но соединения закрыты
	if client.GetClientsCount() != 0 {
		t.Errorf("Expected 2 clients after close, got %d", client.GetClientsCount())
	}
}
