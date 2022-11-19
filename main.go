package main

import (
	"os"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func configureLogging(config *Config) {
	logLevel, err := logrus.ParseLevel(config.Logging.Level)
	if err != nil {
		panic(errors.Wrap(err, "Invalid log level"))
	}
	logrus.SetLevel(logLevel)

	if config.Logging.Output != "" {
		f, err := os.OpenFile(config.Logging.Output, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			panic(errors.Wrap(err, "Failed to open log file"))
		}
		logrus.SetOutput(f)
	}
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			logrus.Fatal(r)
			os.Exit(1)
		}
	}()

	config := ParseConfig()

	configureLogging(&config)

	session, err := InitSession(&config)
	if err != nil {
		panic(errors.Wrap(err, "Failed to initialize session"))
	}

	session.RunSyncLoop()
}
