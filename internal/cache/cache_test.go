package cache

import (
	"os"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

func TestCacheType_SetAndGet(t *testing.T) {
	cache := newCache()

	// Test basic set and get
	cache.Set(100, 500, 1, "TestHost")
	cache.Set(100, 600, 2, "TestHost") // Same proxyID, different server

	// Test GetOriginalID
	if originalID, found := cache.GetOriginalID(100, 1); !found || originalID != 500 {
		t.Errorf("GetOriginalID failed: expected 500, got %d, found %v", originalID, found)
	}

	if originalID, found := cache.GetOriginalID(100, 2); !found || originalID != 600 {
		t.Errorf("GetOriginalID failed: expected 600, got %d, found %v", originalID, found)
	}

	// Test GetProxyID
	if proxyID, found := cache.GetProxyID(500, 1); !found || proxyID != 100 {
		t.Errorf("GetProxyID failed: expected 100, got %d, found %v", proxyID, found)
	}

	if proxyID, found := cache.GetProxyID(600, 2); !found || proxyID != 100 {
		t.Errorf("GetProxyID failed: expected 100, got %d, found %v", proxyID, found)
	}

	// Test non-existent values
	if _, found := cache.GetOriginalID(999, 1); found {
		t.Error("GetOriginalID should return false for non-existent key")
	}

	if _, found := cache.GetProxyID(999, 1); found {
		t.Error("GetProxyID should return false for non-existent key")
	}
}

func TestCacheType_UpdateExisting(t *testing.T) {
	cache := newCache()

	// Set initial value
	cache.Set(100, 500, 1, "HostA")

	// Get initial timestamp
	cache.mu.RLock()
	originalItem := cache.ProxyID[100]
	cache.mu.RUnlock()

	time.Sleep(10 * time.Millisecond) // Ensure different timestamp

	// Update with same values (should only update timestamp)
	cache.Set(100, 500, 1, "HostA")

	cache.mu.RLock()
	updatedItem := cache.ProxyID[100]
	cache.mu.RUnlock()

	if originalItem.CreatedAt.Equal(updatedItem.CreatedAt) {
		t.Error("Timestamp should be updated even for same values")
	}

	// Update with different values
	cache.Set(100, 600, 1, "HostB")
	if originalID, _ := cache.GetOriginalID(100, 1); originalID != 600 {
		t.Errorf("Update failed: expected 600, got %d", originalID)
	}
}

func TestCacheType_Delete(t *testing.T) {
	cache := newCache()

	// Setup test data
	cache.Set(100, 500, 1, "HostA")
	cache.Set(100, 600, 2, "HostA")
	cache.Set(200, 700, 1, "HostB")

	// Delete one proxyID
	cache.Delete([]int{100})

	// Verify deletion
	if _, found := cache.GetOriginalID(100, 1); found {
		t.Error("ProxyID 100 should be deleted")
	}

	if _, found := cache.GetProxyID(500, 1); found {
		t.Error("reverseID 500 should be deleted")
	}

	if _, found := cache.GetProxyID(600, 2); found {
		t.Error("reverseID 600 should be deleted")
	}

	// Verify other data remains
	if _, found := cache.GetOriginalID(200, 1); !found {
		t.Error("ProxyID 200 should still exist")
	}
}

func TestCacheType_Cleanup(t *testing.T) {
	cache := newCache()

	// Add items with different timestamps
	now := time.Now()

	// Old item (should be cleaned up)
	cache.mu.Lock()
	cache.ProxyID[100] = cacheItem{
		OriginalID: map[int]int{1: 500},
		CreatedAt:  now.Add(-2 * time.Hour),
	}
	cache.ReverseID[500] = reverseID{ProxyID: map[int]int{1: 100}}
	cache.mu.Unlock()

	// Recent item (should remain)
	cache.mu.Lock()
	cache.ProxyID[200] = cacheItem{
		OriginalID: map[int]int{1: 600},
		CreatedAt:  now.Add(-30 * time.Minute),
	}
	cache.ReverseID[600] = reverseID{ProxyID: map[int]int{1: 200}}
	cache.mu.Unlock()

	// Run cleanup with 1 hour TTL
	cache.cleanup(time.Hour)

	// Verify results
	cache.mu.RLock()
	_, foundOldProxy := cache.ProxyID[100]
	_, foundOldReverse := cache.ReverseID[500]
	_, foundRecentProxy := cache.ProxyID[200]
	_, foundRecentReverse := cache.ReverseID[600]
	cache.mu.RUnlock()

	if foundOldProxy {
		t.Error("Old item should be cleaned up")
	}

	if foundOldReverse {
		t.Error("Old reverse ID should be cleaned up")
	}

	if !foundRecentProxy {
		t.Error("Recent item should remain")
	}

	if !foundRecentReverse {
		t.Error("Recent reverse ID should remain")
	}
}

func TestCacheEntry_CRUD(t *testing.T) {
	cacheEntry := newCacheEntry()
	cacheEntry.CacheType["hosts"] = newCache()

	// Test Set and Get through CacheEntry
	cacheType := cacheEntry.CacheType["hosts"]
	cacheType.Set(100, 500, 1, "TestHost")

	if originalID, found := cacheType.GetOriginalID(100, 1); !found || originalID != 500 {
		t.Errorf("CacheEntry operation failed: expected 500, got %d", originalID)
	}
}

func TestCacheEntry_SaveAndLoad(t *testing.T) {
	// Create temporary database
	tmpFile, err := os.CreateTemp("", "testdb.*.bolt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	db, err := bbolt.Open(tmpFile.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Create test data
	cacheEntry := newCacheEntry()
	cacheEntry.db = db
	cacheEntry.CacheType["hosts"] = newCache()
	cacheEntry.CacheType["hosts"].Set(100, 500, 1, "TestHost")
	cacheEntry.CacheType["hosts"].Set(200, 600, 2, "TestHost2")

	// Test Save
	if err := cacheEntry.save(); err != nil {
		t.Errorf("Save failed: %v", err)
	}

	// Create new cache and load data
	newCacheEntry := newCacheEntry()
	newCacheEntry.db = db
	if err := newCacheEntry.load(); err != nil {
		t.Errorf("Load failed: %v", err)
	}

	// Verify loaded data
	hostsCache, exists := newCacheEntry.CacheType["hosts"]
	if !exists {
		t.Fatal("Hosts cache type should exist after load")
	}

	if originalID, found := hostsCache.GetOriginalID(100, 1); !found || originalID != 500 {
		t.Errorf("Loaded data mismatch: expected 500, got %d", originalID)
	}

	if originalID, found := hostsCache.GetOriginalID(200, 2); !found || originalID != 600 {
		t.Errorf("Loaded data mismatch: expected 600, got %d", originalID)
	}
}

func TestCacheEntry_AutoSaveAndCleanup(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "testdb.*.bolt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	db, err := bbolt.Open(tmpFile.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cacheEntry := newCacheEntry()
	cacheEntry.db = db
	cacheEntry.CacheType["hosts"] = newCache()

	// Start background processes
	cacheEntry.start(100*time.Millisecond, time.Hour, 40*time.Millisecond)

	// Add some data
	cacheEntry.CacheType["hosts"].Set(100, 500, 1, "TestHost")

	// Let it run for a bit
	time.Sleep(150 * time.Millisecond)

	// Stop processes
	cacheEntry.Stop()

	// Reopen database to verify persistence
	db2, err := bbolt.Open(tmpFile.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	// Verify data was saved
	newCacheEntry := newCacheEntry()
	newCacheEntry.db = db2
	if err := newCacheEntry.load(); err != nil {
		t.Errorf("Load after auto-save failed: %v", err)
	}

	if _, exists := newCacheEntry.CacheType["hosts"]; !exists {
		t.Error("Data should be saved by auto-save")
	}
}

func TestCacheEntry_GetStats(t *testing.T) {
	cacheEntry := newCacheEntry()
	cacheEntry.CacheType["hosts"] = newCache()
	cacheEntry.CacheType["items"] = newCache()

	// Add test data
	cacheEntry.CacheType["hosts"].Set(100, 500, 1, "HostA")
	cacheEntry.CacheType["hosts"].Set(200, 600, 2, "HostB")
	cacheEntry.CacheType["items"].Set(300, 700, 1, "ItemA")

	// Get statistics
	stats := cacheEntry.GetStats()

	// Verify stats
	if stats["hosts_proxy_items"] != 2 {
		t.Errorf("Expected 2 hosts proxy items, got %d", stats["hosts_proxy_items"])
	}

	if stats["hosts_reverse_items"] != 2 {
		t.Errorf("Expected 2 hosts reverse items, got %d", stats["hosts_reverse_items"])
	}

	if stats["items_proxy_items"] != 1 {
		t.Errorf("Expected 1 items proxy item, got %d", stats["items_proxy_items"])
	}
}

func TestCacheEntryInit(t *testing.T) {
	cacheFields := map[string]string{
		"hosts": "Host cache",
		"items": "Item cache",
	}

	cacheEntry := cacheEntryInit(cacheFields)

	// Verify cache types are initialized
	if _, exists := cacheEntry.CacheType["hosts"]; !exists {
		t.Error("Hosts cache type should be initialized")
	}

	if _, exists := cacheEntry.CacheType["items"]; !exists {
		t.Error("Items cache type should be initialized")
	}

	if _, exists := cacheEntry.CacheType["nonexistent"]; exists {
		t.Error("Non-existent cache type should not be initialized")
	}
}

func TestConcurrentAccess(t *testing.T) {
	cache := newCache()
	iterations := 1000

	// Test concurrent writes
	go func() {
		for i := 0; i < iterations; i++ {
			cache.Set(i, i+1000, 1, "Test")
		}
	}()

	go func() {
		for i := 0; i < iterations; i++ {
			cache.Set(i, i+2000, 2, "Test")
		}
	}()

	// Test concurrent reads
	go func() {
		for i := 0; i < iterations; i++ {
			cache.GetOriginalID(i%100, 1)
		}
	}()

	go func() {
		for i := 0; i < iterations; i++ {
			cache.GetProxyID(i%100+1000, 1)
		}
	}()

	// Let goroutines complete
	time.Sleep(100 * time.Millisecond)

	// Should not panic and maintain data consistency
	cache.mu.RLock()
	proxyCount := len(cache.ProxyID)
	cache.mu.RUnlock()

	if proxyCount == 0 {
		t.Error("Concurrent access should maintain data")
	}
}

func TestEdgeCases(t *testing.T) {
	cache := newCache()

	// Test zero values
	cache.Set(0, 0, 0, "")
	if _, found := cache.GetOriginalID(0, 0); found {
		t.Error("Zero values should be handled correctly")
	}

	// Test negative values
	cache.Set(-1, -1, -1, "Negative")
	if _, found := cache.GetOriginalID(-1, -1); found {
		t.Error("Negative values should be handled correctly")
	}

	// Test very large values
	cache.Set(999999, 999999, 999999, "Large")
	if originalID, found := cache.GetOriginalID(999999, 999999); !found || originalID != 999999 {
		t.Error("Large values should be handled correctly")
	}
}

func TestMultipleServersSameProxyID(t *testing.T) {
	cache := newCache()

	// Same proxyID, different servers
	cache.Set(100, 500, 1, "Server1")
	cache.Set(100, 600, 2, "Server2")
	cache.Set(100, 700, 3, "Server3")

	// Verify all mappings work
	testCases := []struct {
		serverID   int
		expectedID int
	}{
		{1, 500}, {2, 600}, {3, 700},
	}

	for _, tc := range testCases {
		if originalID, found := cache.GetOriginalID(100, tc.serverID); !found || originalID != tc.expectedID {
			t.Errorf("Server %d: expected %d, got %d", tc.serverID, tc.expectedID, originalID)
		}
	}

	// Verify reverse mappings
	if proxyID, found := cache.GetProxyID(500, 1); !found || proxyID != 100 {
		t.Error("Reverse mapping for server 1 failed")
	}
	if proxyID, found := cache.GetProxyID(600, 2); !found || proxyID != 100 {
		t.Error("Reverse mapping for server 2 failed")
	}
	if proxyID, found := cache.GetProxyID(700, 3); !found || proxyID != 100 {
		t.Error("Reverse mapping for server 3 failed")
	}
}

func TestCacheType_SetInvalidParameters(t *testing.T) {
	cache := newCache()

	// Test invalid parameters (should be ignored with warning)
	cache.Set(0, 100, 1, "Invalid")
	cache.Set(100, 0, 1, "Invalid")
	cache.Set(100, 200, 0, "Invalid")
	cache.Set(-1, 100, 1, "Negative")

	// Verify no data was stored for invalid parameters
	if _, found := cache.GetOriginalID(0, 1); found {
		t.Error("Invalid proxyID should not be stored")
	}
	if _, found := cache.GetOriginalID(-1, 1); found {
		t.Error("Negative proxyID should not be stored")
	}
}

func TestCacheEntry_StopWithoutStart(t *testing.T) {
	cacheEntry := newCacheEntry()

	// Should not panic when stopping without starting
	cacheEntry.Stop()
}

func TestCacheEntry_StartWithZeroIntervals(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "testdb.*.bolt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	db, err := bbolt.Open(tmpFile.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cacheEntry := newCacheEntry()
	cacheEntry.db = db

	// Should not start with zero intervals
	cacheEntry.start(0, 0, 0)

	// Add some data
	cacheEntry.CacheType["hosts"] = newCache()
	cacheEntry.CacheType["hosts"].Set(100, 500, 1, "TestHost")

	// Wait a bit and stop
	time.Sleep(50 * time.Millisecond)
	cacheEntry.Stop()

	// Verify processes didn't run (no panic)
}

func TestCacheEntry_LoadEmptyDatabase(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "testdb.*.bolt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	db, err := bbolt.Open(tmpFile.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cacheEntry := newCacheEntry()
	cacheEntry.db = db

	// Should not error when loading from empty database
	if err := cacheEntry.load(); err != nil {
		t.Errorf("Load from empty database should not fail: %v", err)
	}
}

func TestCacheEntry_SaveEmptyCache(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "testdb.*.bolt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	db, err := bbolt.Open(tmpFile.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cacheEntry := newCacheEntry()
	cacheEntry.db = db

	// Should not error when saving empty cache
	if err := cacheEntry.save(); err != nil {
		t.Errorf("Save empty cache should not fail: %v", err)
	}
}

func TestCacheType_ConcurrentCleanup(t *testing.T) {
	cache := newCache()
	iterations := 100

	// Add test data
	for i := 1; i <= iterations; i++ {
		cache.Set(i, i+1000, 1, "Test")
	}

	// Run cleanup concurrently with reads/writes
	done := make(chan bool)

	go func() {
		for i := 0; i < 10; i++ {
			cache.cleanup(time.Hour)
			time.Sleep(10 * time.Millisecond)
		}
		done <- true
	}()

	go func() {
		for i := 1; i <= iterations; i++ {
			cache.GetOriginalID(i, 1)
		}
	}()

	go func() {
		for i := iterations + 1; i <= iterations+50; i++ {
			cache.Set(i, i+1000, 1, "Concurrent")
		}
	}()

	<-done
	// Test should not panic
}

func TestCacheEntry_GetStatsEmpty(t *testing.T) {
	cacheEntry := newCacheEntry()

	stats := cacheEntry.GetStats()

	if len(stats) != 0 {
		t.Errorf("Empty cache should return empty stats, got %v", stats)
	}
}

func TestCacheType_ReverseMappingConsistency(t *testing.T) {
	cache := newCache()

	// Set multiple mappings
	cache.Set(100, 500, 1, "Server1")
	cache.Set(100, 600, 2, "Server2")
	cache.Set(200, 700, 1, "Server1")

	// Verify reverse mappings are consistent
	if proxyID, found := cache.GetProxyID(500, 1); !found || proxyID != 100 {
		t.Error("Reverse mapping inconsistency")
	}
	if proxyID, found := cache.GetProxyID(600, 2); !found || proxyID != 100 {
		t.Error("Reverse mapping inconsistency")
	}
	if proxyID, found := cache.GetProxyID(700, 1); !found || proxyID != 200 {
		t.Error("Reverse mapping inconsistency")
	}
}

func TestCacheType_UpdateReverseMapping(t *testing.T) {
	cache := newCache()

	// Initial mapping
	cache.Set(100, 500, 1, "Server1")
	cache.Set(200, 1500, 1, "Server1")

	// Wait for timestamp difference
	time.Sleep(50 * time.Millisecond)

	// Update with different OriginalID
	cache.Set(100, 600, 1, "Server1")
	cache.cleanup(30 * time.Millisecond)

	// Verify reverse mapping was updated
	if proxyID, found := cache.GetProxyID(600, 1); !found || proxyID != 100 {
		t.Error("Reverse mapping should be updated")
	}

	// Old reverse mapping should be removed
	if _, found := cache.GetProxyID(1500, 1); found {
		t.Error("Old reverse mapping should be removed")
	}
}

func TestInitFunction(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "testdb.*.bolt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	cfg := CacheCfg{
		TTL:             "1h",
		CleanupInterval: "1m",
		DBPath:          tmpFile.Name(),
		AutoSave:        "30s",
		CachedFields: map[string]string{
			"hosts": "Host cache",
			"items": "Item cache",
		},
	}

	cache := Init(cfg)

	if cache == nil {
		t.Fatal("Init should return non-nil cache")
	}

	// Verify cache types are initialized
	cache.mu.RLock()
	_, hostsExists := cache.CacheType["hosts"]
	_, itemsExists := cache.CacheType["items"]
	cache.mu.RUnlock()

	if !hostsExists {
		t.Error("Hosts cache type should be initialized")
	}
	if !itemsExists {
		t.Error("Items cache type should be initialized")
	}

	// Stop background processes
	cache.Stop()
}

func TestSerializableConversion(t *testing.T) {
	cacheEntry := newCacheEntry()
	cacheEntry.CacheType["hosts"] = newCache()
	cacheEntry.CacheType["hosts"].Set(100, 500, 1, "TestHost")

	serializable := cacheEntry.toSerializable()

	if serializable == nil {
		t.Fatal("Serializable conversion should not return nil")
	}

	if _, exists := serializable.CacheType["hosts"]; !exists {
		t.Error("Hosts should exist in serializable cache")
	}
}

func TestCacheEntry_DoubleStart(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "testdb.*.bolt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	db, err := bbolt.Open(tmpFile.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cacheEntry := newCacheEntry()
	cacheEntry.db = db

	// First start should work
	cacheEntry.start(100*time.Millisecond, time.Hour, 50*time.Millisecond)

	// Second start should be ignored (already running)
	cacheEntry.start(100*time.Millisecond, time.Hour, 50*time.Millisecond)

	// Should not panic and processes should run normally
	time.Sleep(100 * time.Millisecond)
	cacheEntry.Stop()
}

func TestCacheType_DeleteMultiple(t *testing.T) {
	cache := newCache()

	// Setup test data
	cache.Set(100, 500, 1, "HostA")
	cache.Set(200, 600, 1, "HostB")
	cache.Set(300, 700, 1, "HostC")

	// Delete multiple proxyIDs
	cache.Delete([]int{100, 300})

	// Verify deletions
	if _, found := cache.GetOriginalID(100, 1); found {
		t.Error("ProxyID 100 should be deleted")
	}
	if _, found := cache.GetOriginalID(300, 1); found {
		t.Error("ProxyID 300 should be deleted")
	}

	// Verify remaining data
	if _, found := cache.GetOriginalID(200, 1); !found {
		t.Error("ProxyID 200 should still exist")
	}
}
