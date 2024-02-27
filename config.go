package main

import (
	"encoding/json"
	"flag"
	"os"

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

	MentionForwards []struct {
		UserId string
		Regex  string
	}
	MentionForwardCooldown string
}

func ParseConfig() (result Config) {
	configFile := flag.String("config", "config.json", "Path to the configuration file")

	flag.Parse()

	configBytes, err := os.ReadFile(*configFile)
	if err != nil {
		panic(errors.Wrap(err, "Failed to open configuration file"))
	}

	err = json.Unmarshal(configBytes, &result)
	if err != nil {
		panic(err)
	}

	return
}
