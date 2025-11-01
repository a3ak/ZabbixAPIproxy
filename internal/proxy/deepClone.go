package proxy

import (
	"sync"
)

// Пул map для клонирования запросов
var clonePool = &sync.Pool{
	New: func() any {
		return make(map[string]any, 30)
	},
}

// Получение мапы из пула
func getPool() map[string]any {
	resp := clonePool.Get().(map[string]any)
	// Очищаем перед возвратом
	clear(resp)
	return resp
}

// Глубокое клонирование структур
func deepClone(src any) any {
	switch v := src.(type) {
	case map[string]any:
		if v == nil {
			return map[string]any{}
		}
		// Пытаемся взять из пула
		dst := getPool()
		// Очищаем мапу перед использованием
		clear(dst)

		// Быстрое копирование с оптимизацией для структуры Zabbix
		for key, value := range v {
			switch key {
			case "jsonrpc", "method", "id", "auth":
				// Простые поля - копируем как есть
				dst[key] = value
			default:
				// Остальные поля обрабатываем рекурсивно
				dst[key] = deepClone(value)
			}
		}
		return dst

	case []any:
		// Клонируем массивы
		if len(v) == 0 {
			return []any{}
		}

		dst := make([]any, len(v))
		for i, val := range v {
			dst[i] = deepClone(val)
		}
		return dst
	default:
		return v
	}
}

// Функция для возврата мапы в пул (вызывать после использования)
func returnToPool(m map[string]any) {
	if m != nil {
		clonePool.Put(m)
	}
}
