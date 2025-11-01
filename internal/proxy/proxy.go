package proxy

import (
	"ZabbixAPIproxy/internal/cache"
	"ZabbixAPIproxy/internal/logger"
	"ZabbixAPIproxy/internal/zabbix"
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/a3ak/circuitbreaker"
	"github.com/a3ak/suffix"
)

// Структура для конфигурации Circuit Breaker
type CBConf circuitbreaker.CircuitBreakerConf

// Структура для конфигурации кеша
type CacheConf cache.CacheCfg

// Структура для конфигурации zabbix server
type ZabbixConf zabbix.Zabbix

// Структура для конфигурации proxy
type Global struct {
	ListenAddr string `yaml:"listen_addr"`
	Token      string `yaml:"token"`
	Login      string `yaml:"login"`
	Password   string `yaml:"password"`

	ReadTimeout     string `yaml:"read_timeout"`
	WriteTimeout    string `yaml:"write_timeout"`
	IdleTimeout     string `yaml:"idle_timeout"`
	MaxTimeout      string `yaml:"max_timeout"`
	maxTimeoutInt64 int64

	MaxReqBodySize      string `yaml:"max_req_body_size"`
	MaxRequests         int    `yaml:"max_requests"`
	maxReqBodySizeInt64 int64

	MetricPath      string `yaml:"metric_path"`
	MonitoringInLog bool   `yaml:"monitoring_in_log"`
}

// Структура Proxy
type proxy struct {
	// Имена ID сущностей для которых требуется генерация ID и поле по которому генерируется хеш
	cachedFields map[string]string

	// перечень методов которые требуется исключить из Trace логирования
	excludeRequests []string

	// Переменная для кеша
	cache *cache.CacheEntry

	// Переменная для Circuit Breaker
	cb *circuitbreaker.CBManager

	// Глобальный конфиг proxy
	global Global

	// Список zabbix серверов из конфига
	config ZabbixConf

	// Добавляем переменную для лимита одновременных запросов
	requestSemaphore chan struct{}

	zbxClient zabbix.ZabbixClient
}

// Инициализация первичных параметров proxy
func NewProxy(g Global, z ZabbixConf, excludeLog []string) proxy {
	return proxy{cachedFields: map[string]string{"host": "name", "group": "name"},
		requestSemaphore: make(chan struct{}, g.MaxRequests),
		global:           g,
		config:           z,
		excludeRequests:  excludeLog,
	}
}

var (
	prx proxy
)

// Инициализация Proxy
func InitProxy(g Global, cfg ZabbixConf, cbConf CBConf, cacheCfg CacheConf, excludeLog []string) {

	//Инициализвция нового прохи
	prx = NewProxy(g, cfg, excludeLog)

	// Подготовка имен серверов и инициализация клиента Zabbix
	zbxNames := make([]string, 0, len(cfg.Servers))
	for i := range cfg.Servers {
		// Безопасное извлечение имени из URL
		if urlParts := strings.Split(cfg.Servers[i].URL, "/"); len(urlParts) > 2 {
			cfg.Servers[i].Name = urlParts[2]
			zbxNames = append(zbxNames, urlParts[2])
		} else {
			zbxNames = append(zbxNames, cfg.Servers[i].URL)
		}
	}

	// Инициализация клиента Zabbix
	client, err := zabbix.Init(zabbix.Zabbix(cfg))
	if err != nil {
		logger.Global.Warningf("zabbix_client initiation error: %v", err)
	}
	prx.zbxClient = client

	prx.cb = circuitbreaker.NewCBManager()

	//Инициализируем circutibreakers
	prx.cb.InitCircuitBreakers(zbxNames, circuitbreaker.CircuitBreakerConf(cbConf))

	//Обрабаотываем лимит на размер тела входящего запроса
	prx.global.maxReqBodySizeInt64 = 15 * 1024 * 1024 // 15MB максимальный размер тела по умолчанию
	// Если параметр не пустой, заберем размер из конифга
	if prx.global.MaxReqBodySize != "" {
		if b, err := suffix.ToB(prx.global.MaxReqBodySize); err != nil || b == 0 {
			logger.Global.Errorf("convert error 'max_req_body_size' to bytes: %v", err)

		} else {
			prx.global.maxReqBodySizeInt64 = b
		}
	}

	//Обрабатываем лимит на таймаут входящего запроса
	prx.global.maxTimeoutInt64 = 31 // 31s по умолчанию
	if prx.global.MaxTimeout != "" {
		if s, err := suffix.ToSeconds(prx.global.MaxTimeout); err != nil || s == 0 {
			logger.Global.Errorf("convert error 'max_timeout' to seconds: %v", err)
		} else {
			prx.global.maxTimeoutInt64 = s
		}
	}

	//Инициализируем кеш
	cacheCfg.CachedFields = prx.cachedFields
	prx.cache = cache.Init(cache.CacheCfg(cacheCfg))

}

// Останавливаем кеш
func StopCacheDB() {
	// Останавливаем фоновые процессы кеша
	if prx.cache != nil {
		prx.cache.Stop()
	}
}

// Останавливаем proxy
func StopProxy() {
	StopCacheDB()
	prx.zbxClient.Close()
}

// Получаем массив серверов их конфига
func getAllServers() []int {
	servers := make([]int, 0, len(prx.config.Servers))
	for _, s := range prx.config.Servers {
		servers = append(servers, s.ID)
	}
	return servers
}

// Получаем список серверов, ID которых заначатся в запросах
func getTargetServers(request map[string]any) []int {
	serverMap := make(map[int]bool)
	if params, ok := request["params"].(map[string]any); ok {
		if extractServersFromParams(params, serverMap) {
			return getAllServers()
		}
	}

	servers := make([]int, 0, len(serverMap))
	for serverID := range serverMap {
		servers = append(servers, serverID)
	}
	return servers
}

// Функиция составления списка серверов для опроса из анализа ID переданных в запрос запроса.
// Возврашает true если это массовый запрос для всех серверов
func extractServersFromParams(params map[string]any, serverMap map[int]bool) bool {

	for key, val := range params {
		if !strings.HasSuffix(key, "ids") {
			continue
		}
		switch v := val.(type) {
		case []any:
			for _, id := range v {
				if serverID := getServerFromID(id); serverID > 0 {
					serverMap[serverID] = true
				} else if serverID == 0 {
					return true
				}
			}
		case any:
			if serverID := getServerFromID(v); serverID > 0 {
				serverMap[serverID] = true
			} else if serverID == 0 {
				return true
			}
		}
	}
	return false
}

// Главный процесс proxy
func processAllServers(ctx context.Context, request map[string]any, trace_id string) (any, []string) {
	var (
		wg                sync.WaitGroup
		mu                sync.Mutex
		results           []any
		resultsMap        = make(map[string]any)
		uniqProxyIDs      = make(map[string]map[any]bool)
		uniqMu            sync.RWMutex
		errors            []string
		cancelCtx, cancel = context.WithCancel(ctx)
	)
	defer cancel()

	isIDRequest, idFields := isIDBasedRequest(request)
	logger.Global.Tracef("[%s] IDbased request: %t. Fields: [%s]", trace_id, isIDRequest, idFields)

	var targetServers []int
	if isIDRequest {
		targetServers = getTargetServers(request)
		if len(targetServers) == 0 {
			logger.Global.Warningf("[%s] No target servers for ID-based request", trace_id)
			return nil, []string{"no target servers for ID-based request"}
		}
		logger.Global.Debugf("[%s] ID-Based. Target servers for %s: %v", trace_id, idFields, targetServers)
	} else {
		targetServers = getAllServers()
		logger.Global.Debugf("[%s] Not ID-Based. Target servers for %s: all servers", trace_id, idFields)
	}

	// Канал для результатов
	resultCh := make(chan serverResult, len(targetServers))
	errCh := make(chan serverError, len(targetServers))

	// Ограничиваем количество одновременных запросов
	for _, server := range prx.config.Servers {
		if !slices.Contains(targetServers, server.ID) {
			continue
		}

		//Ожидаем освобождение ресурса для запуска горутины
		select {
		case prx.requestSemaphore <- struct{}{}:
			// Проверяем Circuit Breaker
			if ok, _ := prx.cb.AllowRequest(server.Name); !ok {
				<-prx.requestSemaphore // Освободить слот

				logger.Global.Warningf("[%s] Circuit breaker status 'open' for server %s, skipping", trace_id, server.URL)
				errCh <- serverError{url: server.URL, err: fmt.Sprintf("server %d: circuit breaker open", server.ID)}
				continue
			}

			// Получили слот, запускаем обработку
			wg.Add(1)

		case <-cancelCtx.Done():
			// Отмечаем неудачу в Circuit Breaker
			prx.cb.ReportFailure(server.Name)
			// Контекст отменен, выходим
			continue
		}

		// Запускае горутину для запроса ZBX серверу
		go func(srv zabbix.ZabbixServer) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Global.Errorf("panic in goroutine: %v", r)
				}
			}()

			defer func() { <-prx.requestSemaphore }()

			// Выполняем глубокое клонирование запроса
			serverRequest := deepClone(request).(map[string]any)

			// Возвращаем ресурс
			defer returnToPool(serverRequest)

			// Подставляем токен сервера в завпрос
			serverRequest["auth"] = srv.Token
			//Подготовка запроса
			if isIDRequest {

				for _, idField := range idFields {
					//switch v := serverParams[idField].(type) {
					switch v := (serverRequest["params"]).(map[string]any)[idField].(type) {
					case []any:
						var filtered []any
						for _, id := range v {
							if sid := getServerFromID(id); sid == srv.ID {
								if originalID := convertGrafanaIDToOriginal(id, srv.ID); originalID != nil {
									filtered = append(filtered, originalID)
								}
							} else if sid == 0 {
								logger.Global.Tracef("[%s] Server[%d]: ID[%v] is ProxyID", trace_id, srv.ID, id)
								if originalID := convertProxyIDToOriginal(id, srv.ID, idField); originalID != nil {
									filtered = append(filtered, originalID)
								}
							}
						}
						if len(filtered) == 0 {
							logger.Global.Debugf("[%s] No matching IDs for server %d", trace_id, srv.ID)
							return
						}
						(serverRequest["params"]).(map[string]any)[idField] = filtered
					case any:
						if sid := getServerFromID(v); sid == srv.ID {
							if originalID := convertGrafanaIDToOriginal(v, srv.ID); originalID != nil {
								(serverRequest["params"]).(map[string]any)[idField] = originalID
							}
						} else if sid == 0 {
							logger.Global.Tracef("[%s] Single ID[%v] is ProxyID", trace_id, v)
							if originalID := convertProxyIDToOriginal(v, srv.ID, idField); originalID != nil {
								(serverRequest["params"]).(map[string]any)[idField] = originalID
							}
						} else {
							logger.Global.Debugf("[%s] ID does not belong to server %d", trace_id, srv.ID)
							return
						}
					}
				}
			}

			if !slices.Contains(prx.excludeRequests, serverRequest["method"].(string)) {
				logger.Global.Debugf("[%s] Sending to server[%d]: %s", trace_id, srv.ID, srv.URL)
			}

			// Инкриментируем активную сессию на сервер в метрике
			if metricsCollector != nil {
				metricsCollector.IncIncomingRequests(srv.Name)
			}
			startTime := time.Now()

			// Делаем запрос к Zabbix Server
			response, err := prx.zbxClient.SendToZabbix(cancelCtx, srv.URL, srv.IgnoreSSL, serverRequest)
			if err != nil {
				// Отмечаем неудачу в Circuit Breaker
				prx.cb.ReportFailure(srv.Name)
				//Отмечаем неудачу в метрике
				if metricsCollector != nil {
					metricsCollector.IncRequestStatus(srv.URL, "error")
				}

				logger.Global.Errorf("[%s] Error requesting %s: %v", trace_id, srv.URL, err)
				errCh <- serverError{url: srv.URL, err: err.Error()}
				return
			}
			// Отмечаем успех в метрике
			if metricsCollector != nil {
				metricsCollector.IncRequestStatus(srv.URL, "success")
			}

			// Отмечаем успех в Circuit Breaker
			prx.cb.ReportSuccess(srv.Name)

			// Отмечаем успех в метрике
			if metricsCollector != nil {
				metricsCollector.ObserveRequestDuration(srv.URL, serverRequest["method"].(string), time.Since(startTime))
			}
			if !slices.Contains(prx.excludeRequests, serverRequest["method"].(string)) {
				logger.Global.Debugf("[%s] Response from server [%d] in %v", trace_id, srv.ID, time.Since(startTime))
			}

			if result, ok := response["result"]; ok {
				processedResult := processResponseIDs(result, srv.ID, uniqProxyIDs, &uniqMu, 0)
				resultCh <- serverResult{result: processedResult, serverID: srv.ID}
			}
		}(server)
	}

	// Ждем завершения всех горутин или отмены контекста
	go func() {
		wg.Wait()
		close(resultCh)
		close(errCh)
	}()

	// Собираем результаты
	for {
		select {
		case <-cancelCtx.Done():
			// Таймаут или отмена
			errors = append(errors, "request timeout")
			return nil, errors

		case result, ok := <-resultCh:
			if !ok {
				resultCh = nil
			} else {
				mu.Lock()
				switch r := result.result.(type) {
				case []any:
					results = append(results, r...)
				case map[string]any:
					for key, val := range r {
						resultsMap[key] = val
					}
				}
				mu.Unlock()
			}

		case err, ok := <-errCh:
			if !ok {
				errCh = nil
			} else {
				mu.Lock()
				errors = append(errors, err.url+": "+err.err)
				mu.Unlock()
			}
		}

		if resultCh == nil && errCh == nil {
			break
		}
	}

	if len(results) > 0 {
		return results, errors
	}
	return resultsMap, errors
}

// Вспомогательные структуры для каналов
type serverResult struct {
	result   any
	serverID int
}

type serverError struct {
	url string
	err string
}

// Мониторинг состояния
func GetConnectionStats() map[string]int {

	stats := make(map[string]int)
	stats["active_goroutines"] = runtime.NumGoroutine()
	stats["active_requests"] = len(prx.requestSemaphore)
	stats["http_clients"] = prx.zbxClient.GetClientsCount()

	return stats
}

// Состояние кеша Proxy
func GetCacheStats() (map[string]int, bool) {
	if prx.cache != nil {
		return prx.cache.GetStats(), true
	}
	return nil, false
}

// Cостояние Circuit Breaker
func GetCBStats() map[string]any {
	return prx.cb.GetCircuitBreakerStats()
}

// Подготовка читаемого JSON для вывода в лог
func prettyJSON(data any) string {
	// Создаем временную функцию для маскировки auth-токена
	maskAuth := func(auth string) string {
		if len(auth) <= 10 {
			return strings.Repeat("*", len(auth)) // Короткий токен - полностью маскируем
		}
		// Оставляем первые 3 и последние 3 символа, остальное заменяем *
		return auth[:3] + strings.Repeat("*", len(auth)-6) + auth[len(auth)-3:]
	}

	// Конвертируем данные в map, если это возможно
	if m, ok := data.(map[string]any); ok {
		// Создаем копию map, чтобы не изменять оригинал
		result := make(map[string]any)
		for k, v := range m {
			if k == "auth" {
				if authStr, ok := v.(string); ok {
					result[k] = maskAuth(authStr) // Маскируем токен
				} else {
					result[k] = v // Если не строка, оставляем как есть
				}
			} else {
				result[k] = v // Все остальные поля оставляем без изменений
			}
		}
		data = result // Используем модифицированные данные
	}

	// Форматируем JSON с отступами
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		logger.Global.Errorf("Error formatting JSON: %v", err)
		return ""
	}
	return string(jsonData)
}
