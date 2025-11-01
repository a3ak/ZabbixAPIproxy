package main

import (
	"ZabbixAPIproxy/internal/logger"
	"ZabbixAPIproxy/internal/metrics"
	"ZabbixAPIproxy/internal/proxy"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/a3ak/suffix"
	"gopkg.in/yaml.v3"
)

type config struct {
	Global  proxy.Global    `yaml:"global"`
	Cache   proxy.CacheConf `yaml:"cache"`
	Logging logger.Logging  `yaml:"logging"`

	Zabbix         proxy.ZabbixConf `yaml:"zabbix"`
	CircuitBreaker proxy.CBConf     `yaml:"circuit_breaker"`
}

var (
	conf       config
	confPath   string
	httpServer *http.Server
	version    = "dev"
)

func init() {
	flag.StringVar(&confPath, "c", "config.yaml", "Path to Conf file")
	v := flag.Bool("v", false, "Print version and exit")
	flag.Parse()
	if *v {
		fmt.Println("Verison: ", version)
		os.Exit(0)
	}
}

// startMetricsServer запускает сервер для метрик
func startMetricsServer(mux *http.ServeMux, freq time.Duration) (stopMetricsServer func()) {
	// Инициализируем экспортер метрик
	exporter := metrics.NewExporter()
	exporter.Start(freq * time.Second) // Обновляем метрики каждые 30 секунд

	// Инициализируем метрики в proxy package
	proxy.InitMetrics(exporter)

	mux.Handle(conf.Global.MetricPath, exporter.Handler())
	mux.Handle("/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "OK",
			"version": version,
		})
	}))

	logger.Global.Infof("Metrics available at http://%s/%s", conf.Global.ListenAddr, conf.Global.MetricPath)
	logger.Global.Infof("Health check at http://%s/health", conf.Global.ListenAddr)
	return exporter.Stop
}

func main() {
	fmt.Println("Starting Zabbix API proxy version ", version)

	// Загружаем конфиг
	if err := loadConf(&conf, confPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Инициализируем логер
	logger.InitLogger(conf.Logging)

	// Создаем мультиплексор для обработки разных путей
	mux := http.NewServeMux()

	// Запускаем сбор Prometheus метрик
	if conf.Global.MetricPath != "" {
		stop_exporter := startMetricsServer(mux, 30)
		defer stop_exporter()
	}

	//Запуск мониторинга в логе
	if conf.Global.MonitoringInLog {
		startMonitoring()
	}

	//Инициализируем proxy.Zbx до вывода в лог
	proxy.InitProxy(conf.Global, conf.Zabbix, conf.CircuitBreaker, conf.Cache, conf.Logging.ExcludeRequests)

	logger.Global.Infof("Loaded %d servers from configuration", len(conf.Zabbix.Servers))

	// Канал для graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)

	// Основной эндпоинт API
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		proxy.AuthMiddleware(proxy.Handler, conf.Global.MetricPath, conf.Global.Login, conf.Global.Password, conf.Global.Token)(w, r)
	})

	// Настройка сервера HTTP сервера
	httpServer = &http.Server{
		Addr:         conf.Global.ListenAddr,
		Handler:      mux,
		ReadTimeout:  time.Duration(suffix.UnsafeToSeconds(conf.Global.ReadTimeout)) * time.Second,
		WriteTimeout: time.Duration(suffix.UnsafeToSeconds(conf.Global.WriteTimeout)) * time.Second,
		IdleTimeout:  time.Duration(suffix.UnsafeToSeconds(conf.Global.IdleTimeout)) * time.Second,
	}

	// Запуск сервера в отдельной горутине
	serverErr := make(chan error, 1)
	go func() {
		logger.Global.Infof("Starting proxy on %s", conf.Global.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// Обработка сигналов
	for {
		select {
		case sig := <-stop:
			switch sig {
			case syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT:
				logger.Global.Info("Received shutdown signal")
				gracefulShutdown()
				return

			case syscall.SIGHUP:
				logger.Global.Info("Received SIGHUP, reloading configuration")
				reloadConfiguration()
			}

		case err := <-serverErr:
			logger.Global.Errorf("HTTP server error: %v", err)
			gracefulShutdown()
			return
		}
	}
}

func gracefulShutdown() {
	fmt.Println("Stopping Zabbix API proxy gracefully...")
	// Останавливаем proxy
	proxy.StopProxy()

	// Graceful shutdown HTTP сервера
	if httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(ctx); err != nil {
			logger.Global.Errorf("HTTP server shutdown error: %v", err)
		}
	}

	logger.Global.Info("Server stopped gracefully")
}

func reloadConfiguration() {
	newConf := config{}
	if err := loadConf(&newConf, confPath); err != nil {
		logger.Global.Errorf("Failed to reload configuration: %v", err)
	}
	// Останавливаем кеш
	proxy.StopCacheDB()

	// Обновляем конфигурацию
	conf = newConf

	proxy.InitProxy(conf.Global, conf.Zabbix, conf.CircuitBreaker, conf.Cache, conf.Logging.ExcludeRequests)

	// Переинициализируем логер
	logger.InitLogger(conf.Logging)

	//Устанавливаем таймауты httpServer
	httpServer.ReadTimeout = time.Duration(suffix.UnsafeToSeconds(conf.Global.ReadTimeout)) * time.Second
	httpServer.WriteTimeout = time.Duration(suffix.UnsafeToSeconds(conf.Global.WriteTimeout)) * time.Second
	httpServer.IdleTimeout = time.Duration(suffix.UnsafeToSeconds(conf.Global.IdleTimeout)) * time.Second

	logger.Global.Info("Configuration reloaded successfully")
}

func startMonitoring() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Мониторинг горутин
				goroutineCount := runtime.NumGoroutine()
				logger.Global.Infof("Goroutines: %d", goroutineCount)

				// Мониторинг памяти
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				logger.Global.Infof("Memory: Alloc=%.1fMB, TotalAlloc=%.1fMB, Sys=%.1fMB, NumGC=%d",
					bToMb(m.Alloc), bToMb(m.TotalAlloc), bToMb(m.Sys), m.NumGC)

				// Мониторинг кеша
				if stats, ok := proxy.GetCacheStats(); ok {
					logger.Global.Infof("Cache stats: %+v", stats)
				}

				// Мониторинг HTTP клиентов
				clientStats := proxy.GetConnectionStats()
				logger.Global.Infof("HTTP clients stats: %+v", clientStats)

				// Предупреждение при большом количестве горутин
				if goroutineCount > 1000 {
					logger.Global.Warningf("HIGH GOROUTINE COUNT: %d - possible leak detected!", goroutineCount)
				}

			case <-ctx.Done():
				logger.Global.Info("Monitoring stopped")
				return
			}
		}
	}()

	return cancel
}

func bToMb(b uint64) float64 {
	return float64(b) / 1024 / 1024
}

func setDefaultsConfParams(conf *config) {
	if conf.Global.ListenAddr == "" {
		conf.Global.ListenAddr = ":8080"
	}
	if conf.Zabbix.APIversion == "" {
		conf.Zabbix.APIversion = "6.4"
	}
	if conf.Cache.TTL == "" {
		conf.Cache.TTL = "3d" //3d default
	}
	if conf.Cache.CleanupInterval == "" {
		conf.Cache.CleanupInterval = "12h" //12h default
	}
	if conf.Cache.DBPath == "" {
		conf.Cache.DBPath = "./Cache.db"
	}
	if conf.Logging.FilePath == "" {
		conf.Logging.FilePath = "./ZabbixAPIproxy.log"
	}
	if r, err := suffix.ToMB(conf.Logging.MaxSize); err != nil || r == 0 {
		logger.Global.Errorf("convert error 'max_size' to MB: %s", err)
		conf.Logging.MaxSize = "5MB"
	}
	if conf.Logging.MaxBackups == 0 {
		conf.Logging.MaxBackups = 3
	}
	if conf.Logging.FileLevel == "" {
		conf.Logging.FileLevel = "Warning"
	}

	if r, err := suffix.ToSeconds(conf.Global.ReadTimeout); err != nil || r == 0 {
		logger.Global.Errorf("convert error 'read_timeout' to seconds: %s", err)
		conf.Global.ReadTimeout = "10"
	}
	if r, err := suffix.ToSeconds(conf.Global.WriteTimeout); err != nil || r == 0 {
		logger.Global.Errorf("convert error 'write_timeout' to seconds: %s", err)
		conf.Global.WriteTimeout = "10"
	}
	if r, err := suffix.ToSeconds(conf.Global.IdleTimeout); err != nil || r == 0 {
		logger.Global.Errorf("convert error 'idle_timeout' to seconds: %s", err)
		conf.Global.IdleTimeout = "15"
	}
}

func loadConf(cfg *config, cfgPath string) error {
	file, err := os.Open(cfgPath)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(cfg); err != nil {
		return err
	}

	setDefaultsConfParams(cfg)
	return nil
}
