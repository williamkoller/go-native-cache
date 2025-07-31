// main.go
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// CacheItem representa um item no cache
type CacheItem struct {
	Value     interface{} `json:"value"`
	ExpiresAt time.Time   `json:"expires_at"`
	CreatedAt time.Time   `json:"created_at"`
}

// Cache é um cache thread-safe com limpeza automática
type Cache struct {
	sync.RWMutex
	items         map[string]CacheItem
	defaultTTL    time.Duration
	cleanupTicker *time.Ticker
	stats         CacheStats
}

// CacheStats mantém estatísticas do cache
type CacheStats struct {
	sync.RWMutex
	Hits         int64 `json:"hits"`
	Misses       int64 `json:"misses"`
	ItemsExpired int64 `json:"items_expired"`
	ItemsDeleted int64 `json:"items_deleted"`
	CleanupRuns  int64 `json:"cleanup_runs"`
}

// CacheStatsData contém apenas os dados estatísticos sem o mutex
type CacheStatsData struct {
	Hits         int64 `json:"hits"`
	Misses       int64 `json:"misses"`
	ItemsExpired int64 `json:"items_expired"`
	ItemsDeleted int64 `json:"items_deleted"`
	CleanupRuns  int64 `json:"cleanup_runs"`
}

// NewCache cria uma nova instância do cache
func NewCache(defaultTTL time.Duration, cleanupInterval time.Duration) *Cache {
	c := &Cache{
		items:         make(map[string]CacheItem),
		defaultTTL:    defaultTTL,
		cleanupTicker: time.NewTicker(cleanupInterval),
		stats:         CacheStats{},
	}

	// Goroutine de limpeza automática
	go func() {
		for range c.cleanupTicker.C {
			c.cleanup()
		}
	}()

	log.Printf("Cache inicializado - TTL padrão: %v, Limpeza a cada: %v", defaultTTL, cleanupInterval)
	return c
}

// Set adiciona um item ao cache
func (c *Cache) Set(key string, value interface{}, ttl ...time.Duration) {
	c.Lock()
	defer c.Unlock()

	duration := c.defaultTTL
	if len(ttl) > 0 {
		duration = ttl[0]
	}

	c.items[key] = CacheItem{
		Value:     value,
		ExpiresAt: time.Now().Add(duration),
		CreatedAt: time.Now(),
	}

	log.Printf("Item adicionado ao cache: %s (TTL: %v)", key, duration)
}

// Get recupera um item do cache
func (c *Cache) Get(key string) (interface{}, bool) {
	c.RLock()
	defer c.RUnlock()

	item, exists := c.items[key]
	if !exists {
		c.stats.Lock()
		c.stats.Misses++
		c.stats.Unlock()
		return nil, false
	}

	if time.Now().After(item.ExpiresAt) {
		c.stats.Lock()
		c.stats.Misses++
		c.stats.Unlock()
		return nil, false
	}

	c.stats.Lock()
	c.stats.Hits++
	c.stats.Unlock()

	return item.Value, true
}

// Delete remove um item do cache
func (c *Cache) Delete(key string) bool {
	c.Lock()
	defer c.Unlock()

	if _, exists := c.items[key]; exists {
		delete(c.items, key)
		c.stats.Lock()
		c.stats.ItemsDeleted++
		c.stats.Unlock()
		log.Printf("Item removido do cache: %s", key)
		return true
	}

	return false
}

// cleanup remove itens expirados do cache
func (c *Cache) cleanup() {
	c.Lock()
	defer c.Unlock()

	now := time.Now()
	expired := 0

	for k, v := range c.items {
		if now.After(v.ExpiresAt) {
			delete(c.items, k)
			expired++
		}
	}

	c.stats.Lock()
	c.stats.ItemsExpired += int64(expired)
	c.stats.CleanupRuns++
	c.stats.Unlock()

	if expired > 0 {
		log.Printf("Limpeza automática: %d itens expirados removidos", expired)
	}
}

// GetStats retorna as estatísticas do cache
func (c *Cache) GetStats() CacheStatsData {
	c.stats.RLock()
	defer c.stats.RUnlock()

	return CacheStatsData{
		Hits:         c.stats.Hits,
		Misses:       c.stats.Misses,
		ItemsExpired: c.stats.ItemsExpired,
		ItemsDeleted: c.stats.ItemsDeleted,
		CleanupRuns:  c.stats.CleanupRuns,
	}
}

// Clear limpa todo o cache
func (c *Cache) Clear() {
	c.Lock()
	defer c.Unlock()

	itemCount := len(c.items)
	c.items = make(map[string]CacheItem)

	log.Printf("Cache limpo: %d itens removidos", itemCount)
}

// Size retorna o número de itens no cache
func (c *Cache) Size() int {
	c.RLock()
	defer c.RUnlock()
	return len(c.items)
}

// Stop para a goroutine de limpeza
func (c *Cache) Stop() {
	c.cleanupTicker.Stop()
	log.Println("Cache parado")
}

// === HANDLERS HTTP ===

var cache *Cache

// UserData representa dados de usuário para demonstração
type UserData struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	LastSeen string `json:"last_seen"`
}

// simulateDBQuery simula uma consulta ao banco de dados
func simulateDBQuery(userID int) *UserData {
	// Simula latência do banco
	time.Sleep(time.Millisecond * 100)

	return &UserData{
		ID:       userID,
		Name:     fmt.Sprintf("Usuário %d", userID),
		Email:    fmt.Sprintf("user%d@example.com", userID),
		LastSeen: time.Now().Format("2006-01-02 15:04:05"),
	}
}

// getUserHandler busca dados do usuário (com cache)
func getUserHandler(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.URL.Query().Get("id")
	if userIDStr == "" {
		http.Error(w, "ID do usuário é obrigatório", http.StatusBadRequest)
		return
	}

	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		http.Error(w, "ID do usuário deve ser um número", http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("user:%d", userID)

	// Tenta buscar no cache primeiro
	if cachedData, found := cache.Get(cacheKey); found {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data":   cachedData,
			"cached": true,
		})
		log.Printf("Cache HIT para usuário %d", userID)
		return
	}

	// Cache miss - busca no "banco de dados"
	log.Printf("Cache MISS para usuário %d - buscando no banco", userID)
	userData := simulateDBQuery(userID)

	// Armazena no cache por 30 segundos
	cache.Set(cacheKey, userData, 30*time.Second)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":   userData,
		"cached": false,
	})
}

// setUserHandler adiciona/atualiza dados do usuário no cache
func setUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	var userData UserData
	if err := json.NewDecoder(r.Body).Decode(&userData); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("user:%d", userData.ID)
	cache.Set(cacheKey, userData, 60*time.Second)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Usuário adicionado ao cache",
		"key":     cacheKey,
	})
}

// deleteUserHandler remove um usuário do cache
func deleteUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	userIDStr := r.URL.Query().Get("id")
	if userIDStr == "" {
		http.Error(w, "ID do usuário é obrigatório", http.StatusBadRequest)
		return
	}

	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		http.Error(w, "ID do usuário deve ser um número", http.StatusBadRequest)
		return
	}

	cacheKey := fmt.Sprintf("user:%d", userID)
	deleted := cache.Delete(cacheKey)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"deleted": deleted,
		"key":     cacheKey,
	})
}

// statsHandler retorna estatísticas do cache
func statsHandler(w http.ResponseWriter, r *http.Request) {
	stats := cache.GetStats()
	size := cache.Size()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"stats": stats,
		"size":  size,
	})
}

// clearHandler limpa todo o cache
func clearHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		return
	}

	cache.Clear()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Cache limpo com sucesso",
	})
}

// homeHandler página inicial com instruções
func homeHandler(w http.ResponseWriter, r *http.Request) {
	html := `
<!DOCTYPE html>
<html>
<head>
    <title>Sistema de Cache - Demo</title>
    <meta charset="UTF-8">
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; }
        .endpoint { background: #f4f4f4; padding: 10px; margin: 10px 0; border-radius: 5px; }
        .method { color: #0066cc; font-weight: bold; }
    </style>
</head>
<body>
    <h1>🚀 Sistema de Cache Thread-Safe</h1>
    <p>Sistema de cache com limpeza automática implementado em Go.</p>
    
    <h2>📋 Endpoints Disponíveis:</h2>
    
    <div class="endpoint">
        <span class="method">GET</span> <code>/user?id=123</code><br>
        Busca dados do usuário (com cache automático)
    </div>
    
    <div class="endpoint">
        <span class="method">POST</span> <code>/user</code><br>
        Adiciona usuário ao cache<br>
        Body: <code>{"id": 123, "name": "João", "email": "joao@exemplo.com"}</code>
    </div>
    
    <div class="endpoint">
        <span class="method">DELETE</span> <code>/user?id=123</code><br>
        Remove usuário do cache
    </div>
    
    <div class="endpoint">
        <span class="method">GET</span> <code>/stats</code><br>
        Estatísticas do cache (hits, misses, etc.)
    </div>
    
    <div class="endpoint">
        <span class="method">POST</span> <code>/clear</code><br>
        Limpa todo o cache
    </div>
    
    <h2>🧪 Teste Rápido:</h2>
    <p>1. Acesse <a href="/user?id=1">/user?id=1</a> (cache miss)</p>
    <p>2. Acesse novamente <a href="/user?id=1">/user?id=1</a> (cache hit)</p>
    <p>3. Veja as <a href="/stats">estatísticas</a></p>
    
    <p><em>TTL padrão: 30s | Limpeza automática: 10s</em></p>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

func main() {
	// Inicializa o cache
	// TTL padrão: 60 segundos
	// Limpeza automática: a cada 10 segundos
	cache = NewCache(60*time.Second, 10*time.Second)
	defer cache.Stop()

	// Configura rotas
	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getUserHandler(w, r)
		case http.MethodPost:
			setUserHandler(w, r)
		case http.MethodDelete:
			deleteUserHandler(w, r)
		default:
			http.Error(w, "Método não permitido", http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("/stats", statsHandler)
	http.HandleFunc("/clear", clearHandler)

	// Adiciona alguns dados de exemplo
	go func() {
		time.Sleep(time.Second)
		log.Println("Adicionando dados de exemplo ao cache...")

		cache.Set("exemplo:1", "Primeiro item de exemplo", 120*time.Second)
		cache.Set("exemplo:2", map[string]interface{}{
			"tipo":  "objeto",
			"valor": 42,
			"ativo": true,
		}, 90*time.Second)

		// Simula alta concorrência
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				key := fmt.Sprintf("concurrent:%d", id)
				cache.Set(key, fmt.Sprintf("valor_%d", id), 15*time.Second)

				// Simula leituras
				for j := 0; j < 5; j++ {
					cache.Get(key)
					time.Sleep(time.Millisecond * 10)
				}
			}(i)
		}
		wg.Wait()

		log.Println("Dados de exemplo adicionados!")
	}()

	port := ":8080"
	log.Printf("🌐 Servidor iniciado em http://localhost%s", port)
	log.Printf("📊 Acesse http://localhost%s/stats para ver estatísticas", port)

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal("Erro ao iniciar servidor:", err)
	}
}
