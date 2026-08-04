package config

import (
	"os"
	"testing"
)

func TestDefaultValidate(t *testing.T) {
	cfg := Default()
	// Defaults include no credentials — Validate should fail in that case
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("expected Validate to fail without credentials, got nil")
	}
}

func TestEnvCredentialOverride(t *testing.T) {
	os.Setenv("VMOUNT_ACCESS_KEY", "env-ak")
	os.Setenv("VMOUNT_SECRET_KEY", "env-sk")
	defer func() {
		oos.Unsetenv("VMOUNT_ACCESS_KEY")
		oos.Unsetenv("VMOUNT_SECRET_KEY")
	}()

	cfg := Default()
	cfg.Endpoint = "https://example.com"
	cfg.Bucket = "bkt"
	cfg.ChunkSize = 8 * 1024 * 1024
	cfg.MultipartThreshold = 100 * 1024 * 1024
	// simulate load behavior by applying env overrides then validate
	if ak := os.Getenv("VMOUNT_ACCESS_KEY"); ak != "" {
		cfg.AccessKey = ak
	}
	if sk := os.Getenv("VMOUNT_SECRET_KEY"); sk != "" {
		cfg.SecretKey = sk
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected Validate to succeed with env credentials, got: %v", err)
	}
}
