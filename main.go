package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// CacheItem representa um item no cache com TTL
type CacheItem struct {
	Value     interface{} `json:"value"`
	ExpiresAt time.Time   `json:"expires_at"`
	CreatedAt time.Time   `json:"created_at"`
}

// IsExpired verifica se o item expirou
func (item *CacheItem) IsExpired() bool {
	return time.Now().After(item.ExpiresAt)
}

// NativeCache estrutura principal do cache
type NativeCache struct {
	items       map[string]*CacheItem
	mutex       sync.RWMutex
	defaultTTL  time.Duration
	cleanupTick time.Duration
	ctx         context.Context
	cancel      context.CancelFunc
	stats       CacheStats
}

// CacheStats estatísticas do cache
type CacheStats struct {
	Hits         int64 `json:"hits"`
	Misses       int64 `json:"misses"`
	Sets         int64 `json:"sets"`
	Deletes      int64 `json:"deletes"`
	Cleanups     int64 `json:"cleanups"`
	ItemsCleaned int64 `json:"items_cleaned"`
}

// NewNativeCache cria uma nova instância do cache
func NewNativeCache(defaultTTL, cleanupInterval time.Duration) *NativeCache {
	ctx, cancel := context.WithCancel(context.Background())

	cache := &NativeCache{
		items:       make(map[string]*CacheItem),
		defaultTTL:  defaultTTL,
		cleanupTick: cleanupInterval,
		ctx:         ctx,
		cancel:      cancel,
		stats:       CacheStats{},
	}

	// Inicia o processo de limpeza automática
	go cache.startCleanup()

	return cache
}

// Set adiciona ou atualiza um item no cache
func (c *NativeCache) Set(key string, value interface{}, ttl ...time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	expiration := c.defaultTTL
	if len(ttl) > 0 {
		expiration = ttl[0]
	}

	c.items[key] = &CacheItem{
		Value:     value,
		ExpiresAt: time.Now().Add(expiration),
		CreatedAt: time.Now(),
	}

	c.stats.Sets++
}

// Get recupera um item do cache
func (c *NativeCache) Get(key string) (interface{}, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	item, exists := c.items[key]
	if !exists {
		c.stats.Misses++
		return nil, false
	}

	if item.IsExpired() {
		c.stats.Misses++
		// Remove item expirado durante o get
		go c.Delete(key)
		return nil, false
	}

	c.stats.Hits++
	return item.Value, true
}

// Delete remove um item do cache
func (c *NativeCache) Delete(key string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if _, exists := c.items[key]; exists {
		delete(c.items, key)
		c.stats.Deletes++
	}
}

// Clear limpa todo o cache
func (c *NativeCache) Clear() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.items = make(map[string]*CacheItem)
	c.stats = CacheStats{}
}

// Size retorna o número de itens no cache
func (c *NativeCache) Size() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return len(c.items)
}

// Keys retorna todas as chaves do cache
func (c *NativeCache) Keys() []string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	keys := make([]string, 0, len(c.items))
	for key := range c.items {
		keys = append(keys, key)
	}
	return keys
}

// Has verifica se uma chave existe no cache
func (c *NativeCache) Has(key string) bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	item, exists := c.items[key]
	if !exists {
		return false
	}

	return !item.IsExpired()
}

// GetStats retorna as estatísticas do cache
func (c *NativeCache) GetStats() CacheStats {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.stats
}

// startCleanup inicia o processo de limpeza automática
func (c *NativeCache) startCleanup() {
	ticker := time.NewTicker(c.cleanupTick)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.cleanup()
		}
	}
}

// cleanup remove itens expirados
func (c *NativeCache) cleanup() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	now := time.Now()
	itemsCleaned := int64(0)

	for key, item := range c.items {
		if now.After(item.ExpiresAt) {
			delete(c.items, key)
			itemsCleaned++
		}
	}

	c.stats.Cleanups++
	c.stats.ItemsCleaned += itemsCleaned
}

// Close para o cache e limpa recursos
func (c *NativeCache) Close() {
	c.cancel()
	c.Clear()
}

// ToJSON exporta o cache para JSON (para debug/backup)
func (c *NativeCache) ToJSON() ([]byte, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	data := struct {
		Items map[string]*CacheItem `json:"items"`
		Stats CacheStats            `json:"stats"`
		Size  int                   `json:"size"`
	}{
		Items: c.items,
		Stats: c.stats,
		Size:  len(c.items),
	}

	return json.MarshalIndent(data, "", "  ")
}

// SetWithCallback define um item com callback quando expirar
func (c *NativeCache) SetWithCallback(key string, value interface{}, ttl time.Duration, callback func(key string, value interface{})) {
	c.Set(key, value, ttl)

	// Goroutine para executar callback quando expirar
	go func() {
		time.Sleep(ttl)
		if _, exists := c.Get(key); !exists && callback != nil {
			callback(key, value)
		}
	}()
}

// GetOrSet recupera um item ou define um novo se não existir
func (c *NativeCache) GetOrSet(key string, defaultValue interface{}, ttl ...time.Duration) interface{} {
	if value, exists := c.Get(key); exists {
		return value
	}

	c.Set(key, defaultValue, ttl...)
	return defaultValue
}

// Função principal - exemplo de uso
func main() {
	// Cria cache com TTL padrão de 5 minutos e limpeza a cada 1 minuto
	cache := NewNativeCache(5*time.Minute, 1*time.Minute)
	defer cache.Close()

	fmt.Println("🚀 Cache nativo Go iniciado - rodando 24/7!")

	// Exemplos de uso
	cache.Set("user:123", map[string]string{
		"name":  "João Silva",
		"email": "joao@email.com",
	})

	cache.Set("config:app", "production", 10*time.Minute)
	cache.Set("temp:data", "dados temporários", 30*time.Second)

	// Teste de recuperação
	if user, exists := cache.Get("user:123"); exists {
		fmt.Printf("✅ Usuário encontrado: %+v\n", user)
	}

	// SetWithCallback exemplo
	cache.SetWithCallback("notification:1", "Mensagem importante",
		2*time.Second, func(key string, value interface{}) {
			fmt.Printf("⏰ Callback executado para %s: %v\n", key, value)
		})

	// GetOrSet exemplo
	defaultConfig := cache.GetOrSet("config:theme", "dark")
	fmt.Printf("🎨 Tema: %v\n", defaultConfig)

	// Loop principal simulando aplicação 24/7
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)

		// Adiciona dados dinâmicos
		cache.Set(fmt.Sprintf("dynamic:%d", i), fmt.Sprintf("valor-%d", i), 3*time.Second)

		// Mostra estatísticas a cada 3 segundos
		if i%3 == 0 {
			stats := cache.GetStats()
			fmt.Printf("\n📊 Estatísticas do Cache:\n")
			fmt.Printf("   Hits: %d | Misses: %d | Sets: %d\n", stats.Hits, stats.Misses, stats.Sets)
			fmt.Printf("   Limpezas: %d | Itens removidos: %d\n", stats.Cleanups, stats.ItemsCleaned)
			fmt.Printf("   Tamanho atual: %d itens\n", cache.Size())
		}
	}

	// Exporta estado do cache
	if jsonData, err := cache.ToJSON(); err == nil {
		fmt.Printf("\n💾 Estado do cache:\n%s\n", string(jsonData))
	}

	fmt.Println("\n✨ Cache funcionando perfeitamente! Pronto para rodar 24/7")
}
