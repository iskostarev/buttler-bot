package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	mxid "maunium.net/go/mautrix/id"
)

func canonizeTime(strTime string) string {
	if !strings.Contains(strTime, ":") {
		strTime += ":00"
	}
	return strTime
}

func convertTimezone(strTime string, sourceTz, targetTz *time.Location, targetId string) string {
	now := time.Now()
	nowYear, nowMonth, nowDay := now.Year(), int(now.Month()), now.Day()

	parsedTime, err := time.ParseInLocation("15:04", strTime, sourceTz)
	if err != nil {
		panic(err.Error())
	}

	parsedTime = parsedTime.AddDate(nowYear, nowMonth, nowDay)

	convertedTime := parsedTime.In(targetTz)
	result := fmt.Sprintf("%s %s", convertedTime.Format("15:04"), targetId)

	if convertedTime.Day() > parsedTime.Day() {
		result += " (next day)"
	} else if convertedTime.Day() < parsedTime.Day() {
		result += " (prev day)"
	}

	return result
}

func (session *Session) respondToPing(logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
	if message == "!buttler ping" {
		session.respondMessage(logger, roomId, "Pong!")
	}
}

func (session *Session) respondToPraise(logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
	message = strings.ToLower(message)
	if strings.HasPrefix(message, "good bot") {
		session.respondMessage(logger, roomId, ":)")
	}
	if strings.HasPrefix(message, "bad bot") {
		session.respondMessage(logger, roomId, ":(")
	}
}

func (session *Session) respondToTimezoneHints(logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
	type hint struct {
		time  string
		tzid  string
		tzone *time.Location
	}
	var requiredHints []hint

	for _, tzinfo := range session.Timezones {
		matches := tzinfo.Regex.FindAllStringSubmatch(message, -1)
		if matches == nil {
			continue
		}

		for _, match := range matches {
			time := canonizeTime(match[1])
			requiredHints = append(requiredHints, hint{time, tzinfo.Id, tzinfo.Timezone})
		}
	}

	if requiredHints == nil {
		return
	}

	logger.Debugf("Required timezone hints: %v", requiredHints)

	var response string

	for _, curHint := range requiredHints {
		if response != "" {
			response += "\n"
		}

		line := fmt.Sprintf("%s %s: ", curHint.time, curHint.tzid)
		first := true
		for _, tzinfo := range session.Timezones {
			if tzinfo.Id == curHint.tzid {
				continue
			}

			if !first {
				line += "; "
			}

			line += convertTimezone(curHint.time, curHint.tzone, tzinfo.Timezone, tzinfo.Id)

			first = false
		}
		response += line
	}

	session.respondMessage(logger, roomId, response)
}

func (session *Session) Respond(logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
	defer func() {
		if r := recover(); r != nil {
			logger.Warningf("Error while responding: %v", r)
		}
	}()
	session.respondToPing(logger, roomId, message)
	session.respondToPraise(logger, roomId, message)
	session.respondToTimezoneHints(logger, roomId, message)
}
