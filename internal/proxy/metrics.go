package proxy

import (
	"time"
)

// Добавляем интерфейс для метрик в структуру Handler
type MetricsCollector interface {
	IncRequestsTotal(method, status string)
	ObserveResponseSize(size int)
	ObserveRequestDuration(server, method string, duration time.Duration)
	IncRequestStatus(server, rtype string)
	IncIncomingRequests(server string)
}

// Глобальная переменная для метрик
var metricsCollector MetricsCollector

// InitMetrics инициализирует сборщик метрик
func InitMetrics(collector MetricsCollector) {
	metricsCollector = collector
}
