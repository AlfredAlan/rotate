package rotate

import "time"

const (
	defaultDirPerm    = 0755
	defaultFilePerm   = 0644
	megabyte          = 1024 * 1024
	defaultMaxDays    = 30
	defaultMaxSize    = 128
	defaultMaxBackups = 30
	defaultDelimiter  = "-"
	defaultTimeFormat = time.RFC3339 //"2006-01-02T15:04:05Z07:00"
)
