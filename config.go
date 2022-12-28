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
	Matrix struct {
		UserId          string
		AccessTokenPath string
		HomeserverUrl   string
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
