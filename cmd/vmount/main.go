package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Yuan720/vmount/internal/config"
	"github.com/Yuan720/vmount/internal/fs"
	"github.com/Yuan720/vmount/internal/s3client"
	"github.com/winfsp/cgofuse/fuse"
)

func main() {
	cfgPath := flag.String("config", "vmount.json", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	client, err := s3client.New(cfg.Endpoint, cfg.Bucket, cfg.Prefix,
		cfg.AccessKey, cfg.SecretKey, cfg.UseTLS, 30*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "s3 client:", err)
		os.Exit(1)
	}

	fsys, err := fs.New(client, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fs init:", err)
		os.Exit(1)
	}

	host := fuse.NewFileSystemHost(fsys)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		host.Unmount()
	}()

	fmt.Printf("mounting %s -> s3://%s/%s\n", cfg.Mount, cfg.Bucket, cfg.Prefix)
	if !host.Mount(cfg.Mount, nil) {
		fmt.Fprintln(os.Stderr, "mount failed")
		os.Exit(1)
	}
	fmt.Println("unmounted")
}
