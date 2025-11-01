package zabbix

import (
	"context"
)

// ZabbixClientInterface определяет интерфейс для клиента Zabbix.
// Выделено для возможности создания Mock для тестов
type ZabbixClient interface {
	SendToZabbix(ctx context.Context, url string, ignoreSSL bool, request map[string]any) (map[string]any, error)
	Close()
	GetClientsCount() int
}

// Убедитесь, что ZabbixClient реализует интерфейс
var _ ZabbixClient = (*zabbixClient)(nil)
