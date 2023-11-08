package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	_ "github.com/mattn/go-sqlite3"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	mxevent "maunium.net/go/mautrix/event"
	mxid "maunium.net/go/mautrix/id"
)

type RequestedTimezoneHint struct {
	RoomId mxid.RoomID
	Time   string
	TzId   string
}

type MentionForward struct {
	Regex *regexp.Regexp
}

type MentionForwarderStateKey struct {
	RoomId mxid.RoomID
	UserId mxid.UserID
}

type MentionForwarderState struct {
	LastMentionTime time.Time
}

type Session struct {
	Client                 *mautrix.Client
	StartTimestamp         int64
	MessageCounter         int64
	Timezones              []TimezoneInfo
	MentionForwards        map[mxid.UserID]MentionForward
	LastTzRequests         map[RequestedTimezoneHint]int64
	MentionForwardersState map[MentionForwarderStateKey]*MentionForwarderState
	TimezoneHintCooldown   int64
	MentionForwardCooldown time.Duration
}

type TimezoneInfo struct {
	Id       string
	Color    string
	Timezone *time.Location
	Regex    *regexp.Regexp
}

func buildTimezoneRegexp(innerRegex string) (result *regexp.Regexp, err error) {
	finalRegex := fmt.Sprintf(`(?i)(\d\d?(?:[:.]\d\d)?)\s*(?:%s)`, innerRegex)
	result, err = regexp.Compile(finalRegex)
	return
}

func InitSession(config *Config) (session Session, err error) {
	passwordBytes, err := os.ReadFile(config.Matrix.PasswordPath)
	if err != nil {
		return
	}
	password := strings.TrimSpace(string(passwordBytes))

	pickleKeyBytes, err := os.ReadFile(config.Crypto.PickleKeyPath)
	if err != nil {
		return
	}
	pickleKeyBytes = []byte(strings.TrimSpace(string(pickleKeyBytes)))

	session.Client, err = mautrix.NewClient(config.Matrix.HomeserverUrl, "", "")
	if err != nil {
		return
	}

	reqLogin := mautrix.ReqLogin{
		Type:             mautrix.AuthTypePassword,
		Identifier:       mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: config.Matrix.Username},
		Password:         password,
		StoreCredentials: true,
	}

	if config.Crypto.Enabled {
		logrus.Infof("Initializing CryptoHelper...")
		cryptoHelper, err := cryptohelper.NewCryptoHelper(session.Client, pickleKeyBytes, config.Crypto.Database)
		if err != nil {
			return session, err
		}

		cryptoHelper.LoginAs = &reqLogin
		err = cryptoHelper.Init()
		if err != nil {
			return session, err
		}

		session.Client.Crypto = cryptoHelper
		logrus.Infof("CryptoHelper initialized, logged in")
	} else {
		_, err = session.Client.Login(&reqLogin)
		logrus.Infof("Logged in")
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

		tzinfo := TimezoneInfo{Id: tz.Id, Color: tz.Color, Regex: regex, Timezone: timezone}
		session.Timezones = append(session.Timezones, tzinfo)
	}

	session.MentionForwards = make(map[mxid.UserID]MentionForward)
	for _, mfEntry := range config.MentionForwards {
		userId := mxid.UserID(mfEntry.UserId)

		_, repeat := session.MentionForwards[userId]
		if repeat {
			return session, fmt.Errorf("Repeating UserId %s in MentionForwards", userId)
		}

		regex, err := regexp.Compile(mfEntry.Regex)
		if err != nil {
			return session, nil
		}

		session.MentionForwards[userId] = MentionForward{Regex: regex}
	}

	session.LastTzRequests = make(map[RequestedTimezoneHint]int64)
	session.TimezoneHintCooldown = config.TimezoneHintCooldown

	session.MentionForwardCooldown, err = time.ParseDuration(config.MentionForwardCooldown)
	if err != nil {
		return
	}

	session.MentionForwardersState = make(map[MentionForwarderStateKey]*MentionForwarderState)

	return
}

func (session *Session) GetMentionForwarderState(roomId mxid.RoomID, userId mxid.UserID) (result *MentionForwarderState) {
	key := MentionForwarderStateKey{RoomId: roomId, UserId: userId}
	result, ok := session.MentionForwardersState[key]
	if ok {
		return
	}

	result = &MentionForwarderState{}
	session.MentionForwardersState[key] = result

	return
}

func (session *Session) SendMessage(logger logrus.FieldLogger, roomId mxid.RoomID, message Message) error {
	if _, err := session.Client.SendMessageEvent(roomId, mxevent.EventMessage, message.AsEvent()); err != nil {
		logger.Errorf("Failed to respond: %s", err.Error())
		return err
	}
	return nil
}

func (session *Session) FindDirectMessageRoom(logger logrus.FieldLogger, userId mxid.UserID) (roomId mxid.RoomID, err error) {
	direct := mxevent.DirectChatsEventContent{}
	if err = session.Client.GetAccountData("m.direct", &direct); err != nil {
		return
	}

	directRooms := direct[userId]
	if directRooms == nil || len(directRooms) == 0 {
		err = errors.New("No direct message room is open")
		return
	}

	roomId = directRooms[0]
	return
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

	session.Respond(logger, evt.RoomID, evt.Content.AsMessage())

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

	if emem.IsDirect {
		err := session.Client.SetAccountData("m.direct", &mxevent.DirectChatsEventContent{evt.Sender: []mxid.RoomID{evt.RoomID}})
		if err != nil {
			logrus.Errorf("Failed to mark room %s (with user %s) as direct", evt.RoomID, evt.Sender)
		} else {
			logrus.Infof("Marked room %s (with user %s) as direct", evt.RoomID, evt.Sender)
		}
	}
}

func (session *Session) RunSyncLoop() {
	syncer := session.Client.Syncer.(*mautrix.DefaultSyncer)

	syncer.OnEventType(mxevent.EventMessage, session.handleMessage)
	syncer.OnEventType(mxevent.StateMember, session.handleMembership)

	if err := session.Client.Sync(); err != nil {
		panic(errors.Wrap(err, "Sync failed"))
	}
}
