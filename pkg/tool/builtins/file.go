package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/YasserCR/galdor/pkg/tool"
)

// FileReadIn is the input shape of the file_read tool.
type FileReadIn struct {
	Path string `json:"path" jsonschema:"Path to read; resolved against BaseDir when configured"`
}

// FileReadOut returns the contents of the file together with a couple
// of lightweight metadata fields. Truncated reports whether the body
// was capped at MaxBytes.
type FileReadOut struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

// FileReadOptions tunes the file_read tool's safety profile. Defaults
// are conservative; callers explicitly opt into broader behavior.
type FileReadOptions struct {
	// BaseDir confines reads to the given directory (recursive). Any
	// path resolving outside it is rejected with ErrPathEscape. An
	// empty BaseDir disables confinement, which is rarely what you
	// want when an LLM is choosing the path; configure it explicitly.
	BaseDir string

	// MaxBytes caps how many bytes of file content are returned to
	// the LLM. Default 1 MiB. Larger files are truncated and
	// Truncated=true on the output.
	MaxBytes int64

	// FollowSymlinks, when false (default), refuses to follow
	// symlinks once a path has been resolved to its real location.
	// Useful belt-and-braces protection alongside BaseDir.
	FollowSymlinks bool
}

// ErrPathEscape is returned when the requested path resolves outside
// the configured BaseDir.
var ErrPathEscape = errors.New("file_read: path resolves outside BaseDir")

// NewFileReadTool returns a file_read tool configured with opts. The
// tool only reads files; deliberately no write/list/delete variants.
func NewFileReadTool(opts FileReadOptions) (tool.Tool[FileReadIn, FileReadOut], error) {
	cfg := opts.normalize()
	return tool.NewTool("file_read",
		"Read a UTF-8 file. Bound to BaseDir; capped at MaxBytes; symlinks rejected by default.",
		cfg.run)
}

// MustNewFileReadTool is the panicking variant.
func MustNewFileReadTool(opts FileReadOptions) tool.Tool[FileReadIn, FileReadOut] {
	t, err := NewFileReadTool(opts)
	if err != nil {
		panic(err)
	}
	return t
}

type fileReadConfig struct {
	baseDir        string // absolute, cleaned; empty means no confinement
	maxBytes       int64
	followSymlinks bool
}

func (o FileReadOptions) normalize() fileReadConfig {
	c := fileReadConfig{
		maxBytes:       o.MaxBytes,
		followSymlinks: o.FollowSymlinks,
	}
	if o.BaseDir != "" {
		if abs, err := filepath.Abs(o.BaseDir); err == nil {
			c.baseDir = filepath.Clean(abs)
		} else {
			// Falling back to the literal value is acceptable; the
			// escape check below uses filepath.Rel which handles
			// relative inputs.
			c.baseDir = filepath.Clean(o.BaseDir)
		}
	}
	if c.maxBytes <= 0 {
		c.maxBytes = 1 << 20 // 1 MiB
	}
	return c
}

func (c fileReadConfig) run(_ context.Context, in FileReadIn) (FileReadOut, error) {
	if in.Path == "" {
		return FileReadOut{}, fmt.Errorf("file_read: path is required")
	}

	resolved, err := c.resolvePath(in.Path)
	if err != nil {
		return FileReadOut{}, err
	}

	info, err := os.Lstat(resolved)
	if err != nil {
		return FileReadOut{}, fmt.Errorf("file_read: stat: %w", err)
	}
	if !c.followSymlinks && info.Mode()&os.ModeSymlink != 0 {
		return FileReadOut{}, fmt.Errorf("file_read: refusing to follow symlink %q (set FollowSymlinks=true to allow)", resolved)
	}
	if info.IsDir() {
		return FileReadOut{}, fmt.Errorf("file_read: %q is a directory", resolved)
	}

	f, err := os.Open(resolved) // #nosec G304 -- path is BaseDir-confined upstream + symlink-gated, see validatePath above
	if err != nil {
		return FileReadOut{}, fmt.Errorf("file_read: open: %w", err)
	}
	defer func() { _ = f.Close() }()

	limited := io.LimitReader(f, c.maxBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return FileReadOut{}, fmt.Errorf("file_read: read: %w", err)
	}
	truncated := false
	if int64(len(buf)) > c.maxBytes {
		buf = buf[:c.maxBytes]
		truncated = true
	}

	return FileReadOut{
		Path:      resolved,
		Size:      info.Size(),
		Content:   string(buf),
		Truncated: truncated,
	}, nil
}

// resolvePath joins p with BaseDir (when set), cleans it, and verifies
// the result still lives inside BaseDir. Returns an absolute path.
func (c fileReadConfig) resolvePath(p string) (string, error) {
	var resolved string
	if c.baseDir == "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("file_read: resolve path: %w", err)
		}
		resolved = filepath.Clean(abs)
	} else {
		joined := filepath.Join(c.baseDir, p)
		resolved = filepath.Clean(joined)
		rel, err := filepath.Rel(c.baseDir, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%w: %q", ErrPathEscape, p)
		}
	}
	return resolved, nil
}
