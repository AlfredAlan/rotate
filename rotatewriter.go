package rotate

import (
	"compress/gzip"
	"errors"
	"fmt"
	"go.uber.org/atomic"
	"go.uber.org/multierr"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	ErrFileNameIsEmpty = errors.New("error: file name is empty")
	ErrLogFileClosed   = errors.New("error: log file closed")
	ErrDataOversize    = errors.New("error: model size exceeds maximum")
)

type (
	RotateWriter struct {
		filename   string // log path and file name
		prefix     string // log prefix include base path
		ext        string // log extension
		backupName string // log backup name
		size       int64  // log current size
		opt        *rotateOption
		err        error
		postCh     chan string
		postDone   chan struct{}
		fp         *os.File
		mu         sync.Mutex
		closeOnce  sync.Once
		done       atomic.Bool
	}

	rotateOption struct {
		delimiter  string
		timeFormat string
		gzip       bool
		localTime  bool
		maxDays    int64
		maxSize    int64
		maxBackups int64
	}
	RotateOption func(*rotateOption)
)

var _ io.WriteCloser = (*RotateWriter)(nil)

// NewRotateWriter rotate
func NewRotateWriter(filename string, options ...RotateOption) (*RotateWriter, error) {
	if len(filename) == 0 {
		return nil, ErrFileNameIsEmpty
	}
	r := &RotateWriter{
		filename: filename,
		postCh:   make(chan string, 100), // no block channel
		postDone: make(chan struct{}),
	}
	opt := &rotateOption{
		maxDays:    defaultMaxDays,
		maxSize:    defaultMaxSize * megabyte,
		delimiter:  defaultDelimiter,
		timeFormat: defaultTimeFormat,
		maxBackups: defaultMaxBackups,
		localTime:  true,
		gzip:       false,
	}
	for _, fn := range options {
		fn(opt)
	}
	r.opt = opt
	if err := r.init(); err != nil {
		return nil, err
	}
	// handle other thing like compress and remove outdated files
	go r.afterRotate()
	return r, nil
}

// WithGzip
func WithGzip(gzip bool) RotateOption {
	return func(o *rotateOption) {
		o.gzip = gzip
	}
}

// WithMaxDays
func WithMaxDays(days int64) RotateOption {
	return func(o *rotateOption) {
		o.maxDays = days
	}
}

// WithMaxSize
func WithMaxSize(max int64) RotateOption {
	return func(o *rotateOption) {
		if max <= 0 {
			o.maxSize = defaultMaxSize * megabyte
			return
		}
		o.maxSize = max * megabyte
	}
}

// WithLocalTime
func WithLocalTime(local bool) RotateOption {
	return func(o *rotateOption) {
		o.localTime = local
	}
}

// WithMaxBackups
func WithMaxBackups(max int64) RotateOption {
	return func(o *rotateOption) {
		o.maxBackups = max
	}
}

// WithDelimiter
func WithDelimiter(s string) RotateOption {
	return func(o *rotateOption) {
		if len(s) == 0 {
			o.delimiter = defaultDelimiter
			return
		}
		o.delimiter = s
	}
}

// WithTimeFormat
func WithTimeFormat(format string) RotateOption {
	return func(o *rotateOption) {
		if len(format) == 0 {
			o.timeFormat = defaultTimeFormat
			return
		}
		o.timeFormat = format
	}
}

// afterRotate
func (r *RotateWriter) afterRotate() {
	for {
		select {
		case filename := <-r.postCh:
			r.compressFile(filename)
			r.removeOutdatedFiles()
			r.removeOverMaxFiles()
		case <-r.postDone:
			return
		}
	}
}

// init
func (r *RotateWriter) init() error {
	r.ext = filepath.Ext(r.filename)
	r.prefix = r.filename[:len(r.filename)-len(r.ext)]
	r.backupName = r.backupFileName()
	// create writer if exist filename or open it
	if _, err := os.Stat(r.filename); err != nil {
		basePath := path.Dir(r.filename)
		if _, err = os.Stat(basePath); err != nil {
			if err = os.MkdirAll(basePath, defaultDirPerm); err != nil {
				return err
			}
		}
		if r.fp, err = os.Create(r.filename); err != nil {
			return err
		}
	} else if r.fp, err = os.OpenFile(r.filename, os.O_APPEND|os.O_WRONLY, defaultFilePerm); err != nil {
		return err
	}
	closeOnExec(r.fp)
	return nil
}

// backupFileName return backup file name, default layout is prefix-2006-01-02T15:04:05.000.ext
func (r *RotateWriter) backupFileName() string {
	return fmt.Sprintf(
		"%s%s%s%s",
		r.prefix,
		r.opt.delimiter,
		nowDate(r.opt.timeFormat, r.opt.localTime),
		r.ext,
	)
}

// listFiles find outdated files by log layout pattern
func (r *RotateWriter) listFiles() ([]string, error) {
	var pattern string
	if r.opt.gzip {
		pattern = fmt.Sprintf("%s%s*%s.gz", r.prefix, r.opt.delimiter, r.ext)
	} else {
		pattern = fmt.Sprintf("%s%s*%s", r.prefix, r.opt.delimiter, r.ext)
	}
	files, err := filepath.Glob(pattern)
	if err != nil {
		return []string{}, err
	}
	return files, nil
}

// Write
func (r *RotateWriter) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.done.Load() {
		return 0, ErrLogFileClosed
	}
	size := len(data)
	if int64(size) > r.opt.maxSize {
		return 0, ErrDataOversize
	}
	if r.err != nil {
		err := r.err
		r.err = nil
		return 0, err
	}

	if err := r.write(data); err != nil {
		return 0, err
	}
	return size, nil
}

// Close
func (r *RotateWriter) Close() (err error) {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.done.Store(true)
		close(r.postDone)
		if err = r.fp.Sync(); err != nil {
			return
		}
		err = r.fp.Close()
	})
	return err
}

// write
func (r *RotateWriter) write(data []byte) error {
	size := int64(len(data))
	if (r.size + size) > r.opt.maxSize {
		if err := r.rotate(); err != nil {
			return err
		}
		r.size = 0
	}
	if r.fp != nil {
		if _, err := r.fp.Write(data); err != nil {
			return err
		}
		r.size += size
	}
	return nil
}

// rotate
func (r *RotateWriter) rotate() error {
	if r.fp != nil {
		if err := r.fp.Close(); err != nil {
			return err
		}
		r.fp = nil
	}

	_, err := os.Stat(r.filename)
	if err == nil && len(r.backupName) > 0 {
		backupName := r.backupName
		if err = os.Rename(r.filename, backupName); err != nil {
			return err
		}
		// send backupName to compress and remove old logs
		r.postCh <- backupName
	}
	//save next backup name
	r.backupName = r.backupFileName()
	if r.fp, err = os.Create(r.filename); err == nil {
		closeOnExec(r.fp)
	}
	return err
}

// compressFile
func (r *RotateWriter) compressFile(filename string) {
	if !r.opt.gzip {
		return
	}
	if err := gzipFile(filename); err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.err = err
	}
}

// removeOutdatedFiles
func (r *RotateWriter) removeOutdatedFiles() {
	if r.opt.maxDays <= 0 {
		return
	}
	// get old files
	files, err := r.listFiles()
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.err = err
		return
	}
	// get outdated boundary
	boundary := dateline(r.opt.timeFormat, r.opt.localTime, -time.Hour*time.Duration(24*r.opt.maxDays))
	var buf strings.Builder
	_, _ = fmt.Fprintf(&buf, "%s%s%s%s", r.prefix, r.opt.delimiter, boundary, r.ext)
	if r.opt.gzip {
		buf.WriteString(".gz")
	}
	boundaryFile := buf.String()

	for _, file := range files {
		// skip not outdated file
		if file >= boundaryFile {
			continue
		}
		// remove outdated file
		if err = os.Remove(file); err != nil {
			break
		}
	}

	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.err = err
	}
}

// removeOverMaxFiles
func (r *RotateWriter) removeOverMaxFiles() {
	if r.opt.maxBackups <= 0 {
		return
	}
	oldFiles, err := r.listFiles()
	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.err = err
		return
	}

	sort.Strings(oldFiles)
	remain := len(oldFiles)
	if r.opt.maxBackups <= 0 || r.opt.maxBackups >= int64(remain) {
		return
	}
	overMaxFiles := oldFiles[:remain-int(r.opt.maxBackups)]
	for _, file := range overMaxFiles {
		if err = os.Remove(file); err != nil {
			break
		}
	}

	if err != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.err = err
	}
}

// gzipFile
func gzipFile(filename string) (err error) {
	in, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer func() {
		err = multierr.Append(err, in.Close())
	}()

	out, err := os.Create(fmt.Sprintf("%s.gz", filename))
	if err != nil {
		return err
	}
	defer func() {
		err = multierr.Append(err, out.Close())
	}()

	w := gzip.NewWriter(out)
	if _, err = io.Copy(w, in); err != nil {
		return err
	} else if err = w.Close(); err != nil {
		return err
	}

	return os.Remove(filename)
}

// closeOnExec makes sure closing the writer on process forking.
func closeOnExec(file *os.File) {
	if file == nil {
		return
	}
	syscall.CloseOnExec(int(file.Fd()))
}

// nowDate
func nowDate(format string, local bool) string {
	if !local {
		return time.Now().UTC().Format(format)
	}
	return time.Now().Format(format)
}

// dateline
func dateline(format string, local bool, delay time.Duration) string {
	if !local {
		return time.Now().UTC().Add(delay).Format(format)
	}
	return time.Now().Add(delay).Format(format)
}
