package observability

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

func NewFileLogger(root string) (*log.Logger, io.Closer, error) {
	logDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, err
	}

	file, err := os.OpenFile(filepath.Join(logDir, "aiblog.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}

	logger := log.New(file, "", log.LstdFlags|log.Lmicroseconds|log.LUTC)
	return logger, file, nil
}
