# ZabbixAPIproxy - HTTP-прокси для работы с несколькими серверами Zabbix

Выберите язык / Choose language:
- [Русский](#русский)
- [English](#english)

---

## Русский

ZabbixAPIproxy
- HTTP-прокси для работы с несколькими Zabbix серверами (до 9).
- Проксирование и агрегация ответов для Grafana.
- Двунаправленный маппинг ID: OriginalID ↔ ProxyID (включая кодирование serverID).
- Локальный кеш (BoltDB), автосохранение, метрики Prometheus.

Задача и принцип работы
- Прокси принимает http запросы и пересылает их к одному или нескольким Zabbix серверам.
- Все ID, полученные от Zabbix, заменяются на уникальные ProxyID и кэшируются вместе с информацией о сервере и имени сущности.
- При обращении по ProxyID прокси дешифрует целевой сервер и оригинальный ID, направляя запрос на правильный Zabbix сервер.
- Агрегирует ответы с нескольких серверов.

Зачем нужен Circuit Breaker
- Предотвращает излишние вызовы к недоступным или нестабильным Zabbix серверам.
- Защищает ресурсы прокси (пулы соединений, горутины) от перегрузки.
- Позволяет быстро переключиться на отказоустойчивое поведение (временная блокировка запросов к проблемному серверу) и автоматически восстановиться после заданного таймаута.
- Упрощает мониторинг и диагностику — ошибки и открытые/закрытые состояния фиксируются в метриках.

Конфигурация (основные параметры)
- global.listen_addr — адрес и порт прокси.
- global.auth_token — токен для авторизации входящих запросов (если задан).
- global.request_timeout — таймаут для запросов к Zabbix серверам.
- zabbix.servers[]:
  - id — числовой идентификатор сервера (используется при кодировании ProxyID).
  - name — читаемое имя.
  - url — URL API (api_jsonrpc.php).
  - token — API токен сервера.
  - ignore_ssl — при true — игнорировать ошибки TLS (не рекомендуется в проде).
- cache:
  - TTL — время жизни записей кэша.
  - CleanupInterval — интервал очистки устаревших записей.
  - DBPath — путь к BoltDB (используйте файл в проде, ":memory:" для тестов).
  - AutoSave — период автосохранения кеша.
  - CachedFields — какие поля сущностей кешировать (например hostid, name).
- circuit_breaker:
  - enabled — включить CB.
  - max_consecutive_failures — число ошибок до срабатывания.
  - timeout — время «вскрытия» (open) перед попыткой восстановления.
  - request_window — окно учета ошибок.
- logging.level/file — уровень и вывод логов.
- prometheus.enabled/listen_addr — включение и адрес метрик.
- proxy.max_requests — ограничение конкурентных запросов (опционально).

Сборка и запуск
- Сборка:
  go build -ldflags="-X main.version=$(git describe --tags)" -o ./bin/ZabbixAPIproxy ./cmd/app
- Запуск:
  ./bin/ZabbixAPIproxy -c config.yaml

---

## English

Summary
- HTTP proxy for multiple Zabbix servers (up to 9).
- Request routing and response aggregation for Grafana.
- Bidirectional mapping OriginalID ↔ ProxyID (includes serverID encoding).
- Local cache (BoltDB), autosave, Prometheus metrics.

How it works
- Proxy accepts http requests and forwards them to one or more Zabbix servers.
- IDs returned by Zabbix are replaced with unique ProxyIDs and stored in a cache with server and name metadata.
- On requests using ProxyID the proxy decodes target server and original ID, routing the request to the correct backend or aggregating across servers.

Why Circuit Breaker is used
- Prevents repeated calls to unavailable or unstable Zabbix servers.
- Protects proxy resources (connections, goroutines) from overload.
- Provides temporary blocking of problematic backends and automatic recovery after timeout.
- Improves observability — CB state and errors are exposed via metrics.

Configuration highlights
- global.listen_addr — proxy listen address.
- global.auth_token — incoming request auth token (optional).
- global.request_timeout — timeout for backend requests.
- zabbix.servers[]:
  - id — numeric server id used in ProxyID encoding.
  - name, url, token, ignore_ssl.
- cache:
  - TTL, CleanupInterval, DBPath (file or ":memory:"), AutoSave, CachedFields.
- circuit_breaker:
  - enabled, max_consecutive_failures, timeout, request_window.
- logging.level/file — log settings.
- prometheus.* — metrics options.
- proxy.max_requests — concurrency limit.

Build & run
- Build:
  go build -ldflags="-X main.version=$(git describe --tags)" -o ./bin/ZabbixAPIproxy ./cmd/app
- Run:
  ./bin/ZabbixAPIproxy -c config.yaml