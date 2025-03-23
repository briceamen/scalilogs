package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Scalingo/go-utils/errors/v2"
)

// RegionCache represents a cache of regions data
type RegionCache struct {
	Regions  []Region  `json:"regions"`
	ExpireAt time.Time `json:"expire_at"`
}

// Region represents a Scalingo region
type Region struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	API         string `json:"api"`
	Default     bool   `json:"default,omitempty"`
}

// ClientCache holds all caching-related data for API clients
type ClientCache struct {
	// Cache for environment -> regions mapping
	regionsCache map[string][]Region
	// Cache for environment -> endpoints mapping
	endpointsCache map[string]map[string]string // env -> {api, logs, auth}
	// Token cache to avoid re-exchanging tokens
	tokenCache map[string]string // env -> token
	// Auth file data cache
	authDataCache map[string]interface{}
	// Mutex for thread-safe operations
	mutex sync.RWMutex
}

// NewClientCache creates a new client cache with initialized maps
func NewClientCache() *ClientCache {
	return &ClientCache{
		regionsCache:   make(map[string][]Region),
		endpointsCache: make(map[string]map[string]string),
		tokenCache:     make(map[string]string),
		authDataCache:  make(map[string]interface{}),
	}
}

// GetRegions gets regions from cache for the given environment
func (c *ClientCache) GetRegions(env string) ([]Region, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	regions, ok := c.regionsCache[env]
	return regions, ok
}

// SetRegions sets regions in cache for the given environment
func (c *ClientCache) SetRegions(env string, regions []Region) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.regionsCache[env] = regions
}

// GetEndpoints gets endpoints from cache for the given environment
func (c *ClientCache) GetEndpoints(env string) (map[string]string, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	endpoints, ok := c.endpointsCache[env]
	return endpoints, ok
}

// SetEndpoints sets endpoints in cache for the given environment
func (c *ClientCache) SetEndpoints(env string, endpoints map[string]string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.endpointsCache[env] = endpoints
}

// GetRegionEndpoints gets endpoints for a specific environment-region combination
func (c *ClientCache) GetRegionEndpoints(env, region string) (map[string]string, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	regionKey := env + "-" + region
	endpoints, ok := c.endpointsCache[regionKey]
	return endpoints, ok
}

// SetRegionEndpoints sets endpoints for a specific environment-region combination
func (c *ClientCache) SetRegionEndpoints(env, region string, endpoints map[string]string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	regionKey := env + "-" + region
	c.endpointsCache[regionKey] = endpoints
}

// GetToken gets token from cache for the given environment
func (c *ClientCache) GetToken(env string) (string, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	token, ok := c.tokenCache[env]
	return token, ok
}

// SetToken sets token in cache for the given environment
func (c *ClientCache) SetToken(env, token string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.tokenCache[env] = token
}

// GetAuthData gets auth data from cache
func (c *ClientCache) GetAuthData() (map[string]interface{}, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if c.authDataCache == nil {
		return nil, false
	}
	return c.authDataCache, true
}

// SetAuthData sets auth data in cache
func (c *ClientCache) SetAuthData(data map[string]interface{}) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.authDataCache = data
}

// LoadAuthData loads auth data from file system
func (c *ClientCache) LoadAuthData(ctx context.Context) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return errors.Wrap(ctx, err, "get user home directory")
	}

	authFilePath := filepath.Join(homeDir, ".config", "scalingo", "auth")
	data, err := os.ReadFile(authFilePath)
	if err != nil {
		return errors.Wrap(ctx, err, "read auth file")
	}

	var authData map[string]interface{}
	if err := json.Unmarshal(data, &authData); err != nil {
		return errors.Wrap(ctx, err, "parse auth file")
	}

	c.SetAuthData(authData)
	return nil
}

// LoadRegionsFromCache attempts to load regions from cached file
func LoadRegionsFromCache(ctx context.Context, env, regionName string) ([]Region, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.Wrap(ctx, err, "get user home directory")
	}

	// Determine the appropriate cache file path based on environment
	var cachePaths []string

	// First try environment-specific cache files
	switch env {
	case "production":
		cachePaths = append(cachePaths, filepath.Join(homeDir, ".cache", "scalingo", "regions.json"))
	case "staging":
		cachePaths = append(cachePaths, filepath.Join(homeDir, ".cache", "scalingo", "regions_staging.json"))
	case "dev":
		cachePaths = append(cachePaths, filepath.Join(homeDir, ".cache", "scalingo", "regions_local.json"))
	}

	// Always add the generic regions.json as fallback
	cachePaths = append(cachePaths, filepath.Join(homeDir, ".cache", "scalingo", "regions.json"))

	// Try each cache path in order
	var lastErr error
	for _, path := range cachePaths {
		// Check if file exists
		if _, err := os.Stat(path); os.IsNotExist(err) {
			lastErr = err
			continue
		}

		// Read the cache file
		data, err := os.ReadFile(path)
		if err != nil {
			lastErr = err
			continue
		}

		// Parse the JSON
		var regionsResponse RegionCache
		if err := json.Unmarshal(data, &regionsResponse); err != nil {
			lastErr = errors.Wrap(ctx, err, "parse regions cache file")
			continue
		}

		return regionsResponse.Regions, nil
	}

	// No fallback for staging or dev - must load from cache files
	return nil, errors.Wrap(ctx, lastErr, "read regions cache file")
}
