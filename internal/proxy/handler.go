package proxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"ZabbixAPIproxy/internal/logger"

	"github.com/google/uuid"
)

// Тип для ключей контекста, что бы избежать коллизий, если кто-то еще будет использовать контекст в обработчиках
// Рекомендация из https://golang.org/pkg/context/#WithValue
type ctxKey string
type ctxBody string

const (
	// для хранения тела запроса
	bodyKey ctxBody = "requestBody"
	// для хранения trace_id
	traceIDKey ctxKey = "trace_id"
)

// AuthMiddleware теперь возвращает http.Handler вместо http.HandlerFunc
func AuthMiddleware(next http.HandlerFunc, metricPath, login, password, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		//Инкриментируем метрику активных запросов к APIProxy
		if metricsCollector != nil {
			metricsCollector.IncIncomingRequests("APIproxy")
		}

		if r.URL.Path == "/favicon.ico" {
			faviconHandler(w)
			return
		}

		// Пропускаем запросы к /metrics и /health без аутентификации
		if r.URL.Path == metricPath || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Создаем trace_id на самом верхнем уровне
		trace_id := uuid.New().String()
		ctx := context.WithValue(r.Context(), traceIDKey, trace_id)
		r = r.WithContext(ctx)

		logger.Global.Debugf("[%s] Incoming request: %s %s", trace_id, r.Method, r.URL.Path)

		// Проверяем метод
		if r.Method == "GET" && r.URL.Path == "/" {
			logger.Global.Debugf("[%s] Handling root request", trace_id)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"result":  "Zabbix API Proxy",
				"id":      1,
			})
			return
		}

		if r.Method == "GET" || r.Method == "PUT" || r.Method == "DELETE" {
			logger.Global.Warningf("[%s] Unsupported GET, PUT, DELETE request", trace_id)
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		// Проверяем Content-Type
		contentType := r.Header.Get("Content-Type")
		if !strings.Contains(contentType, "application/json") {
			logger.Global.Errorf("[%s] Invalid Content-Type: %s", trace_id, contentType)
			http.Error(w, "Unsupported Media Type", http.StatusUnsupportedMediaType)
			return
		}

		// Проверяем размер тела
		if r.ContentLength > prx.global.maxReqBodySizeInt64 {
			logger.Global.Errorf("[%s] Request body too large: %d bytes", trace_id, r.ContentLength)
			http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
			return
		}

		// Читаем тело с ограничением размера
		body, err := io.ReadAll(io.LimitReader(r.Body, prx.global.maxReqBodySizeInt64))
		if err != nil {
			logger.Global.Errorf("[%s] Error reading body: %v", trace_id, err)
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// Восстанавливаем тело для последующих обработчиков
		r.Body = io.NopCloser(bytes.NewBuffer(body))

		// Сохраняем тело в контекст для использования в handler
		ctx = context.WithValue(r.Context(), bodyKey, body)
		r = r.WithContext(ctx)

		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			logger.Global.Errorf("[%s] Error parsing JSON: %v", trace_id, err)
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Валидация базовой структуры JSON-RPC
		if request["jsonrpc"] != "2.0" {
			logger.Global.Errorf("[%s] Invalid JSON-RPC version", trace_id)
			http.Error(w, "Invalid JSON-RPC request", http.StatusBadRequest)
			return
		}

		// Обработка специальных методов
		if method, ok := request["method"].(string); ok {
			switch {
			case strings.HasSuffix(method, ".create"):
				logger.Global.Debugf("[%s] Blocking create method: %s", trace_id, method)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"error": map[string]any{
						"code":    -1,
						"message": "Invalid method.",
						"data":    "Create methods are not implemented in proxy.",
					},
					"id": request["id"],
				})
				return

			case method == "user.login":
				logger.Global.Debugf("[%s] Handling login", trace_id)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"result":  "faketoken123",
					"id":      request["id"],
				})
				return

			case method == "apiinfo.version":
				logger.Global.Debugf("[%s] Handling version request", trace_id)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"result":  prx.config.APIversion,
					"id":      request["id"],
				})
				return
			}
		}

		// Аутентификация
		if token != "" {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "Bearer "+token {
				logger.Global.Errorf("[%s] Invalid token from %s", trace_id, r.RemoteAddr)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		} else if login != "" && password != "" {
			getLogin, getPass, ok := r.BasicAuth()
			if !ok || getLogin != login || getPass != password {
				logger.Global.Errorf("[%s] Invalid credentials from %s", trace_id, r.RemoteAddr)
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		// Передаем управление следующему handler
		next.ServeHTTP(w, r)
	}
}

// Иконка для браузера, что бы убарть из лога паразитный трафки с ошибками
func faviconHandler(w http.ResponseWriter) {
	// Base64 encoded 16x16 PNG favicon
	favicon := "iVBORw0KGgoAAAANSUhEUgAAACAAAAAgCAYAAABzenr0AAAEa0lEQVR4AbxXS0xUVxj+7u0IM7xmhOHhC4gJuoAFoUq0CzUsrJG4aPBFG6NpN8aFC23irgtWmtSN67bGReMrisa4IPERFlat0GgsG7uQBFqE8hihZZgHc/t9Z+6MjAxCEZj83z3n/P/5v/+b87iZsbHAj3PggJ/4kviB6CKGibgL9eVTTHP8C6TFvAKcQ4c2OQcP/kjCAeJn4hviU6KE+MSF+vIppjkDylEu4x+0OQU4+/blkeR7OM7vxNdk8RELNZ/JYa44xDVXYlYBTktLDbzeX0lymlg1V/K8fsdZxfzT4jKcWRJmCeCyNcC2f+HcWmKprFachvs9xgwBRmUi0cE5QWKpLQhymxozmNMCzD7Zdjtjy1GctMaCXIl2U8sMMeMW+Hxt9C3lspMuq9UiWcsEzQpwbzZxdJJYKTvp1nRXwHHO8LQu/rT/X9nJ23FGaTbfZH4Wb9VgReE4raqtLWieaGjwjezdixSiZWWIrF+fHss/nZ9v9CVyczG+dStG9+xBZN06TFVWmv7Eli1I5OSYOdGKioxc8U3W1GB09278U1+fmqcXW7MENE00NuLtjh0GPKWwYzG8OXbMjFP+hNcLx+PBnydOYLilBaFduxAtLYWKqf/3/v346/hxOLaNWElJRm68uBiRqiqEmpowdPgw+k+dQow+qm2SgHorHmcfKOzqwup79/Dm6FFMFxQYX+phTU9jqroasfLylGtWG127FpENG2b533fEAwGMcTXor5eA6ty+PuS/fIngrVsYam0134rBtHlfv4YnFEJqG9KBLJ2FzFGatphttQQE9K3LrlzBSHMzJjdvpv+dqbBi7zzz9CxrngnJsLvqAQmAlnd82zaMb9+ejLpPe3ISFRcvmrjrWrKm4MULwyUBofDGjebbG4/7sKJRVFy6hHhREfp4aHQA3dBHNXYkgsDDh/B3doonJAG9oZ07odMvj0EigfLLl8H3AwaPHEEiLw9xv9+EFvvwP3qEyrNnUdXWhuKODlisQa5eCXie8OlKcihzHJTevAnP6Ki5io57txX6GNjhsDnI2u4ZPM8l4EF+Tw8Knz41CLJ4YXc3/q2rMzdD/qInT+AZH8eqkREzRz5B45yhoQyfhAtFjx8jBc/Y2IyaGd0HEnA30NkZLm1vh1D07JlZ+tX375uxfLqeFl9Ouf39aZ/8Xl5fb29vhi93YABC8PZtpJAzOJhR1R2E2d61revX38KyuOEcrqSxpmprBUAB54gYVupjWTHWO6dyRoB19eorDi4QK2UX3Jru7wGVDYe/Y9NDLLf1IFnL1DEroJ51584kfzR+wf4wsVw2rBqmllshLUBj68aNP/hC+pz95RAxLG5TgwVSliFATu7Nb1T5GftLuR094jTcJJ5pswQoaFROTTXypJ4nYvItCsnTfh7kMpxZSLIK0Dztk3Xt2rcUUEf8RJ9eHGwWZGGTY1l14hDXXFlzCkglcNlekUT/etfQ9xWhf8rdbEeIaRfqy6eY5qxRjnIZ/6D9BwAA//8TDJApAAAABklEQVQDAAnO3K5MuJtSAAAAAElFTkSuQmCC"
	data, _ := base64.StdEncoding.DecodeString(favicon)
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400") // Кеширование на 1 день
	w.Write(data)
}

// Handler теперь получает тело из контекста
func Handler(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	trace_id, ok := r.Context().Value(traceIDKey).(string)
	if !ok {
		trace_id = uuid.New().String()
	}

	defer r.Body.Close()

	logger.Global.Debugf("[%s] Incoming HTTP request: %s %s", trace_id, r.Method, r.URL.Path)

	// Проверяем загружены ли серверы
	if len(prx.config.Servers) == 0 {
		logger.Global.Errorf("[%s] No servers configured in Zbx.Servers", trace_id)
		http.Error(w, "No servers configured", http.StatusInternalServerError)
		return
	}

	// Получаем тело из контекста вместо повторного чтения
	body, ok := r.Context().Value(bodyKey).([]byte)
	if !ok || body == nil {
		logger.Global.Errorf("[%s] Body not found in context", trace_id)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		logger.Global.Errorf("[%s] Error parsing JSON: %v", trace_id, err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	method, ok := request["method"].(string)
	if !ok {
		logger.Global.Errorf("[%s] Method not specified", trace_id)
		http.Error(w, "Method required", http.StatusBadRequest)
		return
	}

	if !slices.Contains(prx.excludeRequests, method) {
		logger.Global.Debugf("[%s] Request: %s", trace_id, prettyJSON(request))
	}

	logger.Global.Infof("[%s] Processing: %s", trace_id, method)

	// КРИТИЧЕСКИ ВАЖНО: Добавляем контекст с таймаутом
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(prx.global.maxTimeoutInt64)*time.Second)
	defer cancel()

	results, errors := processAllServers(ctx, request, trace_id)

	if isEmpty(results) && len(errors) > 0 {
		logger.Global.Errorf("[%s] All requests failed", trace_id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"error":   errors,
			"id":      request["id"],
		})
		return
	}

	if isEmpty(results) {
		results = []any{}
	}

	response := map[string]any{
		"jsonrpc": "2.0",
		"result":  results,
		"id":      request["id"],
	}

	responseBytes, err := json.Marshal(response)
	if err != nil {
		logger.Global.Errorf("[%s] Error marshaling response: %v", trace_id, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(responseBytes); err != nil {
		logger.Global.Errorf("[%s] Error writing response: %v", trace_id, err)
	}

	if !slices.Contains(prx.excludeRequests, method) {
		logger.Global.Debugf("[%s] Response: %s", trace_id, prettyJSON(response))
	}

	// Увеличиваем счетчик запросов
	defer func() {
		status := "success"
		if len(errors) == len(prx.config.Servers) {
			status = "error"
		} else if len(errors) < len(prx.config.Servers) {
			status = "halfError"
		}
		if metricsCollector != nil {
			metricsCollector.IncRequestsTotal(method, status)
			metricsCollector.IncRequestsTotal("all", status)
			metricsCollector.ObserveResponseSize(len(responseBytes))
			metricsCollector.ObserveRequestDuration("APIproxy", method, time.Since(startTime))
		}
		logger.Global.Infof("[%s] Completed by status '%s' in %v", trace_id, status, time.Since(startTime))
	}()
}
