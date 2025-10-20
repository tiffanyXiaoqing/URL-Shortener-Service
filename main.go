// file: main.go
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"   // Redis client
	"github.com/go-sql-driver/mysql" // MySQL driver
)

// Global variables for database and cache connections, and a flag for memory mode
var (
	db          *sql.DB       // MySQL database connection
	redisClient *redis.Client // Redis cache client
	useMemory   bool          // if true, use in-memory storage instead of DB
)

// In-memory store (used only if DB is not available, e.g., for testing)
type memoryStore struct {
	sync.RWMutex
	data map[string]string // maps "domain:code" -> original URL
}

var memStore = memoryStore{data: make(map[string]string)}

// Regex to validate short code format in the URL path.
var shortCodePattern = regexp.MustCompile(`^/[A-Za-z0-9]{9}$`)

// Structures for JSON input and output
type newURLRequest struct {
	Domain string `json:"domain"`
	URL    string `json:"url"`
}
type newURLResponse struct {
	URL        string `json:"url"`
	ShortenURL string `json:"shortenUrl"`
}

func main() {
	// Read configuration from environment (for real deployments)
	mysqlDSN := os.Getenv("MYSQL_DSN")   // e.g., "user:pass@tcp(host:3306)/dbname"
	redisAddr := os.Getenv("REDIS_ADDR") // e.g., "mycache.xyz.cache.amazonaws.com:6379"
	redisPass := os.Getenv("REDIS_PASS") // password if any
	redisDBIdx := 0
	if dbStr := os.Getenv("REDIS_DB"); dbStr != "" {
		// Parse redis DB index if provided
		fmt.Sscanf(dbStr, "%d", &redisDBIdx)
	}

	// Initialize MySQL connection
	if mysqlDSN != "" {
		var err error
		db, err = sql.Open("mysql", mysqlDSN)
		if err != nil {
			log.Println("Failed to open MySQL connection:", err)
		} else if err = db.Ping(); err != nil {
			log.Println("MySQL ping failed, using in-memory store. Error:", err)
			db = nil
		} else {
			// Ensure the necessary table exists
			createTable()
		}
	}
	if db == nil {
		useMemory = true
		log.Println("Using in-memory storage (no persistence).")
	}

	// Initialize Redis connection (if configured)
	if redisAddr != "" {
		redisClient = redis.NewClient(&redis.Options{
			Addr:     redisAddr,
			Password: redisPass,
			DB:       redisDBIdx,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := redisClient.Ping(ctx).Err(); err != nil {
			log.Println("Redis connection failed, proceeding without cache. Error:", err)
			redisClient = nil
		} else {
			log.Println("Connected to Redis cache.")
		}
	}

	// Set up HTTP handlers
	http.HandleFunc("/newurl", handleNewURL) // for creating new short URLs
	http.HandleFunc("/", handleRedirect)     // for redirecting short URLs (catch-all)

	// Start the HTTP server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Println("Server starting on port", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// createTable creates the database table for URL storage if it doesn't already exist.
func createTable() {
	if db == nil {
		return
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS shortened_urls (
        id BIGINT AUTO_INCREMENT PRIMARY KEY,
        domain VARCHAR(255) NOT NULL,
        code VARCHAR(9) NOT NULL,
        original_url TEXT NOT NULL,
        UNIQUE KEY unique_domain_code (domain, code)
    ) ENGINE=InnoDB;`)
	if err != nil {
		log.Println("Error creating table:", err)
	} else {
		log.Println("Verified that 'shortened_urls' table exists or was created.")
	}
}

// handleNewURL handles the POST /newurl requests. It reads the JSON body, creates a short URL, and returns the result.
func handleNewURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req newURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Normalize and validate input
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if req.Domain == "" || req.URL == "" {
		http.Error(w, "Bad Request: 'domain' and 'url' are required", http.StatusBadRequest)
		return
	}

	// Save the URL mapping (generate short code and store in DB or memory)
	code, err := saveURLMapping(req.Domain, req.URL)
	if err != nil {
		log.Println("Error saving URL mapping:", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Build the full shortened URL to return
	scheme := "https://"
	// Use http scheme for localhost or IP addresses (assuming no SSL in dev)
	if strings.Contains(req.Domain, "localhost") || strings.Contains(req.Domain, "127.0.0.1") {
		scheme = "http://"
	}
	shortURL := fmt.Sprintf("%s%s/%s", scheme, req.Domain, code)

	// Respond with JSON
	resp := newURLResponse{
		URL:        req.URL,
		ShortenURL: shortURL,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleRedirect handles GET requests for short URLs (e.g., /abcdef123). It finds the original URL and redirects.
func handleRedirect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	path := r.URL.Path // e.g. "/abcdef123"
	if !shortCodePattern.MatchString(path) {
		// If the path is not exactly 9 alphanumeric chars, return 404.
		// This also catches requests to "/" or any other undefined paths.
		http.NotFound(w, r)
		return
	}
	code := path[1:] // strip leading "/"
	// Determine the domain for lookup from the Host header
	domain := strings.ToLower(r.Host)
	if idx := strings.Index(domain, ":"); idx != -1 {
		domain = domain[:idx] // remove port if present (e.g., "localhost:8080" -> "localhost")
	}

	// Lookup the original URL from storage (cache or DB)
	origURL, err := getOriginalURL(domain, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No mapping found for this code
			http.NotFound(w, r)
		} else {
			// Some other error (database issue, etc.)
			log.Println("Error retrieving URL for code", code, ":", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Issue a 301 redirect to the original URL
	http.Redirect(w, r, origURL, http.StatusMovedPermanently)
}

// saveURLMapping generates a unique short code for the given URL and stores the mapping in the database (or memory).
// Returns the short code.
func saveURLMapping(domain, originalURL string) (string, error) {
	if useMemory {
		// Store in in-memory map (non-persistent, for testing/fallback)
		for attempt := 0; attempt < 5; attempt++ {
			code := generateCode(9)
			key := domain + ":" + code
			memStore.Lock()
			if _, exists := memStore.data[key]; !exists {
				// Code is unique in memory, use it
				memStore.data[key] = originalURL
				memStore.Unlock()
				return code, nil
			}
			memStore.Unlock()
			// if exists, loop to try another code
		}
		return "", fmt.Errorf("could not generate a unique code after several attempts")
	}

	// Use database
	for attempt := 0; attempt < 5; attempt++ {
		code := generateCode(9)
		// Try to insert the new mapping
		_, err := db.Exec(
			"INSERT INTO shortened_urls(domain, code, original_url) VALUES (?, ?, ?)",
			domain, code, originalURL,
		)
		if err == nil {
			// Successfully inserted, now update cache (if available) for quick future lookup
			if redisClient != nil {
				cacheKey := fmt.Sprintf("short:%s:%s", domain, code)
				// No expiration (0 = keep until evicted), since link is permanent
				if err := redisClient.Set(context.Background(), cacheKey, originalURL, 0).Err(); err != nil {
					log.Println("Warning: failed to set Redis cache for", cacheKey, ":", err)
				}
			}
			return code, nil
		}
		// If code collision (duplicate key), generate a new code and retry
		if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number == 1062 {
			continue // duplicate entry for unique index, try another code
		}
		// Other errors (e.g., DB unavailable)
		return "", err
	}
	// If we exit loop, we failed to find a unique code after several tries (extremely unlikely)
	return "", fmt.Errorf("failed to generate unique code (too many collisions)")
}

// getOriginalURL retrieves the original URL for a given domain and short code.
// It first checks Redis cache, then falls back to MySQL if not found in cache.
func getOriginalURL(domain, code string) (string, error) {
	if useMemory {
		// Lookup from in-memory store
		key := domain + ":" + code
		memStore.RLock()
		orig, exists := memStore.data[key]
		memStore.RUnlock()
		if !exists {
			return "", sql.ErrNoRows
		}
		return orig, nil
	}

	// Try cache first
	cacheKey := fmt.Sprintf("short:%s:%s", domain, code)
	if redisClient != nil {
		val, err := redisClient.Get(context.Background(), cacheKey).Result()
		if err == nil {
			return val, nil // cache hit
		}
		if err != redis.Nil {
			// An unexpected error occurred with Redis (connection issue, etc.)
			log.Println("Redis GET error for", cacheKey, ":", err)
		}
		// cache miss (or error), proceed to DB
	}

	// Query the database for the mapping
	var originalURL string
	err := db.QueryRow(
		"SELECT original_url FROM shortened_urls WHERE domain = ? AND code = ?",
		domain, code,
	).Scan(&originalURL)
	if err != nil {
		return "", err // could be sql.ErrNoRows or a connection error
	}

	// Populate cache for next time (if cache is enabled)
	if redisClient != nil {
		if err := redisClient.Set(context.Background(), cacheKey, originalURL, 0).Err(); err != nil {
			log.Println("Warning: failed to update Redis cache for", cacheKey, ":", err)
		}
	}
	return originalURL, nil
}

// generateCode produces a random string of the given length using the allowed characters [0-9A-Za-z].
func generateCode(length int) string {
	const charSet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	// Use crypto/rand for secure random bytes
	for i := 0; i < length; i++ {
		// We generate random bytes and use modulo to pick a char from charSet.
		// To reduce modulo bias, we discard values  >= 248 (which would cause bias since 248 mod 62 < 62*4).
		for {
			rb := make([]byte, 1)
			_, err := rand.Read(rb)
			if err != nil {
				// If cryptographic randomness fails, fallback to time-based (this is very unlikely)
				rb[0] = byte(time.Now().UnixNano() % 256)
			}
			// 62 * 4 = 248. If rb[0] < 248, we can use it directly.
			if rb[0] < byte(len(charSet))*4 {
				b[i] = charSet[int(rb[0])%len(charSet)]
				break
			}
			// otherwise, loop again to avoid bias
		}
	}
	return string(b)
}
