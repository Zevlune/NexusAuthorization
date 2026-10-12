// Package logging implements the standard Luck operational log format.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Module         = "Module"
	Category       = "Category"
	Subcategory    = "Subcategory"
	RequestTraceID = "RequestTraceId"
	Filter1        = "Filter1"
	Filter2        = "Filter2"
)

type Options struct {
	Module       string
	MinimumLevel string
	FilePath     string
}

// New creates a logger that writes the standard format to stdout and,
// when configured, appends the same records to a file.
func New(options Options) (*slog.Logger, io.Closer, error) {
	writers := []io.Writer{os.Stdout}
	closer := io.Closer(nopCloser{})
	if path := strings.TrimSpace(options.FilePath); path != "" {
		directory := filepath.Dir(path)
		if directory != "." {
			if err := os.MkdirAll(directory, 0o755); err != nil {
				return nil, nil, fmt.Errorf("create log directory: %w", err)
			}
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file: %w", err)
		}
		writers = append(writers, file)
		closer = file
	}

	module := strings.TrimSpace(options.Module)
	if module == "" {
		module = "permission-center-go"
	}
	handler := &Handler{
		output: &lockedWriter{writer: io.MultiWriter(writers...)},
		module: module,
		level:  parseLevel(options.MinimumLevel),
	}
	return slog.New(handler), closer, nil
}

// NewText creates a standard-format logger for a caller-provided writer.
func NewText(output io.Writer, module, minimumLevel string) *slog.Logger {
	module = strings.TrimSpace(module)
	if module == "" {
		module = "unknown"
	}
	return slog.New(&Handler{
		output: &lockedWriter{writer: output},
		module: module,
		level:  parseLevel(minimumLevel),
	})
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) write(value string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := io.WriteString(w.writer, value)
	return err
}

// Handler renders slog records using the canonical Luck template.
type Handler struct {
	output *lockedWriter
	module string
	level  slog.Level
	attrs  []slog.Attr
	groups []string
}

func (h *Handler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }

func (h *Handler) Handle(_ context.Context, record slog.Record) error {
	fields := map[string]string{
		Module: h.module, Category: "", Subcategory: "", RequestTraceID: "", Filter1: "", Filter2: "",
	}
	extras := make([]string, 0, len(h.attrs)+record.NumAttrs())
	for _, attr := range h.attrs {
		appendAttribute(fields, &extras, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		appendAttribute(fields, &extras, h.groups, attr)
		return true
	})

	timestamp := record.Time
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	message := sanitize(record.Message)
	if len(extras) > 0 {
		message += " " + strings.Join(extras, " ")
	}
	line := fmt.Sprintf("[%s][%s][%s][%s][%s][%s][%s][%s][%s]\n",
		timestamp.Format("2006-01-02 15:04:05.000"), levelName(record.Level),
		sanitize(fields[Module]), sanitize(fields[Category]), sanitize(fields[Subcategory]),
		sanitize(fields[RequestTraceID]), sanitize(fields[Filter1]), sanitize(fields[Filter2]), message)
	return h.output.write(line)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

func (h *Handler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}

func appendAttribute(fields map[string]string, extras *[]string, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		nextGroups := append(append([]string(nil), groups...), attr.Key)
		for _, child := range attr.Value.Group() {
			appendAttribute(fields, extras, nextGroups, child)
		}
		return
	}
	value := valueString(attr.Value)
	if len(groups) == 0 {
		if _, ok := fields[attr.Key]; ok {
			fields[attr.Key] = value
			return
		}
	}
	keyParts := append(append([]string(nil), groups...), attr.Key)
	*extras = append(*extras, strings.Join(keyParts, ".")+"="+quoteValue(value))
}

func valueString(value slog.Value) string {
	if value.Kind() == slog.KindTime {
		return value.Time().Format(time.RFC3339Nano)
	}
	if value.Kind() == slog.KindDuration {
		return value.Duration().String()
	}
	return fmt.Sprint(value.Any())
}

func quoteValue(value string) string {
	value = sanitize(value)
	if strings.ContainsAny(value, " \t=\"") {
		return strconv.Quote(value)
	}
	return value
}

func sanitize(value string) string {
	value = strings.ReplaceAll(value, "\r", `\r`)
	return strings.ReplaceAll(value, "\n", `\n`)
}

func parseLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug", "dbg":
		return slog.LevelDebug
	case "warning", "warn", "wrn":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func levelName(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return "DBG"
	case level < slog.LevelWarn:
		return "INF"
	case level < slog.LevelError:
		return "WRN"
	default:
		return "ERR"
	}
}
