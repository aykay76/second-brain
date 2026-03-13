package filesystem

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	scanner *Scanner
	watcher *fsnotify.Watcher
}

func NewWatcher(scanner *Scanner) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{scanner: scanner, watcher: w}, nil
}

// Start begins watching all configured directories for file changes.
// Blocks until the context is cancelled.
func (w *Watcher) Start(ctx context.Context) error {
	for _, root := range w.scanner.paths {
		expanded := expandHome(root)
		if err := w.addRecursive(expanded); err != nil {
			slog.Warn("failed to watch directory", "path", expanded, "error", err)
		}
	}

	slog.Info("filesystem watcher started", "directories", len(w.scanner.paths))

	for {
		select {
		case <-ctx.Done():
			return w.watcher.Close()

		case event, ok := <-w.watcher.Events:
			if !ok {
				return nil
			}
			w.handleEvent(ctx, event)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return nil
			}
			slog.Error("watcher error", "error", err)
		}
	}
}

func (w *Watcher) handleEvent(ctx context.Context, event fsnotify.Event) {
	if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
		return
	}

	ext := strings.ToLower(filepath.Ext(event.Name))
	if !w.scanner.extensions[ext] {
		return
	}

	slog.Info("file changed, processing", "path", event.Name, "op", event.Op.String())
	ingested, err := w.scanner.ProcessFile(ctx, event.Name)
	if err != nil {
		slog.Error("watcher failed to process file", "path", event.Name, "error", err)
		return
	}
	if ingested {
		slog.Info("watcher ingested file", "path", event.Name)
	}
}

func (w *Watcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if err := w.watcher.Add(path); err != nil {
				slog.Warn("failed to watch subdirectory", "path", path, "error", err)
			}
		}
		return nil
	})
}
