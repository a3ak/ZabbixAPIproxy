Выберите язык / Choose language:
- [Русский](#русский)
- [English](#english)

---

## Русский

### 9. Обработка коллизий FNV-32a при генерации ProxyID
- **Файл**: `internal/proxy/proxyIDprocessing.go`
- **Проблема**: При коллизии хешей FNV-32a (ситуация, когда два разных имени сущности генерируют одинаковый ProxyID) данные перезаписывались без уведомления, что приводило к потере маппингов и некорректной работе системы
- **Исправление**: 
  - Добавлена проверка существования ProxyID в кеше перед записью
  - При обнаружении коллизии выполняется перегенерация уникального ID с добавлением суффикса и уменьшением диапазона
  - Реализовано до 5 попыток разрешения коллизии, после чего возвращается ошибка
  - Добавлена функция `GetEntityName` для проверки принадлежности ProxyID конкретному имени

### 10. Тесты механизма разрешения коллизий и GetEntityName
- **Файлы**: `internal/proxy/proxyIDprocessing_test.go`, `internal/cache/cache_test.go`
- **Что добавлено**:
  - **7 новых тестов** для механизма разрешения коллизий:
    - Проверка уникальности генерируемых ID для разных имен
    - Принудительное создание коллизии и проверка её разрешения
    - Множественные коллизии (до 5 попыток)
    - Повторная генерация для одного имени (возврат существующего ID)
    - Одно имя на разных серверах (одинаковый ProxyID)
    - Возврат строкового типа для string ID
    - Граничные случаи (unicode, спецсимволы, пустые строки)
  - **8 новых тестов** для функции `GetEntityName`:
    - Базовые проверки (существующий/несуществующий ID)
    - Независимость от serverID
    - Обновление имени
    - Удаление записи
    - Очистка по TTL
    - Конкурентный доступ
    - Интеграционный тест (полный цикл)
    - Специальные символы в именах

---

## English

### 9. FNV-32a Collision Handling in ProxyID Generation
- **File**: `internal/proxy/proxyIDprocessing.go`
- **Issue**: On FNV-32a hash collision (when two different entity names generate the same ProxyID), data was overwritten without notification, causing mapping loss and incorrect system behavior
- **Fix**: 
  - Added ProxyID existence check in cache before writing
  - On collision detection, generates a unique ID with suffix and reduced range
  - Implemented up to 5 resolution attempts, returns error after exhaustion
  - Added `GetEntityName` function to verify ProxyID ownership by name

### 10. Tests for Collision Resolution and GetEntityName
- **Files**: `internal/proxy/proxyIDprocessing_test.go`, `internal/cache/cache_test.go`
- **What was added**:
  - **7 new tests** for collision resolution mechanism:
    - Uniqueness verification for different names
    - Forced collision creation and resolution check
    - Multiple collisions (up to 5 attempts)
    - Same name regeneration (returns existing ID)
    - Same name on different servers (same ProxyID)
    - String type return for string IDs
    - Edge cases (unicode, special characters, empty strings)
  - **8 new tests** for `GetEntityName` function:
    - Basic checks (existing/non-existing ID)
    - ServerID independence
    - Name update
    - Record deletion
    - TTL cleanup
    - Concurrent access
    - Integration test (full cycle)
    - Special characters in names

## 📊 Изменения / Changes

| # | Компонент / Component | Файл / File | Тип / Type |
|---|----------------------|-------------|------------|
| 9 | ID processing | `proxyIDprocessing.go` | 🐛 Багфикс / Bugfix |
| 10 | Tests | `*_test.go` | ✅ Тесты / Tests |