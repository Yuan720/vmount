package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/Yuan720/vmount/internal/config"
	"github.com/Yuan720/vmount/internal/fs"
	"github.com/Yuan720/vmount/internal/s3client"
	"github.com/winfsp/cgofuse/fuse"
)

func main() {
	cfgPath := flag.String("config", "vmount.json", "path to config file")
	mountFlag := flag.String("mount", "", "override mount point (e.g. Z:")
	debugFlag := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	// Simple logging setup
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if *debugFlag {
		log.Printf("[DEBUG] starting with config=%s", *cfgPath)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	// allow CLI override of mount point
	if *mountFlag != "" {
		cfg.Mount = *mountFlag
	}

	client, err := s3client.New(cfg.Endpoint, cfg.Bucket, cfg.Prefix,
		cfg.AccessKey, cfg.SecretKey, cfg.UseTLS, 30*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "s3 client:", err)
		os.Exit(1)
	}

	fsys, err := fs.New(client, &cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fs init:", err)
		os.Exit(1)
	}

	host := fuse.NewFileSystemHost(fsys)

	// Ensure Unmount on any exit path (best-effort). If Unmount is called multiple times it's usually fine.
	defer func() {
		// recover from panic to ensure unmount attempt and print stack
		if r := recover(); r != nil {
			log.Printf("[ERROR] panic: %v\n%s", r, debug.Stack())
		}
		if host != nil {
			log.Printf("[INFO] attempting unmount")
			// ignore boolean return here; we're best-effort
			host.Unmount()
		}
	}()

	// Signal handling: trigger unmount on interrupt/terminate.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ERROR] signal handler panic: %v\n%s", r, debug.Stack())
			}
		}()
		<-sig
		log.Printf("[INFO] received shutdown signal, unmounting")
		host.Unmount()
	}()

	log.Printf("[INFO] mounting %s -> s3://%s/%s", cfg.Mount, cfg.Bucket, cfg.Prefix)
	if !host.Mount(cfg.Mount, nil) {
		fmt.Fprintln(os.Stderr, "mount failed")
		os.Exit(1)
	}
	log.Printf("[INFO] unmounted")
}
