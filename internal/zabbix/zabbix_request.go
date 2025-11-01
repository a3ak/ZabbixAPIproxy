package zabbix

import (
	"ZabbixAPIproxy/internal/logger"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/a3ak/suffix"
)

type ZabbixServer struct {
	URL       string `yaml:"url"`
	ID        int    `yaml:"id"`
	Token     string `yaml:"token"`
	IgnoreSSL bool   `yaml:"ignore_ssl"`
	Name      string `yaml:"name"`
}

type Zabbix struct {
	Limits struct {
		//MaxRequests      int    `yaml:"max_requests"`
		MaxRequestsByZBX int `yaml:"max_requests_by_zbx"`
		//MaxTimeout       string `yaml:"max_timeout"`
		MaxTimeoutByZBX string `yaml:"max_timeout_by_zbx"`
		//MaxRespBodySize    string `yaml:"max_resp_body_size"`
		//MaxReqBodySize     string `yaml:"max_req_body_size"`
		MaxRespBodySizeZbx string `yaml:"max_req_body_size_by_zbx"`
	} `yaml:"limits"`

	Servers    []ZabbixServer `yaml:"servers"`
	APIversion string         `yaml:"api.version"`
}

type zabbixClient struct {
	// Пул клиентов
	clients    map[bool]*http.Client
	clientsMux sync.RWMutex
	conf       Zabbix
}

func (c *zabbixClient) SendToZabbix(ctx context.Context, url string, ignoreSSL bool, request map[string]any) (map[string]any, error) {
	return c.sendToZabbix(ctx, url, ignoreSSL, request)
}

// Инициализирует клиент для полкоючения к Zabbix
func Init(cfg Zabbix) (*zabbixClient, error) {
	client := zabbixClient{clients: make(map[bool]*http.Client),
		conf: cfg}

	// Проверяем переменную для лимита тела ответа
	// Если пуста, задаем дефольное значение
	if client.conf.Limits.MaxRespBodySizeZbx == "" {
		client.conf.Limits.MaxRespBodySizeZbx = "20MB"
	} else {
		size, err := suffix.ToMB(client.conf.Limits.MaxRespBodySizeZbx)
		//Если значение не возможно конвертировать, сообщаем о ошибке и задаем дефолтное значение
		if err != nil {
			client.conf.Limits.MaxRespBodySizeZbx = "20MB" // 20MB Default Limit
			return &client, fmt.Errorf("parameter MaxRespBodySizeZbx = %s is not correct. Set default value %s", cfg.Limits.MaxRespBodySizeZbx, client.conf.Limits.MaxRespBodySizeZbx)
		} else if size == 0 {
			//Если размер нулевой, задаем дефотлное значение
			client.conf.Limits.MaxRespBodySizeZbx = "20MB" // 20MB Default Limit
		}
	}
	return &client, nil
}

// Добавляем graceful shutdown для HTTP клиентов
func (c *zabbixClient) Close() {
	c.clientsMux.Lock()
	defer c.clientsMux.Unlock()

	// Закрываем все HTTP клиенты
	for _, client := range c.clients {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}

	// Очищаем мапу клиентов
	c.clients = make(map[bool]*http.Client)
}

// Выделение транспорта для подключения к серверу
func (c *zabbixClient) getHTTPClient(ignoreSSL bool) *http.Client {
	//Блокируем изменение
	c.clientsMux.RLock()
	client, exists := c.clients[ignoreSSL]
	c.clientsMux.RUnlock()

	if exists {
		return client
	}

	//Проверка таймаута в конфиге
	var maxTimeoutToZbx int64 = 20
	if c.conf.Limits.MaxTimeoutByZBX != "" {
		if s, err := suffix.ToSeconds(c.conf.Limits.MaxTimeoutByZBX); err != nil || s == 0 {
			logger.Global.Errorf("convert error 'max_timeout_by_zbx' to seconds: %v", err)
		} else {
			maxTimeoutToZbx = s
		}
	}

	c.clientsMux.Lock()
	defer c.clientsMux.Unlock()
	//Повторная проверка после полного блокирования, что другой поток не создал уже клиента
	if client, exists = c.clients[ignoreSSL]; exists {
		return client
	}

	// Проверяем idleTimeout, если он меньше 10 секунд, то устанавливаем в 10 секунд
	idleConnTimeout := time.Duration(maxTimeoutToZbx) / 4 * time.Second
	if idleConnTimeout < 10*time.Second {
		idleConnTimeout = 10 * time.Second
	}

	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: ignoreSSL},
		MaxIdleConns:        c.conf.Limits.MaxRequestsByZBX / 2, //Обший пул
		MaxIdleConnsPerHost: c.conf.Limits.MaxRequestsByZBX / 4, //пул на хост
		MaxConnsPerHost:     c.conf.Limits.MaxRequestsByZBX,     // Лимит одновременных запросов к одному хосту
		IdleConnTimeout:     idleConnTimeout,                    //время жизни idle соединений
		//ResponseHeaderTimeout: 10 * time.Second, //Время ждать заголовки ответов сервера
		//ExpectContinueTimeout: 1 * time.Second, //Время ждать первых заголовков ответа сервера
		//TLSHandshakeTimeout:   10 * time.Second,
	}

	client = &http.Client{
		Transport: transport,
		Timeout:   time.Duration(maxTimeoutToZbx) * time.Second,
	}

	c.clients[ignoreSSL] = client
	return client
}

// Делаем запрос к ZabbixServer
func (c *zabbixClient) sendToZabbix(ctx context.Context, url string, ignoreSSL bool, request map[string]any) (map[string]any, error) {
	client := c.getHTTPClient(ignoreSSL)

	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Проверяем код и читаем тело ошибки
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("HTTP %d: %s, body: %s", resp.StatusCode, resp.Status, string(body))
	}

	// Ограничиваем чтение тела для защиты от больших ответов
	body, err := io.ReadAll(io.LimitReader(resp.Body, suffix.UnsafeToB(c.conf.Limits.MaxRespBodySizeZbx)))
	if err != nil {
		return nil, err
	}

	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		preview := string(body)
		if len(preview) > 100 {
			preview = string(body[:100]) + "..."
		}
		logger.Global.Warningf("Invalid JSON response from %s: %s", url, preview)
		return nil, fmt.Errorf("invalid JSON response: %v", err)
	}

	if _, ok := response["error"]; ok {
		return nil, fmt.Errorf("%v", response["error"])
	}

	return response, nil
}

// Мониторинг состояния
func (c *zabbixClient) GetClientsCount() int {
	c.clientsMux.RLock()
	defer c.clientsMux.RUnlock()
	return len(c.clients)
}
