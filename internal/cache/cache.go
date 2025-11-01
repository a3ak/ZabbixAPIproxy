package cache

import (
	"ZabbixAPIproxy/internal/logger"
	"context"
	"encoding/json"
	"maps"
	"sync"
	"time"

	"github.com/a3ak/suffix"
	"go.etcd.io/bbolt"
)

const (
	bucketName = "ZabbixAPIproxy"
)

// Структура для конфигурации кеша
type CacheCfg struct {
	TTL             string            `yaml:"ttl"`
	CleanupInterval string            `yaml:"cleanup_interval"`
	DBPath          string            `yaml:"db_path"`
	AutoSave        string            `yaml:"auto_save"`
	CachedFields    map[string]string `yaml:"cached_fields"`
}

// cacheEntry структура для кеша
type CacheEntry struct {
	db         *bbolt.DB
	mu         sync.RWMutex
	CacheType  map[string]*cacheType `json:"cacheType"`
	cancelFunc context.CancelFunc    // Для остановки всех фоновых процессов
}

// cacheType подструктура кеша, для разделения кеша по типам
type cacheType struct {
	mu        sync.RWMutex
	ProxyID   map[int]cacheItem `json:"proxyID"`  //Возвращает OrignalID
	ReverseID map[int]reverseID `json:"ServerID"` //Возвращает ProxyID
}

// ReverseID кеш для получения ProxyID из OriginalID по ServerID
type reverseID struct {
	ProxyID map[int]int `json:"ProxyID"` //serverID: ProxyID
}

// CacheItem хранит данные и время создания для TTL. Возвращает OriginalID для
// определенного ServerID по ProxyID
type cacheItem struct {
	Name       string      `json:"name"`
	OriginalID map[int]int `json:"originalID"` // serverID: OriginalID
	CreatedAt  time.Time   `json:"createdAt"`
}

// cacheEntry структура для кеша в сериализуемом виде для сохранения в БД
type serializablecacheEntry struct {
	CacheType map[string]*serializablecacheType `json:"cacheType"`
}

// cacheType подструктура кеша в сериализуемом виде для сохранения в БД
type serializablecacheType struct {
	ProxyID   map[int]cacheItem `json:"proxyID"`
	ReverseID map[int]reverseID `json:"ServerID"`
}

func (ce *CacheEntry) toSerializable() *serializablecacheEntry {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	serializable := &serializablecacheEntry{
		CacheType: make(map[string]*serializablecacheType),
	}

	for k, v := range ce.CacheType {
		v.mu.RLock()
		serializable.CacheType[k] = &serializablecacheType{
			ProxyID:   v.ProxyID,
			ReverseID: v.ReverseID,
		}
		v.mu.RUnlock()
	}

	return serializable
}

// NewcacheEntry инициализация cacheEntry
func newCacheEntry() *CacheEntry {
	return &CacheEntry{
		CacheType: make(map[string]*cacheType),
	}
}

// NewCache инициализация cacheType
func newCache() *cacheType {
	return &cacheType{
		ProxyID:   make(map[int]cacheItem),
		ReverseID: make(map[int]reverseID),
	}
}

// Set добавляет или обновляет элемент в двунаправленном кэше
// Производит атомарное обновление двух взаимосвязанных мап:
//   - Прямое отображение: ProxyID -> CacheItem (содержит все OriginalID для этого ProxyID)
//   - Обратное отображение: OriginalID -> ReverseID (содержит все ProxyID для этого OriginalID)
//
// Параметры:
//   - proxyID: идентификатор в прокси-системе
//   - OriginalID: идентификатор в оригинальной системе (Zabbix)
//   - SrvID: идентификатор сервера (для мульти-серверных окружений)
//   - ItemName: человеко-читаемое имя элемента
func (c *cacheType) Set(proxyID int, OriginalID int, SrvID int, ItemName string) {
	// Проверка валидности входных параметров
	if proxyID <= 0 || OriginalID <= 0 || SrvID <= 0 {
		logger.Global.Warningf("Invalid parameters in Set: proxyID=%d, OriginalID=%d, SrvID=%d",
			proxyID, OriginalID, SrvID)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	createdAt := time.Now()

	// Обновление прямого кеша (ProxyID -> CacheItem)
	if existingItem, exists := c.ProxyID[proxyID]; exists {
		// Элемент уже существует - обновляем его
		if existingItem.OriginalID[SrvID] == OriginalID {
			// Значение не изменилось, только обновляем время для TTL
			existingItem.CreatedAt = createdAt
			c.ProxyID[proxyID] = existingItem
		} else {
			// Значение изменилось - создаем копию с обновленными данными
			updatedItem := cacheItem{
				Name:       ItemName,
				OriginalID: make(map[int]int, len(existingItem.OriginalID)+1),
				CreatedAt:  createdAt,
			}
			// Копируем существующие значения
			maps.Copy(updatedItem.OriginalID, existingItem.OriginalID)

			// Добавляем/обновляем значение для текущего сервера
			updatedItem.OriginalID[SrvID] = OriginalID
			c.ProxyID[proxyID] = updatedItem
		}
	} else {
		// Новый элемент - создаем с начальными данными
		c.ProxyID[proxyID] = cacheItem{
			Name:       ItemName,
			OriginalID: map[int]int{SrvID: OriginalID},
			CreatedAt:  createdAt,
		}
	}

	// Обновление обратного кеша (OriginalID -> ReverseID)
	if existingReverse, exists := c.ReverseID[OriginalID]; exists {

		// Обратная запись уже существует - обновляем ее
		if existingReverse.ProxyID[SrvID] == proxyID {
			// Значение не изменилось, ничего не делаем
			return
		}

		// Создаем обновленную обратную запись
		updatedReverse := reverseID{
			ProxyID: make(map[int]int, len(existingReverse.ProxyID)+1),
		}
		// Копируем существующие значения
		maps.Copy(updatedReverse.ProxyID, existingReverse.ProxyID)

		// Добавляем/обновляем значение для текущего сервера
		updatedReverse.ProxyID[SrvID] = proxyID
		c.ReverseID[OriginalID] = updatedReverse
	} else {
		// Новая обратная запись - создаем с начальными данными
		c.ReverseID[OriginalID] = reverseID{
			ProxyID: map[int]int{SrvID: proxyID},
		}
	}
}

// GetOriginalID возвращает OriginalID для заданных proxyID и ServerID
// Возвращает (originalID, true) если найдено, (0, false) если не найдено
func (c *cacheType) GetOriginalID(proxyID, ServerID int) (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, exists := c.ProxyID[proxyID]
	if !exists {
		return 0, false
	}

	originalID, exists := item.OriginalID[ServerID]
	return originalID, exists
}

// GetProxyID возвращает ProxyID для заданных OriginalID и ServerID
// Возвращает (proxyID, true) если найдено, (0, false) если не найдено
func (c *cacheType) GetProxyID(OriginalID, ServerID int) (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	reverseItem, exists := c.ReverseID[OriginalID]
	if !exists {
		return 0, false
	}

	proxyID, exists := reverseItem.ProxyID[ServerID]
	return proxyID, exists
}

// Delete удаляет элемент из кеша
func (c *cacheType) Delete(proxyIDs []int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, id := range proxyIDs {
		if item, exists := c.ProxyID[id]; exists {
			// Для каждого originalID удаляем только маппинг для данного proxy id (server)
			for _, originalID := range item.OriginalID {
				delete(c.ReverseID, originalID)
			}
			// Удаляем соответствующую запись в ProxyID
			delete(c.ProxyID, id)
		}
	}
}

// Cleanup удаляет устаревшие записи
func (c *cacheType) cleanup(ttl time.Duration) {
	c.mu.RLock()
	// Массив ключей для удаления, чтобы не блокировать мапы во время удаления
	var clenaup []int

	now := time.Now()
	for proxyID, item := range c.ProxyID {
		// Если запись старше TTL, добавляем в список на удаление
		if now.Sub(item.CreatedAt) > ttl {
			clenaup = append(clenaup, proxyID)
		}
	}
	c.mu.RUnlock() // Разблокируем для удаления

	// Удаляем соответствующие записи в ReverseID
	c.Delete(clenaup)

}

// Save сохраняет cacheEntry в BoltDB
func (ce *CacheEntry) save() error {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	serializableCe := ce.toSerializable()

	return ce.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		if err != nil {
			return err
		}
		data, err := json.Marshal(serializableCe)
		if err != nil {
			return err
		}
		return b.Put([]byte(bucketName), data)
	})
}

// StartAutoSave запускает периодическую запись кеша в БД с возможностью остановки
func (ce *CacheEntry) startAutoSave(saveInterval time.Duration, ctx context.Context) {
	//Если не установле интервал, то выходим
	if saveInterval == 0 {
		logger.Global.Infoln("Autosave interval is not set. The running of the process will not be done.")
		return
	}

	go func(ctx context.Context) {
		logger.Global.Info("AutoSave worker started")
		defer logger.Global.Info("AutoSave worker stopped")

		ticker := time.NewTicker(saveInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := ce.save(); err != nil {
					logger.Global.Warning("Cache save error: " + err.Error())
				} else {
					logger.Global.Debugf("Periodic save cache to database completed")
				}
			case <-ctx.Done():
				return
			}
		}
	}(ctx)
}

// StartCleanup запускает периодическую очистку кеша по TTL с возможностью остановки
func (ce *CacheEntry) startCleanup(cleanupInterval time.Duration, ttl time.Duration, ctx context.Context) {

	//Если не установле интервал, то выходим
	if cleanupInterval == 0 {
		logger.Global.Infoln("Cleanup interval is not set. The running of the process will not be done.")
		return
	}

	go func(ctx context.Context) {
		logger.Global.Info("Cleanup worker started")
		defer logger.Global.Info("Cleanup worker stopped")

		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				for _, cache := range ce.CacheType {
					cache.cleanup(ttl)
				}
				logger.Global.Debugf("Cache cleanup completed")
			case <-ctx.Done():
				return
			}
		}
	}(ctx)
}

// Start все фоновые процессы
func (ce *CacheEntry) start(cleanInterval, ttl, autoSave time.Duration) {
	ce.mu.Lock()
	if ce.cancelFunc != nil {
		// Фоновые процессы уже запущены
		logger.Global.Warningln("Background workers already running")
		ce.mu.Unlock()
		return
	}

	//Контекст для остановки фоновых процессов
	ctx, cancel := context.WithCancel(context.Background())
	ce.cancelFunc = cancel
	ce.mu.Unlock()

	// Запускаем CleanUP
	ce.startCleanup(cleanInterval, ttl, ctx)

	//Звпускаем AutoSave
	ce.startAutoSave(autoSave, ctx)

}

// Stop все фоновые процессы
func (ce *CacheEntry) Stop() {
	ce.mu.Lock()
	if ce.cancelFunc != nil {
		ce.cancelFunc()
		ce.cancelFunc = nil
		logger.Global.Info("All background processes stopped")
	}
	ce.mu.Unlock()

	if ce.db != nil {
		err := ce.save()
		if err != nil {
			logger.Global.Errorf("Final cache save failed: %v", err)
		}

		ce.db.Close()
	}
}

// Load загружает cacheEntry из BoltDB
func (ce *CacheEntry) load() error {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	return ce.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return nil
		}

		data := b.Get([]byte(bucketName))
		if data == nil {
			return nil
		}

		// Десериализуем во временную структуру
		var serializable serializablecacheEntry
		if err := json.Unmarshal(data, &serializable); err != nil {
			return err
		}

		// Вручную копируем данные
		for cacheTypeName, serializableCache := range serializable.CacheType {
			if _, exists := ce.CacheType[cacheTypeName]; !exists {
				ce.CacheType[cacheTypeName] = newCache()
			}

			cache := ce.CacheType[cacheTypeName]
			cache.mu.Lock()
			cache.ProxyID = serializableCache.ProxyID
			cache.ReverseID = serializableCache.ReverseID
			cache.mu.Unlock()
		}

		return nil
	})
}

// cacheEntryInit инициализирует cacheEntry с заданными типами кеша
func cacheEntryInit(cacheFileds map[string]string) *CacheEntry {
	cacheEntry := newCacheEntry()

	cacheEntry.mu.Lock()
	defer cacheEntry.mu.Unlock()

	for ftype := range cacheFileds {
		if _, ok := cacheEntry.CacheType[ftype]; !ok {
			cacheEntry.CacheType[ftype] = newCache()
		}
	}

	return cacheEntry
}

// Инициализация кеша
func Init(cfg CacheCfg) *CacheEntry {

	// Подключаем БД
	db, err := bbolt.Open(cfg.DBPath, 0600, nil)
	if err != nil {
		logger.Global.Fatal(err)
	}

	// Инициализируем кеш
	cache := cacheEntryInit(cfg.CachedFields)
	cache.db = db

	// Загружаем данные в кеш из БД
	if err := cache.load(); err != nil {
		logger.Global.Errorf("Failed to load cache: %v", err)
	}

	// Конвертируем интервалы времени
	cleanInterval, err := suffix.ToSeconds(cfg.CleanupInterval)
	if err != nil {
		logger.Global.Fatalf("Failed convert cleanup interval: %s", err)
	}

	ttlDuration, err := suffix.ToSeconds(cfg.TTL)
	if err != nil {
		logger.Global.Fatalf("Failed convert TTL: %s", err)
	}

	autoSave, err := suffix.ToSeconds(cfg.AutoSave)
	if err != nil {
		logger.Global.Fatalf("Failed convert auto_save: %s", err)
	}

	// Запускаем фоновые процессы кеша
	cache.start(time.Duration(cleanInterval)*time.Second, time.Duration(ttlDuration)*time.Second, time.Duration(autoSave)*time.Second)

	return cache
}

// GetStats возвращает статистику кеша
func (ce *CacheEntry) GetStats() map[string]int {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	stats := make(map[string]int)
	for cacheType, cache := range ce.CacheType {
		cache.mu.RLock()
		stats[cacheType+"_proxy_items"] = len(cache.ProxyID)
		stats[cacheType+"_reverse_items"] = len(cache.ReverseID)
		cache.mu.RUnlock()
	}
	return stats
}
