package metrics

import (
	"ZabbixAPIproxy/internal/logger"
	"ZabbixAPIproxy/internal/proxy"
	"context"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// Метрики runtime
	m runtime.MemStats

	// Регистрируем метрики
	goroutinesCount = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zap_goroutines",
		Help: "Current number of goroutines",
	})

	memoryAlloc = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zap_mem_alloc_bytes",
		Help: "Bytes allocated and still in use",
	})

	memoryTotalAlloc = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zap_memory_total_alloc_bytes",
		Help: "Total bytes allocated",
	})

	memorySys = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "zap_memory_sys_bytes",
		Help: "Total bytes obtained from system",
	})

	gcCount = promauto.NewCounter(prometheus.CounterOpts{
		Name: "zap_gc_cycles_total",
		Help: "Total garbage collection cycles",
	})

	//Делаю Counter, т.к. в моменте тяжело поймать активные сессии. Выгоднее считать изменения во времени, например через rate
	incomingRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "zap_incoming_requests",
		Help: "Current incoming requests to Zabbix servers",
	}, []string{"server"})

	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "zap_request_duration_sec",
		Help:    "Request duration to Zabbix servers",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30},
	}, []string{"server", "method"})

	requestStatus = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "zap_request",
		Help: "Total request by status to Zabbix servers",
	}, []string{"server", "type"})

	cacheSize = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zap_cache_items",
		Help: "Number of items in cache by type",
	}, []string{"cache", "type"})

	httpConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zap_http_conns",
		Help: "HTTP connections statistics",
	}, []string{"type"})

	// Дополнительные метрики
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "zap_requests_total",
		Help: "Total requests processed",
	}, []string{"method", "status"})

	responseSize = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "zap_response_size_bytes",
		Help:    "Response size distribution",
		Buckets: prometheus.ExponentialBuckets(1000, 10, 5), // 1KB to ~10MB
	})

	circuitBreakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zap_cb_state",
		Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
	}, []string{"server"})

	circuitBreakerFailures = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zap_cb_failures",
		Help: "Current number of failures in circuit breaker",
	}, []string{"server"})

	circuitBreakerSuccesses = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zap_cb_successes",
		Help: "Current number of successes in circuit breaker",
	}, []string{"server"})

	circuitBreakerLastFailure = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "zap_cb_last_failure_sec",
		Help: "Seconds since last circuit breaker failure",
	}, []string{"server"})

	circuitBreakerTransitions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "zap_cb_transitions_total",
		Help: "Total circuit breaker state transitions",
	}, []string{"server"})
)

// Exporter структура для управления метриками
type Exporter struct {
	registry   *prometheus.Registry
	cancelFunc context.CancelFunc // Для остановки всех фоновых процессов
	mu         sync.Mutex
}

// NewExporter создает новый экспортер
func NewExporter() *Exporter {
	registry := prometheus.NewRegistry()

	// Регистрируем стандартные метрики Go
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	// Регистрируем наши кастомные метрики
	registry.MustRegister(goroutinesCount)
	registry.MustRegister(memoryAlloc)
	registry.MustRegister(memoryTotalAlloc)
	registry.MustRegister(memorySys)
	registry.MustRegister(gcCount)
	registry.MustRegister(incomingRequests)
	registry.MustRegister(requestDuration)
	registry.MustRegister(requestStatus)
	registry.MustRegister(cacheSize)
	registry.MustRegister(httpConnections)
	registry.MustRegister(requestsTotal)
	registry.MustRegister(responseSize)
	registry.MustRegister(circuitBreakerState)
	registry.MustRegister(circuitBreakerFailures)
	registry.MustRegister(circuitBreakerSuccesses)
	registry.MustRegister(circuitBreakerLastFailure)
	registry.MustRegister(circuitBreakerTransitions)

	return &Exporter{
		registry: registry,
	}
}

// Start запускает сбор метрик
func (e *Exporter) Start(updateInterval time.Duration) {
	if e.cancelFunc != nil {
		// Уже запущен
		logger.Global.Debug("Exporter already started")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.cancelFunc = cancel
	go e.collectMetrics(ctx, updateInterval)
}

// Stop останавливает сбор метрик
func (e *Exporter) Stop() {
	if e.cancelFunc != nil {
		e.cancelFunc()
		e.cancelFunc = nil
		logger.Global.Debug("Exporter stopped")
	}
}

// Handler возвращает HTTP handler для метрик
func (e *Exporter) Handler() http.Handler {
	return promhttp.HandlerFor(e.registry, promhttp.HandlerOpts{
		Registry:          e.registry,
		EnableOpenMetrics: true,
		Timeout:           10 * time.Second,
	})
}

// collectMetrics периодически собирает метрики
func (e *Exporter) collectMetrics(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	time.Sleep(5 * time.Second)
	e.updateMetrics()
	for {
		select {
		case <-ticker.C:
			e.updateMetrics()
		case <-ctx.Done():
			logger.Global.Info("Metrics collection stopped")
			return
		}
	}
}

// updateMetrics обновляет все метрики
func (e *Exporter) updateMetrics() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Метрики runtime
	goroutinesCount.Set(float64(runtime.NumGoroutine()))

	runtime.ReadMemStats(&m)
	memoryAlloc.Set(float64(m.Alloc))
	memoryTotalAlloc.Set(float64(m.TotalAlloc))
	memorySys.Set(float64(m.Sys))
	gcCount.Add(float64(m.NumGC))

	// Метрики HTTP клиентов
	stats := proxy.GetConnectionStats()
	for connType, count := range stats {
		httpConnections.WithLabelValues(connType).Set(float64(count))
	}

	// Метрики кеша
	if cacheStats, ok := proxy.GetCacheStats(); ok {
		for cacheType, count := range cacheStats {
			// Разделяем тип кеша на две части: тип и подтип
			parts := strings.SplitN(cacheType, "_", 2)
			if len(parts) == 2 {
				cacheSize.WithLabelValues(parts[0], parts[1]).Set(float64(count))
			}
		}
	}
	// Метрики Circuit Breaker
	e.updateCircuitBreakerMetrics()

}

func (e *Exporter) updateCircuitBreakerMetrics() {
	stats := proxy.GetCBStats()
	if stats == nil {
		return
	}

	for serverURL, data := range stats {
		if statsMap, ok := data.(map[string]any); ok {
			// Обновляем состояние Circuit Breaker
			if stateVal, ok := statsMap["state"]; ok {
				var stateValue float64

				switch state := stateVal.(type) {
				//case circuitbreaker.State:
				//	stateValue = float64(state)
				case string:
					// Конвертируем строковое представление в число
					switch state {
					case "closed":
						stateValue = 0
					case "open":
						stateValue = 1
					case "half-open":
						stateValue = 2
					case "not configured":
						stateValue = 3
					case "disabled":
						stateValue = 4
					default:
						stateValue = -1
					}
				//case int:
				//	stateValue = float64(state)
				//case float64:
				//	stateValue = state

				default:
					logger.Global.Warningf("Unknown circuit breaker state type: %T", stateVal)
					continue
				}

				circuitBreakerState.WithLabelValues(serverURL).Set(stateValue)
			} else {
				circuitBreakerState.WithLabelValues(serverURL).Set(0)
			}

			// Обновляем счетчик ошибок (как gauge, а не counter)
			if failures, ok := statsMap["failure_count"].(int); ok {
				circuitBreakerFailures.WithLabelValues(serverURL).Set(float64(failures))
			} else {
				circuitBreakerFailures.WithLabelValues(serverURL).Set(0)
			}

			// Обновляем счетчик успехов
			if successes, ok := statsMap["success_count"].(int); ok {
				circuitBreakerSuccesses.WithLabelValues(serverURL).Set(float64(successes))
			} else {
				circuitBreakerSuccesses.WithLabelValues(serverURL).Set(0)
			}

			// Обновляем время последней ошибки
			if lastFailureTime, ok := statsMap["last_failure_time"].(time.Time); ok {
				if !lastFailureTime.IsZero() {
					secondsSinceFailure := time.Since(lastFailureTime).Seconds()
					circuitBreakerLastFailure.WithLabelValues(serverURL).Set(secondsSinceFailure)
				}
			}

			// Обновляем количество переходов между Close -> Open и обратно
			if transaction, ok := statsMap["transaction"].(int); ok {
				circuitBreakerTransitions.WithLabelValues(serverURL).Add(float64(transaction))
			} else {
				circuitBreakerTransitions.WithLabelValues(serverURL).Add(0)
			}
		}
	}
}

// IncRequestsTotal увеличивает счетчик запросов
func (e *Exporter) IncRequestsTotal(method, status string) {
	requestsTotal.WithLabelValues(method, status).Inc()
}

// ObserveRequestDuration записывает длительность запроса
func (e *Exporter) ObserveRequestDuration(server, method string, duration time.Duration) {
	requestDuration.WithLabelValues(simpleURLName(server), method).Observe(duration.Seconds())
}

// IncRequestErrors увеличивает счетчик ошибок
func (e *Exporter) IncRequestStatus(server, rtype string) {
	requestStatus.WithLabelValues(simpleURLName(server), rtype).Inc()
}

// ObserveResponseSize записывает размер ответа
func (e *Exporter) ObserveResponseSize(size int) {
	responseSize.Observe(float64(size))
}

// IncIncomingRequests инкримент активных запросов
func (e *Exporter) IncIncomingRequests(s string) {
	incomingRequests.WithLabelValues(s).Inc()
}

func simpleURLName(server string) string {
	s := strings.Split(server, "/")
	switch len(s) {
	case 0:
		return ""
	case 1, 2:
		return s[0]
	default:
		return s[2]
	}
}
