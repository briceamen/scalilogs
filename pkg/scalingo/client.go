package scalingo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	scalingo "github.com/Scalingo/go-scalingo/v7"
	"github.com/Scalingo/go-utils/errors/v2"
	"github.com/briceamen/scalilogs/internal/config"
	"github.com/briceamen/scalilogs/internal/status"
)

const (
	EnvProduction = "production"
	EnvStaging    = "staging"
	EnvDev        = "dev"
)

type LogsArchiveItem = scalingo.LogsArchiveItem
type LogsArchivesResponse = scalingo.LogsArchivesResponse

type Client struct {
	Scalingo *scalingo.Client
	Env      string
	Region   string
	cache    *config.ClientCache
}

// NewScalingoClient creates a new client with environment and region information
func NewScalingoClient(ctx context.Context, env, region string, statusCh chan<- status.Message) (*Client, error) {
	// Initialize the cache
	cache := config.NewClientCache()

	// Create the Scalingo client wrapper
	sc := &Client{
		Env:    env,
		Region: region,
		cache:  cache,
	}

	// Initialize the underlying client
	client, err := sc.initClient(ctx, statusCh)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "initialize scalingo client")
	}

	sc.Scalingo = client
	return sc, nil
}

// NewScalingoClientWithRegions creates a new client with environment, region, and preloaded regions
func NewScalingoClientWithRegions(ctx context.Context, env, region string, statusCh chan<- status.Message, preloadedRegions map[string][]config.Region) (*Client, error) {
	// Initialize the cache
	cache := config.NewClientCache()

	// Pre-populate the cache with the preloaded regions if available
	if preloadedRegions != nil {
		if regions, ok := preloadedRegions[env]; ok && len(regions) > 0 {
			cache.SetRegions(env, regions)
		} else {
			// Try to load regions from cache - we need this regardless of environment
			regions, err := config.LoadRegionsFromCache(ctx, env, "")
			if err == nil {
				cache.SetRegions(env, regions)
			} else if env == EnvStaging || env == EnvDev {
				// For staging and dev, we must have regions from cache files
				return nil, errors.Wrap(ctx, err, "load regions from cache for "+env)
			}
			// For production, it will fall back to hardcoded values in initClient
		}
	}

	// Create the Scalingo client wrapper
	sc := &Client{
		Env:    env,
		Region: region,
		cache:  cache,
	}

	// Initialize the underlying client
	client, err := sc.initClient(ctx, statusCh)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "initialize scalingo client")
	}

	sc.Scalingo = client
	return sc, nil
}

// GetRegions fetches available regions
func (c *Client) GetRegions(ctx context.Context) ([]config.Region, error) {
	// Try to get from cache first
	regions, ok := c.cache.GetRegions(c.Env)
	if ok {
		return regions, nil
	}

	// Use the client to fetch regions
	scRegions, err := c.Scalingo.RegionsList(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "list regions")
	}

	// Convert from scalingo.Region to our Region type
	regions = make([]config.Region, len(scRegions))
	for i, r := range scRegions {
		regions[i] = config.Region{
			Name:        r.Name,
			DisplayName: r.DisplayName,
			API:         r.API,
		}
	}

	// Store in cache for future use
	c.cache.SetRegions(c.Env, regions)

	return regions, nil
}

// CheckAppExists checks if an app exists
func (c *Client) CheckAppExists(ctx context.Context, appName string) error {
	// Use the client's AppsShow method directly to check if app exists
	_, err := c.Scalingo.AppsShow(ctx, appName)
	if err != nil {
		// Check for app not found errors
		return errors.Wrap(ctx, err, "find app")
	}

	return nil
}

// DownloadLogsArchive downloads and writes the archive content to the provided writer
func (c *Client) DownloadLogsArchive(ctx context.Context, archiveURL string, output io.Writer) error {
	// Create a new request
	req, err := http.NewRequestWithContext(ctx, "GET", archiveURL, nil)
	if err != nil {
		return errors.Wrap(ctx, err, "create request for logs archive")
	}

	// Use standard http client
	httpClient := &http.Client{}

	// First make a HEAD request to get the content length
	headReq, err := http.NewRequestWithContext(ctx, "HEAD", archiveURL, nil)
	if err != nil {
		return errors.Wrap(ctx, err, "create HEAD request for logs archive")
	}

	headRes, err := httpClient.Do(headReq)
	if err != nil {
		return errors.Wrap(ctx, err, "get archive size")
	}
	headRes.Body.Close()

	// Now make the GET request
	res, err := httpClient.Do(req)
	if err != nil {
		return errors.Wrap(ctx, err, "download logs archive")
	}
	defer res.Body.Close()

	// Check for successful response
	if res.StatusCode != http.StatusOK {
		return errors.New(ctx, fmt.Sprintf("failed to download archive: HTTP %d", res.StatusCode))
	}

	// Copy the archive to the output
	_, err = io.Copy(output, res.Body)
	if err != nil {
		return errors.Wrap(ctx, err, "write logs archive to output")
	}

	return nil
}

// GetRegionsForEnv fetches available regions from the Scalingo API for the given environment
func GetRegionsForEnv(ctx context.Context, env string, statusCh chan<- status.Message, preloadedRegions map[string][]config.Region) ([]config.Region, error) {
	// Try to use preloaded regions first if available
	if preloadedRegions != nil {
		if regions, ok := preloadedRegions[env]; ok && len(regions) > 0 {
			return regions, nil
		}
	}

	// Try to load regions from cache if preloaded regions not available
	regions, err := config.LoadRegionsFromCache(ctx, env, "")
	if err == nil {
		return regions, nil
	}

	// If loading from cache failed, create a client and fetch
	client, err := NewScalingoClient(ctx, env, "", statusCh)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "create scalingo client")
	}

	return client.GetRegions(ctx)
}

// initClient creates a new Scalingo API client using the provided configuration
func (c *Client) initClient(ctx context.Context, statusCh chan<- status.Message) (*scalingo.Client, error) {
	// Try to load auth data
	if err := c.cache.LoadAuthData(ctx); err != nil {
		return nil, errors.Wrap(ctx, err, "load auth data")
	}

	// Load auth data
	authData, ok := c.cache.GetAuthData()
	if !ok {
		return nil, errors.New(ctx, "no auth data available - check ~/.config/scalingo/auth")
	}

	// Determine auth host based on environment - we'll extract API endpoint from regions later
	var authHost string
	// Set protocol based on environment
	protocol := "https://"

	switch c.Env {
	case EnvProduction:
		authHost = "auth.scalingo.com"
	case EnvStaging:
		// For staging, find auth by deduction (not prod or 172.X as it is local)
		for host := range authData["auth_config_data"].(map[string]interface{}) {
			// Not production and not local/dev (which starts with 172. or 127. or is localhost)
			if host != "auth.scalingo.com" &&
				!strings.HasPrefix(host, "172.") &&
				!strings.HasPrefix(host, "127.") &&
				host != "localhost" {
				authHost = host
				break
			}
		}
		// If not found, return error
		if authHost == "" {
			return nil, errors.Wrap(ctx, errors.New(ctx, "no suitable auth host found"), "find staging auth host")
		}
	case EnvDev:
		// For dev, find the local development auth host
		for host := range authData["auth_config_data"].(map[string]interface{}) {
			if strings.HasPrefix(host, "172.") || strings.HasPrefix(host, "127.") || host == "localhost" {
				authHost = host
				protocol = "http://" // Dev uses HTTP
				break
			}
		}
		// If not found, return error
		if authHost == "" {
			return nil, errors.New(ctx, "auth host for dev not found in auth data")
		}
	default:
		return nil, errors.New(ctx, fmt.Sprintf("unknown environment: %s", c.Env))
	}

	// Try to get token from auth data
	token := ""
	if authConfig, ok := authData["auth_config_data"].(map[string]interface{}); ok {
		if envAuth, ok := authConfig[authHost].(map[string]interface{}); ok {
			if tokens, ok := envAuth["tokens"].(map[string]interface{}); ok {
				if t, ok := tokens["token"].(string); ok {
					token = t
					// Store in cache
					c.cache.SetToken(c.Env, token)
				}
			}
		}
	}

	if token == "" {
		return nil, errors.New(ctx, fmt.Sprintf("token not found for environment '%s' and auth host '%s' - check ~/.config/scalingo/auth", c.Env, authHost))
	}

	// For all environments, we need to handle regions
	// First try to get regions from the client cache
	regions, ok := c.cache.GetRegions(c.Env)
	if !ok || len(regions) == 0 {
		// If not in cache, try to load from the file cache
		var cacheError error
		regions, cacheError = config.LoadRegionsFromCache(ctx, c.Env, "")
		if cacheError == nil {
			// Store in client cache for future use
			c.cache.SetRegions(c.Env, regions)
		} else if c.Env == EnvStaging || c.Env == EnvDev {
			// For staging and dev, we must have regions from cache files
			return nil, errors.Wrap(ctx, cacheError, fmt.Sprintf("load regions for %s", c.Env))
		}
		// Production has hardcoded fallbacks in LoadRegionsFromCache
	}

	// If region is empty, use the default from cache
	if c.Region == "" {
		if len(regions) == 0 {
			if c.Env == EnvProduction {
				return nil, errors.New(ctx, "no regions found for production - check ~/.cache/scalingo/regions.json")
			} else {
				// For staging and dev, we need regions from the cache files
				if c.Env == EnvStaging {
					return nil, errors.New(ctx, "no regions found for staging - check ~/.cache/scalingo/regions_staging.json")
				} else {
					return nil, errors.New(ctx, "no regions found for dev - check ~/.cache/scalingo/regions_local.json")
				}
			}
		} else {
			// Find the default region
			defaultFound := false
			for _, r := range regions {
				if r.Default {
					c.Region = r.Name
					defaultFound = true
					break
				}
			}

			// If no default is marked, use the first one
			if !defaultFound && len(regions) > 0 {
				c.Region = regions[0].Name
			}
		}
	}

	// Validate region compatibility with environment
	if c.Region != "" {
		// Check if region is in the environment's region list
		regionFound := false
		for _, r := range regions {
			if r.Name == c.Region {
				regionFound = true
				break
			}
		}

		if !regionFound && len(regions) > 0 {
			// Environment mismatch - detected when region not found in environment's region list
			return nil, errors.Wrap(ctx, errors.New(ctx,
				fmt.Sprintf("region '%s' not compatible with environment '%s'. Check region name or use the correct environment flag",
					c.Region, c.Env)),
				"validate region compatibility")
		}
	}

	// At this point we should have a region - set the environment variables
	var msg string
	if c.Region == "" || len(regions) == 0 {
		msg = fmt.Sprintf("Using default region: %s", c.Region)
	} else {
		msg = fmt.Sprintf("Using region: %s", c.Region)
	}
	if statusCh != nil {
		status.Update(statusCh, msg)
	} else {
		fmt.Println(msg)
	}
	os.Setenv("SCALINGO_REGION", c.Region)

	// Try to find the API endpoint for this region
	apiEndpoint := ""
	apiEndpointFound := false
	for _, r := range regions {
		if r.Name == c.Region {
			apiEndpoint = r.API
			apiEndpointFound = true
			break
		}
	}

	// If we couldn't find it, handle based on environment
	if !apiEndpointFound {
		err := errors.New(ctx, fmt.Sprintf("API endpoint not found for region: %s", c.Region))
		// Report error via status channel
		if statusCh != nil {
			status.ReportError(ctx, statusCh, err)
		} else {
			// Just print to stderr if status channel is not available
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		return nil, err
	}

	// Set the API URL environment variable
	os.Setenv("SCALINGO_API_URL", apiEndpoint)
	authURL := protocol + authHost
	os.Setenv("SCALINGO_AUTH_URL", authURL)

	// Create client with configuration
	clientConfig := scalingo.ClientConfig{
		APIToken:     token,
		APIEndpoint:  apiEndpoint,
		AuthEndpoint: authURL,
	}

	client, err := scalingo.New(ctx, clientConfig)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "initialize scalingo client")
	}

	return client, nil
}

// GetRegions fetches available regions from the Scalingo API
func GetRegions(ctx context.Context, env string) ([]config.Region, error) {
	return GetRegionsForEnv(ctx, env, nil, nil)
}

// NewClient creates a new Scalingo API client for the given environment and region
func NewClient(ctx context.Context, env string, region string) (*scalingo.Client, error) {
	c, err := NewScalingoClient(ctx, env, region, nil)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "create scalingo client")
	}
	return c.Scalingo, nil
}

// FetchLogs fetches logs from the Scalingo API and writes them to the provided writer
func FetchLogs(ctx context.Context, client *scalingo.Client, appName string, lineCount int, output io.Writer) error {
	// Get the authenticated logs URL first
	logsURLRes, err := client.LogsURL(ctx, appName)
	if err != nil {
		return errors.Wrap(ctx, err, "get logs URL")
	}
	defer logsURLRes.Body.Close()

	// Read the entire response body
	responseBody, err := io.ReadAll(logsURLRes.Body)
	if err != nil {
		return errors.Wrap(ctx, err, "read logs URL response")
	}

	// Parse response to get the logs URL
	var logsURLData struct {
		LogsURL string `json:"logs_url"`
	}
	if err = json.Unmarshal(responseBody, &logsURLData); err != nil {
		return errors.Wrap(ctx, err, "parse logs URL response")
	}

	logsURL := logsURLData.LogsURL
	if logsURL == "" {
		return errors.Wrap(ctx, fmt.Errorf("API returned: %s", string(responseBody)), "get logs URL")
	}

	// Use the API directly with the authenticated URL
	res, err := client.Logs(ctx, logsURL, lineCount, "")
	if err != nil {
		return errors.Wrap(ctx, err, "fetch logs")
	}
	defer res.Body.Close()

	// Write the logs to the output
	_, err = io.Copy(output, res.Body)
	if err != nil {
		return errors.Wrap(ctx, err, "copy logs to output")
	}

	return nil
}

// DownloadLogsArchive downloads and writes the archive content to the provided writer
func DownloadLogsArchive(ctx context.Context, client *scalingo.Client, archiveURL string, output io.Writer) error {
	// Create a ScalingoClient to handle the download
	sc := &Client{
		Scalingo: client,
		Env:      "",
		Region:   os.Getenv("SCALINGO_REGION"),
		cache:    config.NewClientCache(),
	}

	// Use the ScalingoClient implementation
	return sc.DownloadLogsArchive(ctx, archiveURL, output)
}

// ArchiveInfo contains information about an archive for download
type ArchiveInfo struct {
	FileName string
	URL      string
	Size     int64
}

// GetArchiveInfo returns information about an archive, including its size
func GetArchiveInfo(ctx context.Context, archiveURL, fileName string) (*ArchiveInfo, error) {
	// Make a HEAD request to get the content length
	httpClient := &http.Client{}
	headReq, err := http.NewRequestWithContext(ctx, "HEAD", archiveURL, nil)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "create HEAD request for logs archive")
	}

	headRes, err := httpClient.Do(headReq)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "get archive size")
	}
	headRes.Body.Close()

	// Get the content length
	contentLength := headRes.ContentLength
	if contentLength <= 0 {
		// If we couldn't determine size, use a reasonable default
		contentLength = 10 * 1024 * 1024 // 10MB default
	}

	return &ArchiveInfo{
		FileName: fileName,
		URL:      archiveURL,
		Size:     contentLength,
	}, nil
}

// DownloadArchiveToWriter downloads an archive to the provided writer
func DownloadArchiveToWriter(ctx context.Context, archiveURL string, output io.Writer) error {
	// Create a new request
	req, err := http.NewRequestWithContext(ctx, "GET", archiveURL, nil)
	if err != nil {
		return errors.Wrap(ctx, err, "create request for logs archive")
	}

	// Use standard http client
	httpClient := &http.Client{}
	res, err := httpClient.Do(req)
	if err != nil {
		return errors.Wrap(ctx, err, "download logs archive")
	}
	defer res.Body.Close()

	// Check for successful response
	if res.StatusCode != http.StatusOK {
		return errors.New(ctx, fmt.Sprintf("failed to download archive: HTTP %d", res.StatusCode))
	}

	// Copy the archive to the output
	_, err = io.Copy(output, res.Body)
	if err != nil {
		return errors.Wrap(ctx, err, "write logs archive to output")
	}

	return nil
}

// CheckAppExists checks if an app exists in the given environment and region
func CheckAppExists(ctx context.Context, appName, env, region string) error {
	// Create a ScalingoClient
	sc, err := NewScalingoClient(ctx, env, region, nil)
	if err != nil {
		return errors.Wrap(ctx, err, "create scalingo client")
	}

	// Use the ScalingoClient implementation
	return sc.CheckAppExists(ctx, appName)
}
