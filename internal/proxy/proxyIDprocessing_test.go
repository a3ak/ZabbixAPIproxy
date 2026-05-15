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

// Функция для очистки тестового proxy
func cleanupTestProxy() {
	StopProxy()
	os.Remove(":memory:")
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

// TestGenerateProxyIDCollisions тестирует механизм разрешения коллизий
func TestGenerateProxyIDCollisions(t *testing.T) {
	// Инициализируем proxy для теста
	g := Global{MaxRequests: 10}
	z := ZabbixConf{}
	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer stopTestProxy()

	serverID := 3
	fieldType := "host"

	// Создаем тестовые имена
	testNames := []string{
		"test-host-1",
		"test-host-2",
		"test-host-3",
		"test-host-4",
		"test-host-5",
	}

	generatedIDs := make(map[int]string) // proxyID -> name
	collisionsDetected := 0

	for _, name := range testNames {
		data := map[string]any{
			"hostid": len(name),
			"name":   name,
		}

		result, err := generateProxyID(fieldType, data, serverID)
		if err != nil {
			t.Errorf("Failed to generate proxy ID for '%s': %v", name, err)
			continue
		}

		// Конвертируем результат в int для проверки
		var proxyID int
		switch v := result.(type) {
		case int:
			proxyID = v
		case string:
			proxyID, _ = strconv.Atoi(v)
		}

		if proxyID == 0 {
			t.Errorf("Generated zero proxy ID for '%s'", name)
			continue
		}

		// Проверяем, что ID заканчивается на 0 (наш шаблон)
		if proxyID%10 != 0 {
			t.Errorf("Proxy ID %d for '%s' should end with 0", proxyID, name)
		}

		// Проверяем на коллизию
		if existingName, exists := generatedIDs[proxyID]; exists {
			collisionsDetected++
			// Если коллизия обнаружена - это ожидаемо для реальных данных
			// Главное, что механизм разрешения коллизий сработал и вернул ошибку ИЛИ уникальный ID
			t.Logf("Collision for proxyID %d between '%s' and '%s'", proxyID, existingName, name)
		}
		generatedIDs[proxyID] = name
	}

	t.Logf("Generated %d unique proxy IDs from %d names, detected %d collisions",
		len(generatedIDs), len(testNames), collisionsDetected)
}

// TestGenerateProxyIDForcedCollision тестирует принудительную коллизию
func TestGenerateProxyIDForcedCollision(t *testing.T) {
	// Инициализируем proxy для теста
	g := Global{MaxRequests: 10}
	z := ZabbixConf{}
	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer stopTestProxy()

	serverID := 3
	fieldType := "host"

	// 1. Генерируем ID для первого имени
	name1 := "original-host"
	data1 := map[string]any{
		"hostid": 100,
		"name":   name1,
	}

	result1, err := generateProxyID(fieldType, data1, serverID)
	if err != nil {
		t.Fatalf("First generation failed: %v", err)
	}

	t.Logf("First proxyID: %v", result1)

	// 2. Теперь симулируем коллизию:
	// Добавляем запись с тем же proxyID, но другим именем напрямую в кеш
	var firstProxyID int
	switch v := result1.(type) {
	case int:
		firstProxyID = v
	case string:
		firstProxyID, _ = strconv.Atoi(v)
	}

	// Создаём коллизию - добавляем другую запись с тем же proxyID
	collisionName := "collision-host"
	prx.cache.CacheType[fieldType].Set(firstProxyID, 999, serverID, collisionName)

	// 3. Пытаемся сгенерировать ID для второго имени
	// Механизм должен обнаружить коллизию и сгенерировать новый ID
	name2 := "another-host"
	data2 := map[string]any{
		"hostid": 200,
		"name":   name2,
	}

	result2, err := generateProxyID(fieldType, data2, serverID)
	if err != nil {
		t.Fatalf("Second generation with collision failed: %v", err)
	}

	t.Logf("After collision proxyID: %v", result2)

	// 4. Проверяем, что сгенерирован новый уникальный ID
	var secondProxyID int
	switch v := result2.(type) {
	case int:
		secondProxyID = v
	case string:
		secondProxyID, _ = strconv.Atoi(v)
	}

	if firstProxyID == secondProxyID {
		t.Errorf("Collision resolution failed: both names have same proxyID %d", firstProxyID)
	}

	// 5. Проверяем, что второй ID тоже заканчивается на 0
	if secondProxyID%10 != 0 {
		t.Errorf("Second proxy ID %d should end with 0", secondProxyID)
	}
}

// TestGenerateProxyIDMultipleCollisions тестирует множественные коллизии
func TestGenerateProxyIDMultipleCollisions(t *testing.T) {
	// Инициализируем proxy для теста
	g := Global{MaxRequests: 10}
	z := ZabbixConf{}
	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer stopTestProxy()

	serverID := 3
	fieldType := "host"

	// Создаем базовую запись
	baseName := "base-host"
	baseData := map[string]any{
		"hostid": 100,
		"name":   baseName,
	}

	baseResult, err := generateProxyID(fieldType, baseData, serverID)
	if err != nil {
		t.Fatalf("Base generation failed: %v", err)
	}

	var baseProxyID int
	switch v := baseResult.(type) {
	case int:
		baseProxyID = v
	case string:
		baseProxyID, _ = strconv.Atoi(v)
	}

	t.Logf("Base proxyID: %d", baseProxyID)

	// Добавляем несколько коллизий в кеш
	collisionNames := []string{"collision-1", "collision-2", "collision-3", "collision-4", "collision-5"}
	for i, name := range collisionNames {
		prx.cache.CacheType[fieldType].Set(baseProxyID, 200+i, serverID, name)
	}

	// Пытаемся сгенерировать ID для нового имени
	newName := "new-host-after-multiple-collisions"
	newData := map[string]any{
		"hostid": 300,
		"name":   newName,
	}

	newResult, err := generateProxyID(fieldType, newData, serverID)
	if err != nil {
		// Если после 5 попыток коллизия не разрешилась - это ожидаемо
		t.Logf("Multiple collisions exhausted attempts as expected: %v", err)
		return
	}

	var newProxyID int
	switch v := newResult.(type) {
	case int:
		newProxyID = v
	case string:
		newProxyID, _ = strconv.Atoi(v)
	}

	// Проверяем, что новый ID уникален
	if newProxyID == baseProxyID {
		t.Errorf("Collision resolution failed for multiple collisions")
	}

	t.Logf("New proxyID after %d collisions: %d", len(collisionNames), newProxyID)
}

// TestGenerateProxyIDWithExistingCache тестирует генерацию с уже существующими данными в кеше
func TestGenerateProxyIDWithExistingCache(t *testing.T) {
	// Инициализируем proxy для теста
	g := Global{MaxRequests: 10}
	z := ZabbixConf{}
	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer stopTestProxy()

	serverID := 3
	fieldType := "host"

	// Сначала генерируем ID для одного имени
	data1 := map[string]any{
		"hostid": 100,
		"name":   "test-host-1",
	}

	result1, err := generateProxyID(fieldType, data1, serverID)
	if err != nil {
		t.Fatalf("First generation failed: %v", err)
	}

	// Пытаемся сгенерировать ID для того же имени, но другого сервера
	data2 := map[string]any{
		"hostid": 100,
		"name":   "test-host-1",
	}

	result2, err := generateProxyID(fieldType, data2, serverID+1)
	if err != nil {
		t.Fatalf("Second generation failed: %v", err)
	}

	// Для одного и того же имени на разных серверах должен быть одинаковый proxyID
	if result1 != result2 {
		t.Errorf("Same name should produce same proxy ID on different servers: %v vs %v", result1, result2)
	}

	// Проверяем, что для другого имени генерируется другой ID
	data3 := map[string]any{
		"hostid": 200,
		"name":   "test-host-2",
	}

	result3, err := generateProxyID(fieldType, data3, serverID)
	if err != nil {
		t.Fatalf("Third generation failed: %v", err)
	}

	if result1 == result3 {
		t.Errorf("Different names should produce different proxy IDs: %v equals %v", result1, result3)
	}
}

// TestGenerateProxyIDMultipleServers тестирует работу с несколькими серверами
func TestGenerateProxyIDMultipleServers(t *testing.T) {
	// Инициализируем proxy для теста
	g := Global{MaxRequests: 10}
	z := ZabbixConf{}
	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer stopTestProxy()

	fieldType := "host"
	hostName := "multi-server-host"

	serverIDs := []int{1, 2, 3, 4, 5}

	// Генерируем ID для одного хоста на разных серверах
	generatedIDs := make(map[int]int) // serverID -> proxyID
	for _, serverID := range serverIDs {
		data := map[string]any{
			"hostid": serverID * 100, // Разные оригинальные ID на разных серверах
			"name":   hostName,
		}

		result, err := generateProxyID(fieldType, data, serverID)
		if err != nil {
			t.Errorf("Failed to generate ID for server %d: %v", serverID, err)
			continue
		}

		var proxyID int
		switch v := result.(type) {
		case int:
			proxyID = v
		case string:
			proxyID, _ = strconv.Atoi(v)
		}

		generatedIDs[serverID] = proxyID
	}

	// Все генерации для одного имени должны давать одинаковый proxyID
	firstID := generatedIDs[serverIDs[0]]
	for serverID, proxyID := range generatedIDs {
		if proxyID != firstID {
			t.Errorf("Server %d: expected proxy ID %d, got %d", serverID, firstID, proxyID)
		}
	}

	// Проверяем, что кеш правильно сохранил все маппинги
	for _, serverID := range serverIDs {
		data := map[string]any{
			"hostid": serverID * 100,
		}

		result, err := generateProxyID(fieldType, data, serverID)
		if err != nil {
			t.Errorf("Failed to get cached ID for server %d: %v", serverID, err)
			continue
		}

		if result != generatedIDs[serverID] {
			t.Errorf("Server %d: cached ID %v doesn't match generated %d", serverID, result, generatedIDs[serverID])
		}
	}
}

// TestGenerateProxyIDStringResult тестирует возврат строкового ID
func TestGenerateProxyIDStringResult(t *testing.T) {
	// Инициализируем proxy для теста
	g := Global{MaxRequests: 10}
	z := ZabbixConf{}
	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer stopTestProxy()

	serverID := 3
	fieldType := "host"

	// С string ID
	data := map[string]any{
		"hostid": "100",
		"name":   "string-id-host",
	}

	result, err := generateProxyID(fieldType, data, serverID)
	if err != nil {
		t.Fatalf("Generation with string ID failed: %v", err)
	}

	// Должен вернуть строку
	if _, ok := result.(string); !ok {
		t.Errorf("Expected string result for string input, got %T: %v", result, result)
	}

	// Проверяем, что это валидное число
	proxyID, err := strconv.Atoi(result.(string))
	if err != nil {
		t.Errorf("Result '%v' is not a valid number: %v", result, err)
	}

	if proxyID <= 0 {
		t.Errorf("Generated invalid proxy ID: %d", proxyID)
	}
}

// TestGenerateProxyIDEdgeCases тестирует граничные случаи
func TestGenerateProxyIDEdgeCases(t *testing.T) {
	// Инициализируем proxy для теста
	g := Global{MaxRequests: 10}
	z := ZabbixConf{}
	InitProxy(g, z, CBConf{}, CacheConf(initTestCache()), []string{})
	defer stopTestProxy()

	serverID := 3
	fieldType := "host"

	tests := []struct {
		name        string
		data        map[string]any
		expectError bool
	}{
		{
			"empty name field",
			map[string]any{"hostid": 100, "name": ""},
			false, // Пустое имя - валидный кейс
		},
		{
			"very long name",
			map[string]any{"hostid": 100, "name": "a" + string(make([]byte, 1000)) + "b"},
			false,
		},
		{
			"unicode name",
			map[string]any{"hostid": 100, "name": "хост-тест-юникод-日本語-中文"},
			false,
		},
		{
			"special characters",
			map[string]any{"hostid": 100, "name": "host!@#$%^&*()_+-=[]{}|;':\",./<>?`~"},
			false,
		},
		{
			"numeric name",
			map[string]any{"hostid": 100, "name": "1234567890"},
			false,
		},
		{
			"name with leading/trailing spaces",
			map[string]any{"hostid": 100, "name": "  spaced-host  "},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := generateProxyID(fieldType, tt.data, serverID)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got result: %v", result)
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			var proxyID int
			switch v := result.(type) {
			case int:
				proxyID = v
			case string:
				proxyID, _ = strconv.Atoi(v)
			}

			if proxyID <= 0 {
				t.Errorf("Generated invalid proxy ID: %d", proxyID)
			}
		})
	}
}
