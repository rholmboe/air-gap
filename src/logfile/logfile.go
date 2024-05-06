package logfile

import (
	"os"
	"path/filepath"
    "log"
)

// This needs to be in all main packages
func SetLogFile(filename string, logger *log.Logger) error {
    err := os.MkdirAll(filepath.Dir(filename), 0755)
    if err != nil {
        return err
    }
    f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
    if err != nil {
        return err
    }
    logger.SetOutput(f)
    return nil
}