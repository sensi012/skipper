package dev

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/unified-platform/container/pkg/build"
	"github.com/unified-platform/container/pkg/config"
	"github.com/unified-platform/container/pkg/runtime"
)

type DevOptions struct {
	WatchDir    string
	Debounce    time.Duration
	Spec        *config.AppSpec
	RuntimeEng  runtime.RuntimeEngine
	Builder     build.BuilderEngine
}

type DevServer struct {
	opts   DevOptions
	mu     sync.Mutex
	status *runtime.WorkloadStatus
}

func NewDevServer(opts DevOptions) *DevServer {
	if opts.WatchDir == "" {
		opts.WatchDir = "."
	}
	if opts.Debounce == 0 {
		opts.Debounce = 400 * time.Millisecond
	}
	return &DevServer{opts: opts}
}

func (d *DevServer) Start(ctx context.Context, out io.Writer) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}
	defer watcher.Close()

	if err := d.addWatchRecursive(watcher, d.opts.WatchDir); err != nil {
		return fmt.Errorf("failed to watch directory: %w", err)
	}

	_, _ = fmt.Fprintf(out, "==> [Skipper Dev] Watching %s for code modifications...\n", d.opts.WatchDir)

	// Initial deployment
	if err := d.rebuildAndDeploy(ctx, out); err != nil {
		_, _ = fmt.Fprintf(out, "!! Initial run failed: %v\n", err)
	}

	var timer *time.Timer
	for {
		select {
		case <-ctx.Done():
			_, _ = fmt.Fprintln(out, "\n==> [Skipper Dev] Shutting down...")
			if d.status != nil {
				_ = d.opts.RuntimeEng.Stop(context.Background(), d.opts.Spec.Name)
			}
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if shouldIgnorePath(event.Name) {
				continue
			}

			// Add dynamically created subdirectories
			if event.Has(fsnotify.Create) {
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					_ = d.addWatchRecursive(watcher, event.Name)
				}
			}

			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) {
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(d.opts.Debounce, func() {
					_, _ = fmt.Fprintf(out, "\n==> [Change Detected: %s] Synchronizing & rebuilding...\n", filepath.Base(event.Name))
					if err := d.rebuildAndDeploy(ctx, out); err != nil {
						_, _ = fmt.Fprintf(out, "!! Rebuild failed: %v\n", err)
					}
				})
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			_, _ = fmt.Fprintf(out, "Watcher error: %v\n", err)
		}
	}
}

func (d *DevServer) rebuildAndDeploy(ctx context.Context, out io.Writer) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 1. Rebuild image
	imageTag, err := d.opts.Builder.Build(ctx, d.opts.Spec, out)
	if err != nil {
		return err
	}
	d.opts.Spec.Image = imageTag

	// 2. Restart/Deploy workload
	status, err := d.opts.RuntimeEng.Deploy(ctx, d.opts.Spec)
	if err != nil {
		return err
	}
	d.status = status
	_, _ = fmt.Fprintf(out, "==> [Skipper Dev] Application '%s' running at status [%s]\n", d.opts.Spec.Name, status.State)
	return nil
}

func (d *DevServer) addWatchRecursive(watcher *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if shouldIgnorePath(path) {
				return filepath.SkipDir
			}
			return watcher.Add(path)
		}
		return nil
	})
}

func shouldIgnorePath(path string) bool {
	clean := filepath.ToSlash(path)
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") && base != "." {
		return true
	}
	ignored := []string{
		"node_modules", "vendor", "dist", "bin", "target", "build",
		".git", ".skipper", "__pycache__", ".idea", ".vscode",
	}
	for _, ign := range ignored {
		if strings.Contains("/"+clean+"/", "/"+ign+"/") || base == ign {
			return true
		}
	}
	return false
}
