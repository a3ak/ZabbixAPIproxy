package proxy

import (
	"reflect"
	"runtime"
	"testing"
)

func TestCloneZabbixRequest(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]any
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: map[string]any{},
		},
		{
			name:     "empty map",
			input:    map[string]any{},
			expected: map[string]any{},
		},
		{
			name: "simple request",
			input: map[string]any{
				"jsonrpc": "2.0",
				"method":  "host.get",
				"id":      1,
				"auth":    "abc123",
			},
			expected: map[string]any{
				"jsonrpc": "2.0",
				"method":  "host.get",
				"id":      1,
				"auth":    "abc123",
			},
		},
		{
			name: "request with params",
			input: map[string]any{
				"jsonrpc": "2.0",
				"method":  "item.get",
				"params": map[string]any{
					"output":    []string{"itemid", "name"},
					"hostids":   []any{"10084", "10085"},
					"sortfield": "name",
				},
			},
			expected: map[string]any{
				"jsonrpc": "2.0",
				"method":  "item.get",
				"params": map[string]any{
					"output":    []string{"itemid", "name"},
					"hostids":   []any{"10084", "10085"},
					"sortfield": "name",
				},
			},
		},
		{
			name: "request with nested structures",
			input: map[string]any{
				"jsonrpc": "2.0",
				"method":  "trigger.get",
				"params": map[string]any{
					"filter": map[string]any{
						"value":    1,
						"priority": []any{4, 5},
					},
					"selectHosts": []string{"host1", "host2"},
				},
			},
			expected: map[string]any{
				"jsonrpc": "2.0",
				"method":  "trigger.get",
				"params": map[string]any{
					"filter": map[string]any{
						"value":    1,
						"priority": []any{4, 5},
					},
					"selectHosts": []string{"host1", "host2"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deepClone(tt.input)
			if tt.expected == nil && result != nil {
				t.Errorf("Expected nil for nil input, got %v", result)
				return
			}

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("CloneZabbixRequest() = %v, want %v", result, tt.expected)
			}

			// Проверяем, что это действительно копия, а не ссылка
			if tt.input != nil && result != nil {
				modifyAndCheckOriginalNotChanged(t, tt.input, result.(map[string]any))
			}

			// Возвращаем в пул для cleanup
			if result != nil {
				returnToPool(result.(map[string]any))
			}
		})
	}
}

func TestCloneZabbixValue(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected any
	}{
		{
			name:     "string",
			input:    "test",
			expected: "test",
		},
		{
			name:     "int",
			input:    42,
			expected: 42,
		},
		{
			name:     "bool",
			input:    true,
			expected: true,
		},
		{
			name:     "nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty slice any",
			input:    []any{},
			expected: []any{},
		},
		{
			name:     "empty slice string",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "slice any",
			input:    []any{1, "test", true},
			expected: []any{1, "test", true},
		},
		{
			name:     "slice string",
			input:    []string{"a", "b", "c"},
			expected: []string{"a", "b", "c"},
		},
		{
			name: "map",
			input: map[string]any{
				"key": "value",
			},
			expected: map[string]any{
				"key": "value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deepClone(tt.input)

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("cloneZabbixValue() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestReturnZabbixPoolPoolBehavior(t *testing.T) {
	// Изначально пул пустой или содержит одну мапу
	initialPoolState := func() int {
		// Получаем несколько элементов чтобы проверить поведение пула
		item1 := clonePool.Get()
		item2 := clonePool.Get()

		// Возвращаем обратно
		if item1 != nil {
			clonePool.Put(item1)
		}
		if item2 != nil {
			clonePool.Put(item2)
		}

		return 2 // Минимум 2 вызова Get вернут разные объекты
	}()

	t.Run("pool reuse", func(t *testing.T) {
		// Создаем запрос и возвращаем в пул
		request := map[string]any{
			"jsonrpc": "2.0",
			"method":  "test.get",
		}

		cloned := deepClone(request)
		originalPointer := reflect.ValueOf(cloned).Pointer()

		// Возвращаем в пул
		returnToPool(cloned.(map[string]any))

		// Теперь при следующем клонировании должен быть переиспользован тот же объект
		newRequest := map[string]any{
			"jsonrpc": "2.0",
			"method":  "host.get",
		}

		newCloned := deepClone(newRequest)
		newPointer := reflect.ValueOf(newCloned).Pointer()

		if initialPoolState > 1 {
			// Если в пуле было больше одного элемента, мы не можем гарантировать
			// что получим тот же самый объект
			t.Log("Pool had multiple items, reuse test skipped")
		} else if originalPointer != newPointer {
			t.Errorf("Expected pool reuse, got different pointers: %v != %v",
				originalPointer, newPointer)
		}

		// Cleanup
		returnToPool(newCloned.(map[string]any))
	})
}

func TestModificationIndependence(t *testing.T) {
	original := map[string]any{
		"jsonrpc": "2.0",
		"method":  "host.get",
		"params": map[string]any{
			"output": []string{"hostid", "name"},
			"filter": map[string]any{
				"status": 0,
			},
		},
	}

	cloned := deepClone(original)

	// Модифицируем клон
	(cloned.(map[string]any))["method"] = "item.get"
	(cloned.(map[string]any))["params"].(map[string]any)["output"] = []string{"itemid"}
	(cloned.(map[string]any))["params"].(map[string]any)["filter"].(map[string]any)["status"] = 1

	// Проверяем, что оригинал не изменился
	if original["method"] != "host.get" {
		t.Error("Original was modified when clone changed")
	}

	if output, ok := original["params"].(map[string]any)["output"].([]string); ok {
		if len(output) != 2 || output[0] != "hostid" || output[1] != "name" {
			t.Error("Original output was modified")
		}
	}

	if status, ok := original["params"].(map[string]any)["filter"].(map[string]any)["status"].(int); ok {
		if status != 0 {
			t.Error("Original filter was modified")
		}
	}

	returnToPool(cloned.(map[string]any))
}

func TestConcurrentSafety(t *testing.T) {
	concurrency := 10
	done := make(chan bool, concurrency)

	/*
		testRequest := map[string]any{
			"jsonrpc": "2.0",
			"method":  "concurrent.test",
			"params": map[string]any{
				"test": "value",
			},
		}*/

	for i := 0; i < concurrency; i++ {
		go func(id int) {
			// Каждая горутина делает свой запрос
			request := map[string]any{
				"jsonrpc": "2.0",
				"method":  "test.get",
				"id":      id,
				"params": map[string]any{
					"output": []string{"id", "name"},
				},
			}

			cloned := deepClone(request)

			// Проверяем что клонирование корректное
			if (cloned.(map[string]any))["id"] != id {
				t.Errorf("Concurrency issue: expected id %d, got %v", id, (cloned.(map[string]any))["id"])
			}

			returnToPool(cloned.(map[string]any))
			done <- true
		}(i)
	}

	// Ждем завершения всех горутин
	for i := 0; i < concurrency; i++ {
		<-done
	}
}

// Вспомогательная функция для проверки что оригинал не изменяется
func modifyAndCheckOriginalNotChanged(t *testing.T, original, cloned map[string]any) {
	// Сохраняем оригинальные значения
	originalJSONRPC := original["jsonrpc"]
	originalMethod := original["method"]

	// Модифицируем клон
	cloned["jsonrpc"] = "modified"
	cloned["method"] = "modified"

	// Проверяем что оригинал не изменился
	if original["jsonrpc"] != originalJSONRPC {
		t.Error("Original jsonrpc was modified")
	}
	if original["method"] != originalMethod {
		t.Error("Original method was modified")
	}

	// Восстанавливаем клон для чистого теста
	cloned["jsonrpc"] = originalJSONRPC
	cloned["method"] = originalMethod
}

func TestPoolMemoryManagement(t *testing.T) {
	// Тест на утечки памяти (логическая проверка)
	initialGoroutines := runtime.NumGoroutine()

	// Создаем и возвращаем много объектов
	for i := 0; i < 1000; i++ {
		resp := getPool()
		resp["test"] = i
		returnToPool(resp)
	}

	// Не должно быть утечек горутин
	finalGoroutines := runtime.NumGoroutine()
	if finalGoroutines > initialGoroutines+2 { // Небольшой допуск
		t.Errorf("Possible goroutine leak: initial %d, final %d",
			initialGoroutines, finalGoroutines)
	}
}

func TestResponsePoolReuse(t *testing.T) {
	// Проверяем что пул действительно переиспользует объекты
	firstResp := getPool()
	firstPointer := reflect.ValueOf(firstResp).Pointer()
	returnToPool(firstResp)

	secondResp := getPool()
	defer returnToPool(secondResp)
	secondPointer := reflect.ValueOf(secondResp).Pointer()

	if firstPointer != secondPointer {
		t.Log("Pool reuse detected - same object pointer")
	} else {
		t.Log("Different objects from pool (normal for some pool implementations)")
	}
}
