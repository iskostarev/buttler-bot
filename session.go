package main

import (
	"fmt"
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

type RequestedTimezoneHint struct {
	RoomId mxid.RoomID
	Time   string
	TzId   string
}

type Session struct {
	Client               *mautrix.Client
	StartTimestamp       int64
	MessageCounter       int64
	Timezones            []TimezoneInfo
	LastTzRequests       map[RequestedTimezoneHint]int64
	TimezoneHintCooldown int64
}

type TimezoneInfo struct {
	Id       string
	Timezone *time.Location
	Regex    *regexp.Regexp
}

func buildTimezoneRegexp(innerRegex string) (result *regexp.Regexp, err error) {
	finalRegex := fmt.Sprintf(`(?i)(\d\d?(?:[:.]\d\d)?)\s*(?:%s)`, innerRegex)
	result, err = regexp.Compile(finalRegex)
	return
}

func InitSession(config *Config) (session Session, err error) {
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

	session.LastTzRequests = make(map[RequestedTimezoneHint]int64)
	session.TimezoneHintCooldown = config.TimezoneHintCooldown

	return
}

func (session *Session) RespondMessage(logger logrus.FieldLogger, roomId mxid.RoomID, message Message) bool {
	if _, err := session.Client.SendMessageEvent(roomId, mxevent.EventMessage, message.AsEvent()); err != nil {
		logger.Errorf("Failed to respond: %s", err.Error())
		return false
	}
	return true
}

func (session *Session) handleMessage(source mautrix.EventSource, evt *mxevent.Event) {
	session.MessageCounter++

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
		"msgno":    session.MessageCounter,
	})

	message := evt.Content.Raw["body"].(string)
	logger.Debugf("Message: %v\n", message)

	session.Respond(logger, evt.RoomID, message)

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
