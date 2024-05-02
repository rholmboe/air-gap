package kafka

import (
	"log"
	"os"
)

var Logger = log.New(os.Stdout, "", log.LstdFlags)
func SetLogger(newLogger *log.Logger) {
	Logger = newLogger
}