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
	"github.com/sensi012/skipper/pkg/build"
	"github.com/sensi012/skipper/pkg/config"
	"github.com/sensi012/skipper/pkg/runtime"
)

type DevOptions struct {
	WatchDir   string
	Debounce   time.Duration
	Spec       *config.AppSpec
	RuntimeEng runtime.RuntimeEngine
	Builder    build.BuilderEngine
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
	if out == nil {
		out = io.Discard
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	if err := d.addWatchRecursive(watcher, d.opts.WatchDir); err != nil {
		return fmt.Errorf("failed to watch directory: %w", err)
	}

	_, _ = fmt.Fprintf(out, "==> [Skipper Dev] Watching %s for code modifications...\n", d.opts.WatchDir)

	// Initial deployment
	if err := d.rebuildAndDeploy(ctx, out); err != nil {
		_, _ = fmt.Fprintf(out, "!! Initial run failed: %v\n", err)
	}

	var timer *time.Timer
	var timerCh <-chan time.Time
	var pendingFile string

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			_, _ = fmt.Fprintln(out, "\n==> [Skipper Dev] Shutting down...")
			d.mu.Lock()
			hasStatus := d.status != nil
			d.mu.Unlock()
			if hasStatus {
				_ = d.opts.RuntimeEng.Stop(context.Background(), d.opts.Spec.Name)
			}
			return nil

		case <-timerCh:
			timerCh = nil
			_, _ = fmt.Fprintf(out, "\n==> [Change Detected: %s] Synchronizing & rebuilding...\n", pendingFile)
			if err := d.rebuildAndDeploy(ctx, out); err != nil {
				_, _ = fmt.Fprintf(out, "!! Rebuild failed: %v\n", err)
			}

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
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
				}
				pendingFile = filepath.Base(event.Name)
				timer = time.NewTimer(d.opts.Debounce)
				timerCh = timer.C
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
			if path == root {
				return err
			}
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
