package config

import (
	"encoding/json"
	"errors"
	"os"
)

type Config struct {
	Endpoint           string `json:"endpoint"`
	Bucket             string `json:"bucket"`
	Prefix             string `json:"prefix"`
	AccessKey          string `json:"access_key"`
	SecretKey          string `json:"secret_key"`
	Mount              string `json:"mount"`
	CacheDir           string `json:"cache_dir"`
	ReadCacheMB        int64  `json:"read_cache_mb"`
	ListTTLSeconds     int64  `json:"list_ttl_sec"`
	MultipartThreshold int64  `json:"multipart_threshold"`
	ChunkSize          int64  `json:"chunk_size"`
	UseTLS             bool   `json:"use_tls"`
}

func Default() Config {
	return Config{
		Mount:              "Z:",
		CacheDir:           "cache",
		ReadCacheMB:        512,
		ListTTLSeconds:     30,
		MultipartThreshold: 100 * 1024 * 1024,
		ChunkSize:          8 * 1024 * 1024,
	}
}

// Validate checks config value boundaries and relations.
// Returns a descriptive error if configuration is invalid.
func (c *Config) Validate() error {
	if c.Endpoint == "" {
		return errors.New("endpoint is required")
	}
	if c.Bucket == "" {
		return errors.New("bucket is required")
	}
	// Credentials validation: at least one method must provide credentials, but allow external credential chain
	if c.AccessKey == "" || c.SecretKey == "" {
		return errors.New("access_key and secret_key are required (or provide credentials via environment or shared credentials)")
	}
	if c.ChunkSize < 5*1024*1024 {
		return errors.New("chunk_size must be at least 5 MiB (S3 multipart minimum)")
	}
	if c.MultipartThreshold < c.ChunkSize {
		return errors.New("multipart_threshold must be >= chunk_size")
	}
	if c.ReadCacheMB < 0 {
		return errors.New("read_cache_mb must be >= 0")
	}
	return nil
}

// Load reads config from JSON file at path, applies defaults and environment overrides,
// then validates configuration.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}

	// Allow credentials to be provided via environment variables to avoid storing secrets in file.
	// Priority: VMOUNT_ACCESS_KEY / VMOUNT_SECRET_KEY -> AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY
	if ak := os.Getenv("VMOUNT_ACCESS_KEY"); ak != "" {
		cfg.AccessKey = ak
	} else if ak := os.Getenv("AWS_ACCESS_KEY_ID"); ak != "" && cfg.AccessKey == "" {
		cfg.AccessKey = ak
	}
	if sk := os.Getenv("VMOUNT_SECRET_KEY"); sk != "" {
		cfg.SecretKey = sk
	} else if sk := os.Getenv("AWS_SECRET_ACCESS_KEY"); sk != "" && cfg.SecretKey == "" {
		cfg.SecretKey = sk
	}

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}
