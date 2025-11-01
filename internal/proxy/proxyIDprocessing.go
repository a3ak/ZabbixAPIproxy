package proxy

import (
	"ZabbixAPIproxy/internal/logger"
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

var (
	//Поля которые требуется дедуплицировать
	dedupFileds = []string{"group"}
)

// Проверка на пустоту
func isEmpty(data any) bool {
	switch v := data.(type) {
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	default:
		return v == nil // Неизвестный тип сравниваем с nil
	}
}

func isZeroID(id any) bool {
	switch v := id.(type) {
	case float64:
		return v == 0
	case int:
		return v == 0
	case string:
		return v == "0"
	default:
		return false
	}

}

// Стандартная замена ID = OriginalID *10 + serverID
func simpleModifyID(id any, serverID int) any {
	if isZeroID(id) {
		logger.Global.Debugf("Skipping zero ID: %v", id)
		return id
	}
	switch v := id.(type) {
	case float64:
		return int(v)*10 + serverID
	case int:
		return v*10 + serverID
	case string:
		if num, err := strconv.Atoi(v); err == nil {
			return strconv.Itoa(num*10 + serverID)
		}
		logger.Global.Warningf("Non-numeric ID found: %s", v)
		return v
	default:
		logger.Global.Warningf("Unexpected ID type: %T", id)
		return id
	}

}

// generateProxyID генерирует ProxyID на основе имени сущности и записываем данные в кеш
func generateProxyID(fieldType string, data map[string]any, serverID int) (any, error) {
	// Забираем из структуры поле с ID для даноого типа
	if origID, ok := data[fieldType+"id"]; ok {
		var intOrigID int     //Для преобразованного в INT значения OriginID
		var origIDtype string //Для сохранения исходного типа ID
		switch w := origID.(type) {
		case string:
			var err error
			origIDtype = "string"
			intOrigID, err = strconv.Atoi(w)
			if err != nil {
				// Обработка ошибки
				logger.Global.Errorf("[ServerID:%d] ID transformation error: '%s' for the field of type '%s' structure: '%v': %s", serverID, w, fieldType, data, err)
				return 0, err
			}
		case int:
			origIDtype = "int"
			intOrigID = w
		}

		var proxyID int

		//Проеряем, что кеш инициализирован
		if prx.cache == nil {
			return 0, fmt.Errorf("proxy cache is not initialized")
		}

		//проверяем наличие ProxyID в кеше
		if val, _ := prx.cache.CacheType[fieldType].GetProxyID(intOrigID, serverID); val != 0 {
			proxyID = val
		} else {
			// Проверям в структуре наличие поля для генерации ID(имя объекта).
			if m, ok := data[prx.cachedFields[fieldType]]; ok {
				//проверяем, что это строка
				switch v := m.(type) {
				case string:
					//Генерируем кеш от имени объекта
					h := fnv.New32a()
					h.Write([]byte(v))

					//Забираем 6 последник цифр и умножаем на 0, что бы получить PorxyID с 0 в конце для более простой идентификации ProxyID
					proxyID = int(h.Sum32()) % 10000000 * 10

					//Пооизводим запись в кеш
					prx.cache.CacheType[fieldType].Set(proxyID, intOrigID, serverID, v)

					logger.Global.Tracef(`Generated proxyID[%d] for id '%s' based on the field 'name': %s. Recrod to the cash: %d -> {%d: %d}`, proxyID, fieldType, v, proxyID, serverID, intOrigID)
				}
			} else {
				return 0, fmt.Errorf("failed to generate proxy ID for type %s.Field '%s' was not found", fieldType, prx.cachedFields[fieldType])
			}
		}

		//Возвращаем сгенерированный ProxyID
		if proxyID != 0 {
			if origIDtype == "int" {
				return proxyID, nil
			}
			return strconv.Itoa(proxyID), nil
		}
	}
	return 0, fmt.Errorf("failed to generate proxy ID for type %s", fieldType)
}

func convertProxyIDToOriginal(id any, serverID int, cacheType string) any {
	cacheType = strings.TrimSuffix(cacheType, "ids")
	cacheType = strings.TrimSuffix(cacheType, "id")
	switch proxyID := id.(type) {
	case float64:
		intproxyID := int(proxyID)
		if cashedItems, ok := prx.cache.CacheType[cacheType].GetOriginalID(intproxyID, serverID); ok {
			logger.Global.Tracef("For Server[%d] Proxyid %d was transformed into OriginalID %d from cache[%s]", serverID, intproxyID, cashedItems, cacheType)
			return cashedItems
		} else {
			logger.Global.Tracef("For Server[%d] Proxyid %d not found OriginalID %d from cache[%s]", serverID, intproxyID, cashedItems, cacheType)

		}
	case int:
		if cashedItems, ok := prx.cache.CacheType[cacheType].GetOriginalID(proxyID, serverID); ok {
			logger.Global.Tracef("For Server[%d] Proxyid %d was transformed into OriginalID %d from cache[%s]", serverID, proxyID, cashedItems, cacheType)
			return cashedItems
		} else {
			logger.Global.Tracef("For Server[%d] Proxyid %d not found OriginalID %d from cache[%s]", serverID, proxyID, cashedItems, cacheType)

		}
	case string:
		if intproxyID, err := strconv.Atoi(proxyID); err == nil {
			if cashedItems, ok := prx.cache.CacheType[cacheType].GetOriginalID(intproxyID, serverID); ok {
				logger.Global.Tracef("For Server[%d] Proxyid %s was transformed into OriginalID '%d' from cache[%s]", serverID, proxyID, cashedItems, cacheType)
				return strconv.Itoa(cashedItems)
			} else {
				logger.Global.Tracef("For Server[%d] Proxyid %s not found OriginalID %d from cache[%s]", serverID, proxyID, cashedItems, cacheType)
			}
		}
	}
	return nil

}

// Провекра, что строка содержит только цифры
func isPureDigitString(s string) bool {
	if len(s) == 0 {
		return false
	}

	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// Проверка, что запрос строится на основе ID
func isIDBasedRequest(request map[string]any) (bool, []string) {
	if params, ok := request["params"].(map[string]any); ok {
		keys := make(map[string]bool)
		for key, val := range params {
			if !strings.HasSuffix(key, "ids") {
				continue
			}

			switch v := val.(type) {
			case []any:
				if len(v) > 0 {
					keys[key] = true
				}
			case []string:
				if len(v) > 0 {
					keys[key] = true
				}
			case []int:
				if len(v) > 0 {
					keys[key] = true
				}
			case nil:
				continue
			default:
				logger.Global.Warningf("Unexpected type for field %s: %T", key, val)
			}

		}

		if len(keys) != 0 {
			var arr []string
			for i := range keys {
				arr = append(arr, i)
			}
			return true, arr
		}
	}

	return false, nil
}

// Если ключ карты(словаря) является ID, то модифицируем его по простому принципу *10+serverID
func ifIDBasedResponseSimpleModify(data map[string]any, serverID int) {
	var keys []string
	var isKeyDigits bool

	for i := range data {
		if isKeyDigits {
			keys = append(keys, i)
		} else {
			if !isPureDigitString(i) {
				return
			}

			isKeyDigits = true
			keys = append(keys, i)
		}
	}
	if len(keys) > 0 {
		newData := make(map[string]any)
		for _, key := range keys {
			new_key := simpleModifyID(key, serverID)
			newData[new_key.(string)] = data[key]
			delete(data, key)
		}
		maps.Copy(data, newData)
	}
}

func getServerFromID(id any) int {
	switch v := id.(type) {
	case float64:
		if v < 10 {
			return 0
		}
		return int(v) % 10
	case int:
		if v < 10 {
			return 0
		}
		return v % 10
	case string:
		if id, err := strconv.Atoi(v); err == nil {
			if id < 10 {
				return 0
			}
			return id % 10
		}
	}
	return 0
}

func convertGrafanaIDToOriginal(id any, serverID int) any {
	switch v := id.(type) {
	case float64:
		grafanaID := int(v)
		if grafanaID%10 == serverID {
			return grafanaID / 10
		}
	case int:
		if v%10 == serverID {
			return v / 10
		}
	case string:
		if grafanaID, err := strconv.Atoi(v); err == nil {
			if grafanaID%10 == serverID {
				return grafanaID / 10
			}
		}
		return v
	}
	return nil
}

// processResponseIDs обрабатывает ответы от сервера, подставляя ProxyID и удаляя дубликаты
// data - данные для обработки (может быть slice, map или примитивом)
// serverID - идентификатор сервера для генерации proxy ID
// uniqProxyID - карта для отслеживания уникальных proxy ID (предотвращение дубликатов)
// mu - RWMutex для безопасной работы с картой уникальных ID в конкурентной среде
// deepLevel - уровень вложенности (0 - верхний уровень, где нужно удалять дубликаты)
// возвращает обработанные данные с подставленными proxy ID или nil для фильтрации дубликатов
func processResponseIDs(data any, serverID int, uniqProxyID map[string]map[any]bool, mu *sync.RWMutex, deepLevel int) any {
	switch v := data.(type) {
	case []any:
		//Массив отфильтрованных данных
		filtered := make([]any, 0, len(v))

		// Обрабатываем слайс, удаляем элементы, если shouldDelete = true
		for _, item := range v {
			if processResponseIDs(item, serverID, uniqProxyID, mu, deepLevel+1) != nil {
				filtered = append(filtered, item)
			}
		}
		// Возвращаем отфильтрованный slice
		return filtered
	case map[string]any:
		//Проверка для структур где ID являются ключем, например при пролучении problem.get
		ifIDBasedResponseSimpleModify(v, serverID)

		// Обрабатываем каждое поле map
		for key, value := range v {
			if isIDField(key) {
				// Если поле является ID-полем (оканчивается на "id" но не просто "id")
				v[key] = processIDField(key, value, v, serverID, uniqProxyID, mu, deepLevel)

			} else {
				processResponseIDs(value, serverID, uniqProxyID, mu, deepLevel+1)
			}
		}
		return v
	}
	return nil
}

// isIDField проверяет является ли поле ID-полем
// key - имя поля для проверки
// возвращает true если поле оканчивается на "id" но не равно просто "id"
func isIDField(key string) bool {
	return strings.HasSuffix(key, "id") && key != "id"
}

// processIDField обрабатывает поле содержащее ID
// key - имя поля ID (например "hostid", "groupid")
// value - текущее значение поля
// data - вся map данных (нужна для генерации proxy ID)
// возвращает обработанное значение ID (proxy ID или модифицированный оригинальный ID)
func processIDField(key string, value any, data map[string]any, serverID int, uniqProxyID map[string]map[any]bool, mu *sync.RWMutex, deepLevel int) any {
	// Извлекаем тип сущности из имени поля (например "host" из "hostid")
	fieldType := strings.TrimSuffix(key, "id")

	// Проверяем нужно ли для этого типа сущности использовать кешированные proxy ID
	if _, ok := prx.cachedFields[fieldType]; ok {
		// Для кешируемых сущностей генерируем proxy ID на основе имени
		return processCachedIDField(fieldType, value, data, serverID, uniqProxyID, mu, deepLevel)
	}
	// Для некешируемых сущностей используем простое преобразование ID
	return simpleModifyID(value, serverID)
}

// processCachedIDField обрабатывает ID поле для кешируемых сущностей
// fieldType - тип сущности (например "host", "group")
// value - текущее значение ID поля
// data - вся map данных (нужна для доступа к полю имени для генерации хеша)
// возвращает сгенерированный proxy ID или оригинальное значение в случае ошибки
func processCachedIDField(fieldType string, value any, data map[string]any, serverID int, uniqProxyID map[string]map[any]bool, mu *sync.RWMutex, deepLevel int) any {
	// Генерируем proxy ID на основе имени сущности
	id, err := generateProxyID(fieldType, data, serverID)
	if err != nil {
		// В случае ошибки генерации логируем ошибку и возвращаем оригинальное значение
		logger.Global.Errorf("server[%d]: ProxyID generation failed for %s: %v", serverID, fieldType, err)
		return value
	}

	// Проверяем нужно ли проверять дубликаты для этого типа сущности на текущем уровне вложенности
	if deepLevel == 1 && slices.Contains(dedupFileds, fieldType) {
		// Для group сущностей на верхнем уровне проверяем дубликаты
		if isDuplicateID(id, fieldType, uniqProxyID, mu) {
			// Если дубликат найден - возвращаем nil для фильтрации элемента
			return nil
		}
		// Помечаем ID как обработанный чтобы предотвратить будущие дубликаты
		markIDAsProcessed(id, fieldType, uniqProxyID, mu)
	}

	return id
}

// isDuplicateID проверяет является ли ID дубликатом
// id - proxy ID для проверки
// fieldType - тип сущности
// uniqProxyID - карта отслеживания уникальных ID
// mu - RWMutex для безопасного доступа к карте
// возвращает true если ID уже существует (дубликат)
func isDuplicateID(id any, fieldType string, uniqProxyID map[string]map[any]bool, mu *sync.RWMutex) bool {
	mu.RLock()         // Блокировка для чтения
	defer mu.RUnlock() // Гарантированное разблокирование

	// Проверяем существует ли карта для данного типа сущности и содержит ли она данный ID
	if fieldMap, exists := uniqProxyID[fieldType]; exists {
		return fieldMap[id]
	}
	return false
}

// markIDAsProcessed помечает ID как обработанный для предотвращения дубликатов
// id - proxy ID для пометки
// fieldType - тип сущности
// uniqProxyID - карта отслеживания уникальных ID
// mu - RWMutex для безопасного доступа к карте
func markIDAsProcessed(id any, fieldType string, uniqProxyID map[string]map[any]bool, mu *sync.RWMutex) {
	mu.Lock()         // Блокировка для записи
	defer mu.Unlock() // Гарантированное разблокирование

	// Инициализируем карту для типа сущности если она не существует
	if uniqProxyID[fieldType] == nil {
		uniqProxyID[fieldType] = make(map[any]bool)
	}
	// Помечаем ID как обработанный
	uniqProxyID[fieldType][id] = true
}
