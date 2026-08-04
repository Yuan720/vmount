package config

import (
	"encoding/json"
	"errors"
	"os"
)

type Config struct {
	Endpoint          string   `json:"endpoint"`
	Bucket            string   `json:"bucket"`
	Prefix            string   `json:"prefix"`
	AccessKey         string   `json:"access_key"`
	SecretKey         string   `json:"secret_key"`
	Mount             string   `json:"mount"`
	CacheDir          string   `json:"cache_dir"`
	ReadCacheMB       int64    `json:"read_cache_mb"`
	ListTTLSeconds    int64    `json:"list_ttl_sec"`
	MultipartThreshold int64   `json:"multipart_threshold"`
	ChunkSize         int64    `json:"chunk_size"`
	UseTLS            bool     `json:"use_tls"`
	ExcludeSuffixes   []string `json:"exclude_suffixes"`
}

func Default() Config {
	return Config{
		Mount:              "Z:",
		CacheDir:           "cache",
		ReadCacheMB:        512,
		ListTTLSeconds:     30,
		MultipartThreshold: 100 * 1024 * 1024,
		ChunkSize:          8 * 1024 * 1024,
		ExcludeSuffixes: []string{
			".crdownload", ".part", ".partial", ".download", ".tmp", ".temp", ".aria2",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.Endpoint == "" {
		return cfg, errors.New("endpoint is required")
	}
	if cfg.Bucket == "" {
		return cfg, errors.New("bucket is required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return cfg, errors.New("access_key and secret_key are required")
	}
	return cfg, nil
}
