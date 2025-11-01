package proxy

import (
	"os"
	"strconv"
	"sync"
	"testing"

	"ZabbixAPIproxy/internal/cache"
)

type cacheConfig cache.CacheCfg

// Инициализация тестового кеша
func initTestCache() cacheConfig {
	cfg := cacheConfig{
		TTL:             "1d",
		CleanupInterval: "1m",
		DBPath:          ":memory:",
		AutoSave:        "30s",
		CachedFields:    prx.cachedFields,
	}

	return cfg
}

func stopTestProxy() {
	StopProxy()
	defer os.Remove(":memory:")
}

// TestIsEmpty тестирует функцию isEmpty
func TestIsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected bool
	}{
		{"nil", nil, true},
		{"empty slice", []any{}, true},
		{"empty map", map[string]any{}, true},
		{"non-empty slice", []any{1, 2}, false},
		{"non-empty map", map[string]any{"a": 1}, false},
		{"string", "test", false},
		{"number", 42, false},
		{"zero number", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isEmpty(tt.input)
			if result != tt.expected {
				t.Errorf("isEmpty(%v) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestIsZeroID тестирует функцию isZeroID
func TestIsZeroID(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected bool
	}{
		{"zero float", 0.0, true},
		{"zero int", 0, true},
		{"zero string", "0", true},
		{"non-zero float", 1.0, false},
		{"non-zero int", 1, false},
		{"non-zero string", "1", false},
		{"text string", "abc", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isZeroID(tt.input)
			if result != tt.expected {
				t.Errorf("isZeroID(%v) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestSimpleModifyID тестирует функцию simpleModifyID
func TestSimpleModifyID(t *testing.T) {
	serverID := 5

	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{"int ID", 123, 123*10 + serverID},
		{"float ID", 123.0, 123*10 + serverID},
		{"string ID", "123", strconv.Itoa(123*10 + serverID)},
		{"zero int", 0, 0},
		{"zero string", "0", "0"},
		{"non-numeric string", "abc", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := simpleModifyID(tt.input, serverID)
			if result != tt.expected {
				t.Errorf("simpleModifyID(%v, %d) = %v, expected %v", tt.input, serverID, result, tt.expected)
			}
		})
	}
}

// TestGenerateProxyID тестирует функцию generateProxyID
func TestGenerateProxyID(t *testing.T) {
	// Инициализируем proxy для теста
	g := Global{MaxRequests: 10}
	z := ZabbixConf{}
	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer stopTestProxy()

	serverID := 3

	tests := []struct {
		name        string
		fieldType   string
		data        map[string]any
		expectError bool
	}{
		{
			"valid host",
			"host",
			map[string]any{"hostid": 100, "name": "test-host"},
			false,
		},
		{
			"valid group",
			"group",
			map[string]any{"groupid": 200, "name": "test-group"},
			false,
		},
		{
			"Get previously generated Proxyid from Cache",
			"host",
			map[string]any{"hostid": 100},
			false,
		},
		{
			"missing name field",
			"host",
			map[string]any{"hostid": 300},
			true,
		},
		{
			"missing id field",
			"host",
			map[string]any{"name": "test-host"},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := generateProxyID(tt.fieldType, tt.data, serverID)

			if tt.expectError {
				if err == nil {
					t.Errorf("%s: Expected error but got none", tt.name)
				}
				return
			}

			if err != nil {
				t.Errorf("%s: Unexpected error: %v", tt.name, err)
				return
			}

			if result == 0 {
				t.Errorf("%s: Generated zero proxy ID", tt.name)
			}
		})
	}
}

// TestConvertProxyIDToOriginal тестирует функцию convertProxyIDToOriginal
func TestConvertProxyIDToOriginal(t *testing.T) {
	// Инициализируем proxy для теста
	g := Global{MaxRequests: 10}
	z := ZabbixConf{}
	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer stopTestProxy()

	serverID := 2
	// Добавляем тестовые данные в кеш
	prx.cache.CacheType["host"].Set(123450, 100, serverID, "test-host")
	prx.cache.CacheType["group"].Set(678900, 200, serverID, "test-group")

	tests := []struct {
		name       string
		proxyID    any
		serverID   int
		cacheType  string
		expected   any
		shouldFind bool
	}{
		{"int proxy ID found", 123450, serverID, "host", 100, true},
		{"float proxy ID found", 678900.0, serverID, "group", 200, true},
		{"string proxy ID found", "123450", serverID, "host", "100", true},
		{"proxy ID not found", 999999, serverID, "host", nil, false},
		{"wrong server ID", 123450, 999, "host", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertProxyIDToOriginal(tt.proxyID, tt.serverID, tt.cacheType)

			if tt.shouldFind {
				if result == nil {
					t.Errorf("Expected to find original ID but got nil")
				}
			} else {
				if result != nil {
					t.Errorf("Expected nil but got %v", result)
				}
			}
		})
	}
}

// TestIsPureDigitString тестирует функцию isPureDigitString
func TestIsPureDigitString(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"123", true},
		{"0", true},
		{"", false},
		{"12a3", false},
		{"12.3", false},
		{"-123", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isPureDigitString(tt.input)
			if result != tt.expected {
				t.Errorf("isPureDigitString(%s) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestIsIDBasedRequest тестирует функцию isIDBasedRequest
func TestIsIDBasedRequest(t *testing.T) {
	tests := []struct {
		name     string
		request  map[string]any
		expected bool
		keys     []string
	}{
		{
			"with hostids",
			map[string]any{
				"params": map[string]any{
					"hostids": []any{1, 2, 3},
				},
			},
			true,
			[]string{"hostids"},
		},
		{
			"with multiple id arrays",
			map[string]any{
				"params": map[string]any{
					"hostids":  []any{1, 2},
					"groupids": []any{3, 4},
				},
			},
			true,
			[]string{"hostids", "groupids"},
		},
		{
			"empty id array",
			map[string]any{
				"params": map[string]any{
					"hostids": []any{},
				},
			},
			false,
			nil,
		},
		{
			"no id arrays",
			map[string]any{
				"params": map[string]any{
					"name": "test",
				},
			},
			false,
			nil,
		},
		{
			"nil params",
			map[string]any{},
			false,
			nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, keys := isIDBasedRequest(tt.request)
			if result != tt.expected {
				t.Errorf("isIDBasedRequest() = %v, expected %v", result, tt.expected)
			}

			if result {
				if len(keys) != len(tt.keys) {
					t.Errorf("Expected %d keys, got %d", len(tt.keys), len(keys))
				}
			}
		})
	}
}

// TestProcessResponseIDs тестирует основную функцию обработки
func TestProcessResponseIDs(t *testing.T) {
	// Инициализируем proxy для теста
	g := Global{MaxRequests: 10}
	z := ZabbixConf{}
	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer stopTestProxy()

	serverID := 1
	uniqProxyID := make(map[string]map[any]bool)
	mu := &sync.RWMutex{}

	tests := []struct {
		name  string
		input any
	}{
		{
			"simple map with IDs",
			map[string]any{
				"hostid":    100,
				"name":      "test-host",
				"groupid":   200,
				"groupname": "test-group",
			},
		},
		{
			"array of maps",
			[]any{
				map[string]any{
					"hostid": 101,
					"name":   "host1",
				},
				map[string]any{
					"hostid": 102,
					"name":   "host2",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := processResponseIDs(tt.input, serverID, uniqProxyID, mu, 0)

			// Для сложных структур нужна более детальная проверка
			if result == nil {
				t.Error("Result should not be nil")
			}
		})
	}
}

// TestGetServerFromID тестирует функцию getServerFromID
func TestGetServerFromID(t *testing.T) {
	tests := []struct {
		input    any
		expected int
	}{
		{1231, 1},
		{4562, 2},
		{7893, 3},
		{123.1, 3},
		{"1234", 4},
		{"abc", 0},
		{0, 0},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := getServerFromID(tt.input)
			if result != tt.expected {
				t.Errorf("getServerFromID(%v) = %d, expected %d", tt.input, result, tt.expected)
			}
		})
	}
}

// TestConvertGrafanaIDToOriginal тестирует функцию convertGrafanaIDToOriginal
func TestConvertGrafanaIDToOriginal(t *testing.T) {
	serverID := 3

	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{"matching server ID int", 1233, 123},
		{"matching server ID float", 123.3, 12},
		{"matching server ID string", "1233", 123},
		{"non-matching server ID", 1234, nil},
		{"wrong format string", "abc", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertGrafanaIDToOriginal(tt.input, serverID)
			if result != tt.expected {
				t.Errorf("%s: convertGrafanaIDToOriginal(%v, %d) = %v, expected %v", tt.name, tt.input, serverID, result, tt.expected)
			}
		})
	}
}

// TestIDBasedResponseSimpleModify тестирует функцию ifIDBasedResponseSimpleModify
func TestIDBasedResponseSimpleModify(t *testing.T) {
	serverID := 2

	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]any
	}{
		{
			"digit keys",
			map[string]any{"100": "value1", "200": "value2"},
			map[string]any{"1002": "value1", "2002": "value2"},
		},
		{
			"mixed keys",
			map[string]any{"100": "value1", "text": "value2"},
			map[string]any{"1002": "value1", "text": "value2"},
		},
		{
			"non-digit keys",
			map[string]any{"key1": "value1", "key2": "value2"},
			map[string]any{"key1": "value1", "key2": "value2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Копируем входные данные чтобы не модифицировать оригинал
			inputCopy := make(map[string]any)
			for k, v := range tt.input {
				inputCopy[k] = v
			}
			// Вызываем тестируемую функцию
			ifIDBasedResponseSimpleModify(inputCopy, serverID)

			// Проверяем ожидаемый результат
			for expectedKey, expectedValue := range tt.expected {
				if actualValue, exists := inputCopy[expectedKey]; !exists || actualValue != expectedValue {
					t.Errorf("RUN %s. Expected key %s with value %v, got %v", tt.name, expectedKey, expectedValue, actualValue)
				}
			}
		})
	}
}
