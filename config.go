package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"

	"github.com/pkg/errors"
)

type Config struct {
	Logging struct {
		Level  string
		Output string
	}
	Crypto struct {
		Enabled       bool
		PickleKeyPath string
		Database      string
	}
	Matrix struct {
		Username      string
		PasswordPath  string
		HomeserverUrl string
	}
	Timezones []struct {
		Id       string
		Timezone string
		Regex    string
		Color    string
	}
	TimezoneHintCooldown int64
}

func ParseConfig() (result Config) {
	configFile := flag.String("config", "config.json", "Path to the configuration file")

	flag.Parse()

	configBytes, err := ioutil.ReadFile(*configFile)
	if err != nil {
		panic(errors.Wrap(err, "Failed to open configuration file"))
	}

	json.Unmarshal(configBytes, &result)

	return
}
