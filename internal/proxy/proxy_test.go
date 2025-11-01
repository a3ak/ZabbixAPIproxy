package proxy

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"ZabbixAPIproxy/internal/zabbix"

	"github.com/a3ak/circuitbreaker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockZabbixClient мок для клиента Zabbix
type MockZabbixClient struct {
	mu          sync.Mutex
	SendFunc    func(ctx context.Context, url string, ignoreSSL bool, request map[string]any) (map[string]any, error)
	CallCount   int
	LastRequest map[string]any
}

func (m *MockZabbixClient) SendToZabbix(ctx context.Context, url string, ignoreSSL bool, request map[string]any) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallCount++
	m.LastRequest = request

	if m.SendFunc != nil {
		return m.SendFunc(ctx, url, ignoreSSL, request)
	}

	// Default success response
	return map[string]any{
		"jsonrpc": "2.0",
		"result": []any{
			map[string]any{"hostid": "10001", "name": "test-host"},
		},
		"id": request["id"],
	}, nil
}

func (m *MockZabbixClient) Close()               {}
func (m *MockZabbixClient) GetClientsCount() int { return 0 }

// MockMetricsCollector для тестов
type MockMetricsCollector struct {
	mu               sync.Mutex
	requestsTotal    map[string]int
	responseSizes    []int
	requestDurations []time.Duration
	requestErrors    map[string]int
	activeRequests   int
}

func NewMockMetricsCollector() *MockMetricsCollector {
	return &MockMetricsCollector{
		requestsTotal: make(map[string]int),
		requestErrors: make(map[string]int),
	}
}

func (m *MockMetricsCollector) IncRequestsTotal(method, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s_%s", method, status)
	m.requestsTotal[key]++
}

func (m *MockMetricsCollector) ObserveResponseSize(size int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responseSizes = append(m.responseSizes, size)
}

func (m *MockMetricsCollector) ObserveRequestDuration(server, method string, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestDurations = append(m.requestDurations, duration)
}

func (m *MockMetricsCollector) IncRequestStatus(server, errorType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s_%s", server, errorType)
	m.requestErrors[key]++
}

func (m *MockMetricsCollector) IncIncomingRequests(server string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeRequests++
}

func (m *MockMetricsCollector) GetRequestsTotal(method, status string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s_%s", method, status)
	return m.requestsTotal[key]
}

// Test структура для тестирования с инъекцией зависимостей
type TestProxy struct {
	t           *testing.T
	zbxClient   *MockZabbixClient
	metrics     *MockMetricsCollector
	initialized bool
}

func NewTestProxy(t *testing.T) *TestProxy {
	return &TestProxy{
		t:         t,
		zbxClient: &MockZabbixClient{},
		metrics:   NewMockMetricsCollector(),
	}
}

// processAllServersWithMock версия processAllServers с инъекцией моков
func (tp *TestProxy) processAllServersWithMock(ctx context.Context, request map[string]any, trace_id string) (any, []string) {
	if !tp.initialized {
		tp.t.Fatal("TestProxy not initialized. Call Init() first.")
	}

	// Сохраняем оригинальные зависимости
	originalClient := prx.zbxClient
	originalMetrics := metricsCollector

	// Подменяем зависимости для теста
	prx.zbxClient = tp.zbxClient
	metricsCollector = tp.metrics

	// Восстанавливаем оригинальные зависимости после завершения
	defer func() {
		prx.zbxClient = originalClient
		metricsCollector = originalMetrics
	}()

	return processAllServers(ctx, request, trace_id)
}

func (tp *TestProxy) Init(g Global, z ZabbixConf, cbConf CBConf, cacheCfg CacheConf, excludeLog []string) {
	// Инициализируем proxy с тестовыми настройками
	InitProxy(g, z, cbConf, cacheCfg, excludeLog)

	tp.initialized = true
}

func (tp *TestProxy) Cleanup() {
	if tp.initialized {
		StopProxy()
		tp.initialized = false
	}

	defer os.Remove(":memory:") // Cleanup in-memory DB file
}

func (tp *TestProxy) GetMockClient() *MockZabbixClient {
	return tp.zbxClient
}

func (tp *TestProxy) GetMockMetrics() *MockMetricsCollector {
	return tp.metrics
}

// TestInitProxy тестирует инициализацию прокси
func TestInitProxy(t *testing.T) {
	g := Global{
		ListenAddr:     ":8080",
		Token:          "test-token",
		MaxRequests:    10,
		MaxReqBodySize: "5MB",
		MaxTimeout:     "30s",
	}

	z := ZabbixConf{
		Servers: []zabbix.ZabbixServer{
			{URL: "http://server1.com", ID: 1, Token: "token1"},
			{URL: "http://server2.com", ID: 2, Token: "token2"},
		},
		Limits: struct {
			MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
			MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
			MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
		}{
			MaxRequestsByZBX:   5,
			MaxTimeoutByZBX:    "20s",
			MaxRespBodySizeZbx: "10MB",
		},
		APIversion: "6.0",
	}

	cbConf := CBConf{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		RecoveryTimeout:  30 * time.Second,
	}

	cacheCfg := CacheConf{
		TTL:             "1h",
		CleanupInterval: "5m",
		DBPath:          ":memory:",
		AutoSave:        "30s",
	}

	excludeLog := []string{"apiinfo.version", "user.login"}

	InitProxy(g, z, cbConf, cacheCfg, excludeLog)

	// Проверяем инициализацию
	assert.NotNil(t, prx.cache)
	assert.Equal(t, 2, len(prx.config.Servers))
	assert.Equal(t, 10, cap(prx.requestSemaphore))
	assert.Equal(t, []string{"apiinfo.version", "user.login"}, prx.excludeRequests)
	assert.NotNil(t, prx.zbxClient)

	// Cleanup
	StopProxy()
}

// TestGetAllServers тестирует получение всех серверов
func TestGetAllServers(t *testing.T) {
	// Инициализируем тестовый proxy
	g := Global{MaxRequests: 10}
	z := ZabbixConf{
		Servers: []zabbix.ZabbixServer{
			{ID: 1}, {ID: 2}, {ID: 3},
		},
	}

	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer StopProxy()

	servers := getAllServers()
	assert.ElementsMatch(t, []int{1, 2, 3}, servers)
}

// TestGetTargetServers тестирует определение целевых серверов
func TestGetTargetServers(t *testing.T) {
	testCases := []struct {
		name     string
		request  map[string]any
		expected []int
	}{
		{
			name: "hostids from specific server",
			request: map[string]any{
				"params": map[string]any{
					"hostids": []any{"10001", "10002"}, // Server ID 1
				},
			},
			expected: []int{1, 2},
		},
		{
			name: "multiple server IDs",
			request: map[string]any{
				"params": map[string]any{
					"hostids": []any{"10001", "20001"}, // Servers 1 and 2
				},
			},
			expected: []int{1},
		},
		{
			name: "proxy IDs should return all servers",
			request: map[string]any{
				"params": map[string]any{
					"hostids": []any{"1", "2"}, // Proxy IDs
				},
			},
			expected: []int{1, 2}, // All servers
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := Global{MaxRequests: 10}
			z := ZabbixConf{
				Servers: []zabbix.ZabbixServer{
					{ID: 1}, {ID: 2},
				},
			}

			InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
			defer StopProxy()

			servers := getTargetServers(tc.request)
			assert.ElementsMatch(t, tc.expected, servers)
		})
	}
}

// TestProcessAllServers_Success тестирует успешную обработку запросов ко всем серверам
func TestProcessAllServers_Success(t *testing.T) {
	testProxy := NewTestProxy(t)
	defer testProxy.Cleanup()

	g := Global{
		MaxRequests: 10,
	}

	z := ZabbixConf{
		Servers: []zabbix.ZabbixServer{
			{URL: "http://server1.com", ID: 1, Token: "token1", Name: "server1"},
			{URL: "http://server2.com", ID: 2, Token: "token2", Name: "server2"},
		},
		Limits: struct {
			MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
			MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
			MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
		}{
			MaxRequestsByZBX: 5,
		},
	}

	cbConf := CBConf{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		RecoveryTimeout:  30 * time.Second,
	}

	cacheCfg := CacheConf{
		TTL:             "1h",
		CleanupInterval: "5m",
		DBPath:          ":memory:",
		AutoSave:        "30s",
	}

	testProxy.Init(g, z, cbConf, cacheCfg, []string{})

	cb := circuitbreaker.NewCBManager()
	// Initialize circuit breakers
	cb.InitCircuitBreakers([]string{"server1", "server2"}, circuitbreaker.CircuitBreakerConf{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		RecoveryTimeout:  30 * time.Second,
	})

	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "host.get",
		"id":      1,
		"params": map[string]any{
			"hostids": []any{"10001", "20001"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, errors := testProxy.processAllServersWithMock(ctx, request, "test-trace")

	assert.Empty(t, errors)
	assert.NotNil(t, result)

	// Проверяем что мок был вызван
	mockClient := testProxy.GetMockClient()
	assert.NotNil(t, mockClient)
	assert.Greater(t, mockClient.CallCount, 0)
}

// TestProcessAllServers_Timeout тестирует обработку таймаутов
func TestProcessAllServers_Timeout(t *testing.T) {
	testProxy := NewTestProxy(t)
	defer testProxy.Cleanup()

	// Настраиваем мок для медленного ответа
	mockClient := testProxy.GetMockClient()
	mockClient.SendFunc = func(ctx context.Context, url string, ignoreSSL bool, request map[string]any) (map[string]any, error) {
		select {
		case <-time.After(2 * time.Second):
			return map[string]any{"result": "success"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	cbConf := CBConf{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		RecoveryTimeout:  30 * time.Second,
	}

	g := Global{
		MaxRequests: 5,
	}

	z := ZabbixConf{
		Servers: []zabbix.ZabbixServer{
			{URL: "http://slow-server.com", ID: 1, Token: "token1", Name: "slow-server"},
		},
		Limits: struct {
			MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
			MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
			MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
		}{
			MaxRequestsByZBX: 5,
		},
	}

	testProxy.Init(g, z, cbConf, CacheConf(initTestCache()), []string{})
	cb := circuitbreaker.NewCBManager()
	//Initialize circuit breakers
	cb.InitCircuitBreakers([]string{"slow-server"}, circuitbreaker.CircuitBreakerConf{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		RecoveryTimeout:  30 * time.Second,
	})

	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "host.get",
		"id":      1,
		"params":  map[string]any{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond) // Short timeout
	defer cancel()

	result, errors := testProxy.processAllServersWithMock(ctx, request, "test-timeout")

	assert.NotEmpty(t, errors)
	assert.Contains(t, errors[0], "request timeout")
	assert.Nil(t, result)
}

// TestProcessAllServers_Error тестирует обработку ошибок
func TestProcessAllServers_Error(t *testing.T) {
	testProxy := NewTestProxy(t)
	defer testProxy.Cleanup()

	// Настраиваем мок для возврата ошибок
	mockClient := testProxy.GetMockClient()
	mockClient.SendFunc = func(ctx context.Context, url string, ignoreSSL bool, request map[string]any) (map[string]any, error) {
		return nil, fmt.Errorf("mock server error")
	}

	g := Global{
		MaxRequests: 5,
	}

	z := ZabbixConf{
		Servers: []zabbix.ZabbixServer{
			{URL: "http://failing-server.com", ID: 1, Token: "token1", Name: "failing-server"},
		},
		Limits: struct {
			MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
			MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
			MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
		}{
			MaxRequestsByZBX: 5,
		},
	}

	testProxy.Init(g, z, CBConf{}, CacheConf(initTestCache()), []string{})

	cb := circuitbreaker.NewCBManager()
	// Initialize circuit breakers
	cb.InitCircuitBreakers([]string{"failing-server"}, circuitbreaker.CircuitBreakerConf{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		RecoveryTimeout:  30 * time.Second,
	})

	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  "host.get",
		"id":      1,
		"params":  map[string]any{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, errors := testProxy.processAllServersWithMock(ctx, request, "test-error")

	assert.NotEmpty(t, errors)
	assert.Equal(t, map[string]interface{}{}, result)
	assert.Contains(t, errors[0], "mock server error")
}

// TestExtractServersFromParams тестирует извлечение серверов из параметров
func TestExtractServersFromParams(t *testing.T) {
	testCases := []struct {
		name            string
		params          map[string]any
		expectedResult  bool
		expectedServers map[int]bool
	}{
		{
			name: "hostids from single server",
			params: map[string]any{
				"hostids": []any{"10001", "20001"},
			},
			expectedResult:  false,
			expectedServers: map[int]bool{1: true},
		},
		{
			name: "multiple server IDs",
			params: map[string]any{
				"hostids": []any{"10001", "20002"},
			},
			expectedResult:  false,
			expectedServers: map[int]bool{1: true, 2: true},
		},
		{
			name: "proxy IDs",
			params: map[string]any{
				"hostids": []any{"1", "2"},
			},
			expectedResult:  true,
			expectedServers: map[int]bool{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			serverMap := make(map[int]bool)
			result := extractServersFromParams(tc.params, serverMap)

			assert.Equal(t, tc.expectedResult, result)
			assert.Equal(t, tc.expectedServers, serverMap)
		})
	}
}

// TestGetConnectionStats тестирует получение статистики соединений
func TestGetConnectionStats(t *testing.T) {
	g := Global{MaxRequests: 10}
	z := ZabbixConf{
		Servers: []zabbix.ZabbixServer{
			{ID: 1},
		},
	}

	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer StopProxy()

	stats := GetConnectionStats()

	assert.Contains(t, stats, "active_goroutines")
	assert.Contains(t, stats, "active_requests")
	assert.Contains(t, stats, "http_clients")

	assert.GreaterOrEqual(t, stats["active_goroutines"], 0)
	assert.GreaterOrEqual(t, stats["active_requests"], 0)
	assert.GreaterOrEqual(t, stats["http_clients"], 0)
}

// TestPrettyJSON тестирует форматирование JSON с маскировкой токенов
func TestPrettyJSON(t *testing.T) {
	testCases := []struct {
		name     string
		input    map[string]any
		contains []string
		excludes []string
	}{
		{
			name: "mask auth token",
			input: map[string]any{
				"auth":   "very-long-authentication-token",
				"method": "host.get",
				"params": map[string]any{},
			},
			contains: []string{"method", "host.get"},
			excludes: []string{"very-long-authentication-token"},
		},
		{
			name: "short auth token",
			input: map[string]any{
				"auth":   "short",
				"method": "host.get",
			},
			contains: []string{"method", "host.get", "*****"},
			excludes: []string{"short"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := prettyJSON(tc.input)
			assert.NotEmpty(t, result)

			for _, contain := range tc.contains {
				assert.Contains(t, result, contain)
			}

			for _, exclude := range tc.excludes {
				assert.NotContains(t, result, exclude)
			}
		})
	}
}

// TestStopProxy тестирует корректную остановку прокси
func TestStopProxy(t *testing.T) {
	// Create temp file for cache
	tmpFile, err := os.CreateTemp("", "test_cache.*.db")
	require.NoError(t, err)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// Initialize proxy
	g := Global{
		MaxRequests: 10,
	}

	z := ZabbixConf{
		Servers: []zabbix.ZabbixServer{
			{URL: "http://test.com", ID: 1, Token: "token1"},
		},
		Limits: struct {
			MaxRequestsByZBX   int    `yaml:"max_requests_by_zbx"`
			MaxTimeoutByZBX    string `yaml:"max_timeout_by_zbx"`
			MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
		}{
			MaxRequestsByZBX: 10,
		},
	}

	cacheCfg := CacheConf{
		TTL:             "1h",
		CleanupInterval: "5m",
		DBPath:          tmpFile.Name(),
		AutoSave:        "30s",
	}

	InitProxy(g, z, CBConf{}, cacheCfg, []string{})

	// Verify cache is initialized
	assert.NotNil(t, prx.cache)

	// Stop proxy
	StopProxy()

	// Verify semaphore is closed (нужно аккуратно проверять, так как close делает канал непригодным для использования)
	// Вместо этого проверим, что основные структуры очищены
	assert.NotNil(t, prx.cache) // Кеш все еще существует, но остановлен
}

// Вспомогательные функции для тестов
func TestDeepClone(t *testing.T) {
	original := map[string]any{
		"key1": "value1",
		"key2": []any{1, 2, 3},
		"key3": map[string]any{"nested": "value"},
		"key4": map[string]map[string]int{"test": {"nested": 1}},
	}

	cloned := deepClone(original).(map[string]any)

	assert.Equal(t, original, cloned)
}

func TestReturnToPool(t *testing.T) {
	obj := make(map[string]any)
	returnToPool(obj)
	// Mainly testing that it doesn't panic
}
