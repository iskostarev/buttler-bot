package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"maunium.net/go/mautrix"
	mxevent "maunium.net/go/mautrix/event"
	mxid "maunium.net/go/mautrix/id"
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
	}
}

type TimezoneInfo struct {
	Id       string
	Timezone *time.Location
	Regex    *regexp.Regexp
}

type Session struct {
	Client         *mautrix.Client
	StartTimestamp int64
	Timezones      []TimezoneInfo
}

func parseConfig() (result Config) {
	configFile := flag.String("config", "config.json", "Path to the configuration file")

	flag.Parse()

	configBytes, err := ioutil.ReadFile(*configFile)
	if err != nil {
		panic(errors.Wrap(err, "Failed to open configuration file"))
	}

	json.Unmarshal(configBytes, &result)

	return
}

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

func buildTimezoneRegexp(innerRegex string) (result *regexp.Regexp, err error) {
	finalRegex := fmt.Sprintf(`(?i)(\d\d?(?::\d\d)?)\s*(?:%s)`, innerRegex)
	result, err = regexp.Compile(finalRegex)
	return
}

func initSession(config *Config) (session Session, err error) {
	userId := mxid.UserID(config.Matrix.UserId)

	accessTokenBytes, err := os.ReadFile(config.Matrix.AccessTokenPath)
	if err != nil {
		return
	}
	accessToken := strings.TrimSpace(string(accessTokenBytes))

	session.Client, err = mautrix.NewClient(config.Matrix.HomeserverUrl, userId, accessToken)
	if err != nil {
		return
	}

	session.StartTimestamp = time.Now().UnixMilli()

	for _, tz := range config.Timezones {
		regex, err := buildTimezoneRegexp(tz.Regex)
		if err != nil {
			return session, err
		}

		timezone, err := time.LoadLocation(tz.Timezone)
		if err != nil {
			return session, nil
		}

		tzinfo := TimezoneInfo{Id: tz.Id, Regex: regex, Timezone: timezone}
		session.Timezones = append(session.Timezones, tzinfo)
	}

	return
}

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

func (session *Session) respondMessage(logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
	if _, err := session.Client.SendText(roomId, message); err != nil {
		logger.Errorf("Failed to respond: %s", err.Error())
	}
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

func (session *Session) respond(logger logrus.FieldLogger, roomId mxid.RoomID, message string) {
	defer func() {
		if r := recover(); r != nil {
			logger.Warningf("Error while responding: %v", r)
		}
	}()
	session.respondToPing(logger, roomId, message)
	session.respondToPraise(logger, roomId, message)
	session.respondToTimezoneHints(logger, roomId, message)
}

func (session *Session) handleMessage(source mautrix.EventSource, evt *mxevent.Event) {
	if evt.Sender == session.Client.UserID {
		return
	}

	if evt.Timestamp < session.StartTimestamp {
		return
	}

	logger := logrus.WithFields(logrus.Fields{
		"event_id": evt.ID,
		"room_id":  evt.RoomID,
		"sender":   evt.Sender,
	})

	message := evt.Content.Raw["body"].(string)
	logger.Debugf("Message: %v\n", message)

	session.respond(logger, evt.RoomID, message)

	if err := session.Client.MarkRead(evt.RoomID, evt.ID); err != nil {
		logger.Errorf("Failed to mark as read: %s", err.Error())
	}
}

func (session *Session) handleMembership(source mautrix.EventSource, evt *mxevent.Event) {
	emem := evt.Content.AsMember()
	if emem.Membership != mxevent.MembershipInvite {
		return
	}

	_, err := session.Client.JoinRoomByID(evt.RoomID)
	if err != nil {
		logrus.Errorf("Failed to join room %s: %s", evt.RoomID, err.Error())
		return
	}

	logrus.Infof("Joined room %s", evt.RoomID)
}

func (session *Session) RunSyncLoop() {
	syncer := session.Client.Syncer.(*mautrix.DefaultSyncer)

	syncer.OnEventType(mxevent.EventMessage, session.handleMessage)
	syncer.OnEventType(mxevent.StateMember, session.handleMembership)

	if err := session.Client.Sync(); err != nil {
		panic(errors.Wrap(err, "Sync failed"))
	}
}

func main() {
	config := parseConfig()

	configureLogging(&config)

	session, err := initSession(&config)
	if err != nil {
		panic(errors.Wrap(err, "Failed to initialize session"))
	}

	session.RunSyncLoop()

	fmt.Println("Done")
}
